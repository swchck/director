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

// defaultPersistenceMaxConcurrency is the maximum number of concurrent
// persistence goroutines per view. When the limit is reached, newer saves
// are dropped — the next Swap will produce a fresh save anyway.
const defaultPersistenceMaxConcurrency = 2

// ViewPersistence allows View results to be stored externally (e.g. Redis).
// This enables sharing precomputed views across replicas without recomputation.
//
// Implementations are provided in the cache package (e.g. RedisViewStore).
type ViewPersistence interface {
	Save(ctx context.Context, key string, data []byte) error
	Load(ctx context.Context, key string) ([]byte, error)
}

// viewSnapshot holds a precomputed, immutable view result.
type viewSnapshot[T any] struct {
	items   []T
	version Version
}

// View is an auto-updating materialized view over a Collection[T].
//
// Define filter, sort, and limit rules once — the View automatically recomputes
// when the source collection changes. Results are cached in memory (lock-free reads)
// and optionally persisted to an external store like Redis.
//
// Example — keep a sorted, filtered cache of tech articles:
//
//	techView := config.NewView("tech-by-level", articles,
//	    []config.FilterOption[Article]{
//	        config.Where(func(a Article) bool { return a.Category == "tech" }),
//	        config.SortBy(func(a, b Article) int { return cmp.Compare(a.Level, b.Level) }),
//	        config.Limit[Article](100),
//	    },
//	)
//
// The view can be further queried without copying the full collection:
//
//	top := techView.Filter(config.Limit[Article](10))
//	item, ok := techView.Find(func(a Article) bool { return a.ID == 42 })
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

// WithPersistence enables external persistence for the view.
// When set, the view saves its computed results after each recomputation
// and loads from the store on creation for a warm start.
func WithPersistence[T any](p ViewPersistence) ViewOption[T] {
	return func(v *View[T]) {
		v.persistence = p
	}
}

// NewView creates an auto-updating view over a source Collection.
//
// name is used as the persistence key and for logging.
// filters define the transformation pipeline (Where, SortBy, Limit, Offset).
// The view immediately computes its initial state from the current collection data.
func NewView[T any](name string, source *Collection[T], filters []FilterOption[T], opts ...ViewOption[T]) *View[T] {
	v := &View[T]{
		name:    name,
		source:  source,
		filters: filters,
	}

	for _, opt := range opts {
		opt(v)
	}

	// Initialize semaphore for bounded persistence concurrency.
	if v.persistence != nil {
		v.persistSem = make(chan struct{}, defaultPersistenceMaxConcurrency)
	}

	// Initialize with empty snapshot.
	v.data.Store(&viewSnapshot[T]{})

	// Try loading from persistence first.
	if v.persistence != nil {
		v.loadFromPersistence()
	}

	// Compute initial view from current source data.
	v.recompute(source.All(), source.Version())

	// Register for future updates.
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

// Close unsubscribes the view from its source collection. After Close,
// the view stops recomputing on source changes. It is safe to call
// Close multiple times. Reads remain valid after Close but return
// stale data.
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
//
// The predicate must not panic. If it does, the panic propagates to the caller.
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
//
// The predicate must not panic. If it does, the panic propagates to the caller.
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

// OnChange registers a callback that fires after the view recomputes.
// Returns a function that removes the hook when called.
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
		// applyFilters returns a new slice; copy it to own the data.
		items := applyFilters(sourceItems, v.filters)
		stored = make([]T, len(items))
		copy(stored, items)
	} else {
		// No filters — skip applyFilters, just copy source directly.
		stored = make([]T, len(sourceItems))
		copy(stored, sourceItems)
	}

	old := v.data.Swap(&viewSnapshot[T]{
		items:   stored,
		version: version,
	})

	// Persist asynchronously to avoid blocking the OnChange callback chain.
	// Uses a semaphore to bound concurrency — if max goroutines are in flight,
	// this save is skipped (the next Swap will produce a fresher save).
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

	// Fire view hooks with panic recovery.
	v.mu.RLock()
	hooks := v.hooks
	v.mu.RUnlock()

	// Defensive copies so hooks cannot mutate the internal snapshots.
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

func (v *View[T]) loadFromPersistence() {
	ctx, cancel := v.persistCtx()
	defer cancel()

	data, err := v.persistence.Load(ctx, v.name)
	if err != nil || len(data) == 0 {
		return
	}

	var items []T
	if err := json.Unmarshal(data, &items); err != nil {
		return
	}

	v.data.Store(&viewSnapshot[T]{items: items})
}

// SingletonView is an auto-updating materialized transformation of a Singleton[T].
//
// It allows applying a transformation function to the singleton value and caching
// the result. Useful when the raw singleton needs post-processing before use.
//
// Example — extract and cache a specific field from a settings singleton:
//
//	featureFlags := config.NewSingletonView("features", settings,
//	    func(s Settings) FeatureFlags { return s.Features },
//	)
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

// NewSingletonView creates a transformed view of a Singleton.
//
// transform is called each time the source singleton changes, and the result
// is cached for lock-free reads.
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

	// Load from persistence.
	if v.persistence != nil {
		v.loadFromPersistence()
	}

	// Compute initial value.
	if val, ok := source.Get(); ok {
		v.recompute(val, source.Version())
	}

	// Register for updates.
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

// Close unsubscribes the view from its source singleton. After Close,
// the view stops recomputing on source changes. It is safe to call
// Close multiple times.
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

// CompositeView combines multiple Views into a single read endpoint.
// Useful when you need to merge results from views on the same collection
// with different rules.
//
// Example:
//
//	combo := config.NewCompositeView("all-specials", foodView, drinkView)
//	allSpecials := combo.All() // items from both views, deduplicated by user function
type CompositeView[T any] struct {
	name  string
	views []*View[T]
	dedup func(T, T) bool // returns true if items are the same (for dedup)
}

// NewCompositeView creates a view that merges results from multiple source views.
//
// dedup is optional — if nil, no deduplication is performed and results are concatenated.
// If provided, it should return true when two items represent the same entity.
//
// Note: when dedup is set, deduplication uses linear search per item, resulting
// in O(n²) time complexity where n is the total number of items across all views.
// For large datasets, consider pre-filtering at the View level to minimize overlap.
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

// Count returns the total number of items across all source views.
// When no dedup function is set, this avoids allocating the merged slice.
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

// ErrorFunc is a callback for handling non-fatal errors in views.
// Used by persistence operations that run in background goroutines.
type ErrorFunc func(viewName string, err error)

// WithErrorHandler sets an error callback for persistence failures.
// Without this, persistence errors are silently ignored.
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
