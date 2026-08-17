package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const defaultPersistenceTimeout = 10 * time.Second

// defaultPersistenceMaxConcurrency caps concurrent persistence goroutines per view; past
// the limit a save is silently skipped, since the next Swap produces a fresh one anyway.
const defaultPersistenceMaxConcurrency = 2

// ViewPersistence stores computed view results externally (e.g. Redis) for warm starts;
// implemented by cache/redis.ViewStore and cache/memory.ViewStore. See keepWarmStart.
type ViewPersistence interface {
	Save(ctx context.Context, key string, data []byte) error
	Load(ctx context.Context, key string) ([]byte, error)
}

// keepWarmStart reports whether a stored snapshot stands instead of the initial compute:
// only while the source is unloaded, since after that the view must be a function of it.
func keepWarmStart(loaded bool, sourceVersion Version) bool {
	return loaded && sourceVersion.IsZero()
}

type viewSnapshot[T any] struct {
	items   []T
	version Version
}

// View is an auto-updating materialized projection of a Collection[T]: filter, sort and
// limit rules applied once per source Swap, cached in memory for lock-free reads.
type View[T any] struct {
	name               string
	source             *Collection[T]
	filters            []FilterOption[T]
	persistence        ViewPersistence
	persistenceTimeout time.Duration
	onError            ErrorFunc
	persistSem         chan struct{}
	unsub              func()
	closeOnce          sync.Once

	data atomic.Pointer[viewSnapshot[T]]

	mu    sync.RWMutex
	hooks []func(old, new []T)
}

// ViewOption configures optional View behavior.
type ViewOption[T any] func(*View[T])

// WithPersistence enables external persistence for the view: computed results are
// saved after each recomputation, and a stored one warms the view on creation.
func WithPersistence[T any](p ViewPersistence) ViewOption[T] {
	return func(v *View[T]) {
		v.persistence = p
	}
}

// NewView creates an auto-updating view; name doubles as the persistence key, filters are
// the pipeline. It computes at once, unless persistence warmed it while source is unloaded.
func NewView[T any](name string, source *Collection[T], filters []FilterOption[T], opts ...ViewOption[T]) *View[T] {
	v := &View[T]{
		name:    name,
		source:  source,
		filters: filters,
	}

	for _, opt := range opts {
		opt(v)
	}

	if v.persistence != nil {
		v.persistSem = make(chan struct{}, defaultPersistenceMaxConcurrency)
	}

	v.data.Store(&viewSnapshot[T]{})

	if !keepWarmStart(v.loadFromPersistence(), source.Version()) {
		v.recompute(source.All(), source.Version())
	}

	v.unsub = source.OnChange(func(_, newItems []T) {
		v.recompute(newItems, source.Version())
	})

	return v
}

// Name returns the view name.
func (v *View[T]) Name() string {
	return v.name
}

// Version returns the current snapshot version.
func (v *View[T]) Version() Version {
	return v.data.Load().version
}

// Close unsubscribes the view from its source; idempotent. Reads stay valid but go stale.
func (v *View[T]) Close() {
	v.closeOnce.Do(func() {
		if v.unsub != nil {
			v.unsub()
		}
	})
}

// All returns a copy of the cached view items.
func (v *View[T]) All() []T {
	items := v.data.Load().items
	result := make([]T, len(items))
	copy(result, items)

	return result
}

// Count returns the number of items in the view.
func (v *View[T]) Count() int {
	return len(v.data.Load().items)
}

// First returns the first item in the view, or false if empty.
func (v *View[T]) First() (T, bool) {
	items := v.data.Load().items
	if len(items) == 0 {
		var zero T
		return zero, false
	}

	return items[0], true
}

// Find returns the first item in the view matching the predicate.
func (v *View[T]) Find(pred func(T) bool) (T, bool) {
	for _, item := range v.data.Load().items {
		if pred(item) {
			return item, true
		}
	}

	var zero T
	return zero, false
}

// FindMany returns all items in the view matching the predicate.
func (v *View[T]) FindMany(pred func(T) bool) []T {
	var result []T
	for _, item := range v.data.Load().items {
		if pred(item) {
			result = append(result, item)
		}
	}

	return result
}

// Filter applies additional filter options on top of the cached view result.
func (v *View[T]) Filter(opts ...FilterOption[T]) []T {
	return applyFilters(v.data.Load().items, opts)
}

// OnChange registers a callback fired after each recompute; the unregister is idempotent.
func (v *View[T]) OnChange(fn func(old, new []T)) func() {
	v.mu.Lock()
	defer v.mu.Unlock()

	idx := len(v.hooks)
	v.hooks = append(v.hooks, fn)

	return func() {
		v.mu.Lock()
		defer v.mu.Unlock()

		if idx < len(v.hooks) {
			v.hooks[idx] = nil
		}
	}
}

// recompute applies the view's filter pipeline and atomically swaps the result.
func (v *View[T]) recompute(sourceItems []T, version Version) {
	var stored []T
	if len(v.filters) > 0 {
		// Re-copy to trim: filters hand back subslices or source-sized slices, either of
		// which would pin a source-sized backing array for the life of the snapshot.
		items := applyFilters(sourceItems, v.filters)
		stored = make([]T, len(items))
		copy(stored, items)
	} else {
		stored = make([]T, len(sourceItems))
		copy(stored, sourceItems)
	}

	old := v.data.Swap(&viewSnapshot[T]{
		items:   stored,
		version: version,
	})

	// Async so a slow store never delays the swap chain.
	if v.persistence != nil {
		select {
		case v.persistSem <- struct{}{}:
			go func() {
				defer func() { <-v.persistSem }()
				v.saveToPersistence(stored)
			}()
		default:
			// Semaphore full — skip this save.
		}
	}

	v.mu.RLock()
	hooks := v.hooks
	v.mu.RUnlock()

	// Copies, shared between hooks: they isolate the published snapshot, not each other.
	oldCopy := make([]T, len(old.items))
	copy(oldCopy, old.items)
	newCopy := make([]T, len(stored))
	copy(newCopy, stored)

	wrappers := make([]func(), 0, len(hooks))
	for _, fn := range hooks {
		if fn == nil {
			continue
		}

		fn := fn
		wrappers = append(wrappers, func() { fn(oldCopy, newCopy) })
	}

	if err := safeCallHooks(wrappers...); err != nil {
		if v.onError != nil {
			v.onError(v.name, err)
		}
	}
}

func (v *View[T]) persistCtx() (context.Context, context.CancelFunc) {
	timeout := v.persistenceTimeout
	if timeout == 0 {
		timeout = defaultPersistenceTimeout
	}

	return context.WithTimeout(context.Background(), timeout)
}

func (v *View[T]) saveToPersistence(items []T) {
	data, err := json.Marshal(items)
	if err != nil {
		if v.onError != nil {
			v.onError(v.name, fmt.Errorf("marshal view: %w", err))
		}
		return
	}

	ctx, cancel := v.persistCtx()
	defer cancel()

	if err := v.persistence.Save(ctx, v.name, data); err != nil {
		if v.onError != nil {
			v.onError(v.name, fmt.Errorf("save view: %w", err))
		}
	}
}

// loadFromPersistence installs a stored snapshot and reports whether it did. A read that
// fails, is absent, or decodes to null is not an error: the view computes instead.
func (v *View[T]) loadFromPersistence() bool {
	if v.persistence == nil {
		return false
	}

	ctx, cancel := v.persistCtx()
	defer cancel()

	data, err := v.persistence.Load(ctx, v.name)
	if err != nil || len(data) == 0 {
		return false
	}

	var items []T
	if err := json.Unmarshal(data, &items); err != nil || items == nil {
		return false
	}

	v.data.Store(&viewSnapshot[T]{items: items})

	return true
}

// SingletonView is an auto-updating materialized transformation of a Singleton[T], cached
// for lock-free reads. Useful when the raw singleton needs post-processing before use.
type SingletonView[T any, R any] struct {
	name               string
	source             *Singleton[T]
	transform          func(T) R
	persistence        ViewPersistence
	persistenceTimeout time.Duration
	onError            ErrorFunc
	persistSem         chan struct{}
	unsub              func()
	closeOnce          sync.Once

	data atomic.Pointer[singletonViewSnapshot[R]]
}

type singletonViewSnapshot[R any] struct {
	value   *R
	version Version
}

// SingletonViewOption configures optional SingletonView behavior.
type SingletonViewOption[T any, R any] func(*SingletonView[T, R])

// WithSingletonViewPersistence enables external persistence for the singleton view.
func WithSingletonViewPersistence[T any, R any](p ViewPersistence) SingletonViewOption[T, R] {
	return func(v *SingletonView[T, R]) {
		v.persistence = p
	}
}

// WithSingletonViewErrorHandler sets an error callback for persistence failures.
func WithSingletonViewErrorHandler[T any, R any](fn ErrorFunc) SingletonViewOption[T, R] {
	return func(v *SingletonView[T, R]) {
		v.onError = fn
	}
}

// WithSingletonViewPersistenceTimeout sets the timeout for persistence operations.
// Default: 10 seconds.
func WithSingletonViewPersistenceTimeout[T any, R any](d time.Duration) SingletonViewOption[T, R] {
	return func(v *SingletonView[T, R]) {
		v.persistenceTimeout = d
	}
}

// NewSingletonView creates a transformed view of a Singleton; transform runs on every
// source change and its result is cached.
func NewSingletonView[T any, R any](name string, source *Singleton[T], transform func(T) R, opts ...SingletonViewOption[T, R]) *SingletonView[T, R] {
	v := &SingletonView[T, R]{
		name:      name,
		source:    source,
		transform: transform,
	}

	for _, opt := range opts {
		opt(v)
	}

	if v.persistence != nil {
		v.persistSem = make(chan struct{}, defaultPersistenceMaxConcurrency)
	}

	v.data.Store(&singletonViewSnapshot[R]{})

	v.loadFromPersistence()

	// keepWarmStart's rule via the source's own flag: nothing to transform while unloaded.
	if val, ok := source.Get(); ok {
		v.recompute(val, source.Version())
	}

	v.unsub = source.OnChange(func(_, newVal *T) {
		if newVal != nil {
			v.recompute(*newVal, source.Version())
		}
	})

	return v
}

// Get returns the cached transformed value, or false if the source hasn't loaded yet.
func (v *SingletonView[T, R]) Get() (R, bool) {
	snap := v.data.Load()
	if snap.value == nil {
		var zero R
		return zero, false
	}

	return *snap.value, true
}

// Name returns the view name.
func (v *SingletonView[T, R]) Name() string {
	return v.name
}

// Close unsubscribes the view from its source; idempotent. Reads stay valid but go stale.
func (v *SingletonView[T, R]) Close() {
	v.closeOnce.Do(func() {
		if v.unsub != nil {
			v.unsub()
		}
	})
}

func (v *SingletonView[T, R]) recompute(source T, version Version) {
	result := v.transform(source)
	v.data.Store(&singletonViewSnapshot[R]{
		value:   &result,
		version: version,
	})

	if v.persistence != nil {
		select {
		case v.persistSem <- struct{}{}:
			go func() {
				defer func() { <-v.persistSem }()
				v.saveToPersistence(result)
			}()
		default:
			// Semaphore full — skip this save.
		}
	}
}

func (v *SingletonView[T, R]) persistCtx() (context.Context, context.CancelFunc) {
	timeout := v.persistenceTimeout
	if timeout == 0 {
		timeout = defaultPersistenceTimeout
	}

	return context.WithTimeout(context.Background(), timeout)
}

func (v *SingletonView[T, R]) saveToPersistence(result R) {
	data, err := json.Marshal(result)
	if err != nil {
		if v.onError != nil {
			v.onError(v.name, fmt.Errorf("marshal singleton view: %w", err))
		}
		return
	}

	ctx, cancel := v.persistCtx()
	defer cancel()

	if err := v.persistence.Save(ctx, v.name, data); err != nil {
		if v.onError != nil {
			v.onError(v.name, fmt.Errorf("save singleton view: %w", err))
		}
	}
}

func (v *SingletonView[T, R]) loadFromPersistence() {
	if v.persistence == nil {
		return
	}

	ctx, cancel := v.persistCtx()
	defer cancel()

	data, err := v.persistence.Load(ctx, v.name)
	if err != nil || len(data) == 0 {
		return
	}

	var result R
	if err := json.Unmarshal(data, &result); err != nil {
		return
	}

	v.data.Store(&singletonViewSnapshot[R]{value: &result})
}

// CompositeView merges several View[T] into one read endpoint. Unlike its siblings it holds no
// snapshot and subscribes to nothing — reads fan out, so there is no Version and no Close.
type CompositeView[T any] struct {
	name  string
	views []*View[T]
	dedup func(T, T) bool // nil disables deduplication
}

// NewCompositeView merges results from multiple views; dedup reports same-entity, nil just
// concatenates. Dedup is a linear scan per item — O(n²) in total items across all views.
func NewCompositeView[T any](name string, dedup func(a, b T) bool, views ...*View[T]) *CompositeView[T] {
	return &CompositeView[T]{
		name:  name,
		views: views,
		dedup: dedup,
	}
}

// All returns the merged items from all source views.
func (cv *CompositeView[T]) All() []T {
	var result []T

	for _, v := range cv.views {
		items := v.data.Load().items
		if cv.dedup == nil {
			result = append(result, items...)
			continue
		}

		for _, item := range items {
			if !cv.contains(result, item) {
				result = append(result, item)
			}
		}
	}

	return result
}

// Count returns the total number of items across all source views. Without a dedup
// function it sums lengths instead of building the merged slice.
func (cv *CompositeView[T]) Count() int {
	if cv.dedup != nil {
		return len(cv.All())
	}

	total := 0
	for _, v := range cv.views {
		total += len(v.data.Load().items)
	}

	return total
}

func (cv *CompositeView[T]) contains(items []T, candidate T) bool {
	for _, item := range items {
		if cv.dedup(item, candidate) {
			return true
		}
	}

	return false
}

// Name returns the composite view name.
func (cv *CompositeView[T]) Name() string {
	return cv.name
}

// ErrorFunc reports non-fatal view errors: persistence failures from a background
// goroutine, recovered hook panics from the Swap goroutine. Must be concurrency-safe.
type ErrorFunc func(viewName string, err error)

// WithErrorHandler sets the callback for this view's persistence failures and recovered
// hook panics. Without it, both are silently dropped.
func WithErrorHandler[T any](fn ErrorFunc) ViewOption[T] {
	return func(v *View[T]) {
		v.onError = fn
	}
}

// WithPersistenceTimeout sets the timeout for persistence Save/Load operations.
// Default: 10 seconds.
func WithPersistenceTimeout[T any](d time.Duration) ViewOption[T] {
	return func(v *View[T]) {
		v.persistenceTimeout = d
	}
}
