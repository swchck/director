package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type indexSnapshot[K comparable, V any] struct {
	index   map[K][]V
	version Version
}

// IndexedViewOption configures an IndexedView.
type IndexedViewOption[T any, K comparable] func(*IndexedView[T, K])

// WithIndexPersistence enables external persistence for the indexed view.
func WithIndexPersistence[T any, K comparable](p ViewPersistence) IndexedViewOption[T, K] {
	return func(v *IndexedView[T, K]) {
		v.persistence = p
	}
}

// WithIndexErrorHandler sets the callback for this view's persistence failures and
// recovered hook panics. See ErrorFunc.
func WithIndexErrorHandler[T any, K comparable](fn ErrorFunc) IndexedViewOption[T, K] {
	return func(v *IndexedView[T, K]) {
		v.onError = fn
	}
}

// IndexedView is an auto-updating map[K][]T grouping of a Collection, rebuilt on every
// source change so that reads stay O(1) lookups against an immutable snapshot.
type IndexedView[T any, K comparable] struct {
	name               string
	source             *Collection[T]
	keyFn              func(T) K
	persistence        ViewPersistence
	persistenceTimeout time.Duration
	onError            ErrorFunc
	persistSem         chan struct{}
	unsub              func()
	closeOnce          sync.Once

	data atomic.Pointer[indexSnapshot[K, T]]

	mu    sync.RWMutex
	hooks []func(old, new map[K][]T)
}

// NewIndexedView creates an auto-updating grouped view of a Collection, with keyFn
// extracting the grouping key from each item.
func NewIndexedView[T any, K comparable](name string, source *Collection[T], keyFn func(T) K, opts ...IndexedViewOption[T, K]) *IndexedView[T, K] {
	v := &IndexedView[T, K]{
		name:   name,
		source: source,
		keyFn:  keyFn,
	}

	for _, opt := range opts {
		opt(v)
	}

	if v.persistence != nil {
		v.persistSem = make(chan struct{}, defaultPersistenceMaxConcurrency)
	}

	v.data.Store(&indexSnapshot[K, T]{index: make(map[K][]T)})

	if !keepWarmStart(v.loadFromPersistence(), source.Version()) {
		v.recompute(source.All(), source.Version())
	}

	v.unsub = source.OnChange(func(_, newItems []T) {
		v.recompute(newItems, source.Version())
	})

	return v
}

// Name returns the view name.
func (v *IndexedView[T, K]) Name() string {
	return v.name
}

// Close unsubscribes the view from its source; idempotent. Reads stay valid but go stale.
func (v *IndexedView[T, K]) Close() {
	v.closeOnce.Do(func() {
		if v.unsub != nil {
			v.unsub()
		}
	})
}

// Get returns a copy of the items for the given key, or nil if the key doesn't exist.
func (v *IndexedView[T, K]) Get(key K) []T {
	items := v.data.Load().index[key]
	if items == nil {
		return nil
	}

	result := make([]T, len(items))
	copy(result, items)

	return result
}

// All returns the full index as a map copy.
func (v *IndexedView[T, K]) All() map[K][]T {
	snap := v.data.Load().index
	result := make(map[K][]T, len(snap))

	for k, items := range snap {
		copied := make([]T, len(items))
		copy(copied, items)
		result[k] = copied
	}

	return result
}

// Keys returns all unique keys in the index.
func (v *IndexedView[T, K]) Keys() []K {
	snap := v.data.Load().index
	keys := make([]K, 0, len(snap))

	for k := range snap {
		keys = append(keys, k)
	}

	return keys
}

// Count returns the number of unique keys.
func (v *IndexedView[T, K]) Count() int {
	return len(v.data.Load().index)
}

// CountFor returns the number of items for a specific key.
func (v *IndexedView[T, K]) CountFor(key K) int {
	return len(v.data.Load().index[key])
}

// Has reports whether the key exists in the index.
func (v *IndexedView[T, K]) Has(key K) bool {
	_, ok := v.data.Load().index[key]
	return ok
}

// OnChange registers a callback fired after each regroup; the unregister is idempotent.
func (v *IndexedView[T, K]) OnChange(fn func(old, new map[K][]T)) func() {
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

func (v *IndexedView[T, K]) recompute(items []T, version Version) {
	// Count first so each group is allocated at its exact size; len/4 just seeds the counter.
	keyCounts := make(map[K]int, len(items)/4+1)
	for i := range items {
		keyCounts[v.keyFn(items[i])]++
	}

	index := make(map[K][]T, len(keyCounts))
	for k, count := range keyCounts {
		index[k] = make([]T, 0, count)
	}

	for i := range items {
		key := v.keyFn(items[i])
		index[key] = append(index[key], items[i])
	}

	old := v.data.Swap(&indexSnapshot[K, T]{
		index:   index,
		version: version,
	})

	// Async so a slow store never delays the swap chain.
	if v.persistence != nil {
		select {
		case v.persistSem <- struct{}{}:
			go func() {
				defer func() { <-v.persistSem }()
				v.saveToPersistence(index)
			}()
		default:
			// Semaphore full — skip this save.
		}
	}

	v.mu.RLock()
	hooks := v.hooks
	v.mu.RUnlock()

	// Copies, shared between hooks: they isolate the published snapshot, not each other.
	oldCopy := copyIndex(old.index)
	newCopy := copyIndex(index)

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

func copyIndex[K comparable, V any](m map[K][]V) map[K][]V {
	result := make(map[K][]V, len(m))
	for k, items := range m {
		copied := make([]V, len(items))
		copy(copied, items)
		result[k] = copied
	}
	return result
}

func (v *IndexedView[T, K]) persistCtx() (context.Context, context.CancelFunc) {
	timeout := v.persistenceTimeout
	if timeout == 0 {
		timeout = defaultPersistenceTimeout
	}

	return context.WithTimeout(context.Background(), timeout)
}

func (v *IndexedView[T, K]) saveToPersistence(index map[K][]T) {
	data, err := json.Marshal(index)
	if err != nil {
		if v.onError != nil {
			v.onError(v.name, fmt.Errorf("marshal index: %w", err))
		}
		return
	}

	ctx, cancel := v.persistCtx()
	defer cancel()

	if err := v.persistence.Save(ctx, v.name, data); err != nil {
		if v.onError != nil {
			v.onError(v.name, fmt.Errorf("save index: %w", err))
		}
	}
}

// loadFromPersistence installs a stored index and reports whether it did.
func (v *IndexedView[T, K]) loadFromPersistence() bool {
	if v.persistence == nil {
		return false
	}

	ctx, cancel := v.persistCtx()
	defer cancel()

	data, err := v.persistence.Load(ctx, v.name)
	if err != nil || len(data) == 0 {
		return false
	}

	var index map[K][]T
	if err := json.Unmarshal(data, &index); err != nil || index == nil {
		return false
	}

	v.data.Store(&indexSnapshot[K, T]{index: index})

	return true
}

type indexSnapshotT[K comparable, V any] struct {
	index   map[K][]V
	version Version
}

// IndexedViewTOption configures an IndexedViewT.
type IndexedViewTOption[T any, K comparable, V any] func(*IndexedViewT[T, K, V])

// WithIndexTPersistence enables external persistence for the transformed indexed view.
func WithIndexTPersistence[T any, K comparable, V any](p ViewPersistence) IndexedViewTOption[T, K, V] {
	return func(v *IndexedViewT[T, K, V]) {
		v.persistence = p
	}
}

// WithIndexTErrorHandler sets an error callback for persistence failures; IndexedViewT has
// no hooks, so no hook panics reach it.
func WithIndexTErrorHandler[T any, K comparable, V any](fn ErrorFunc) IndexedViewTOption[T, K, V] {
	return func(v *IndexedViewT[T, K, V]) {
		v.onError = fn
	}
}

// IndexedViewT is an auto-updating map[K][]V grouping of a Collection, extracting a key and
// a value slice from each item. It has no OnChange — subscribe to the source instead.
type IndexedViewT[T any, K comparable, V any] struct {
	name               string
	source             *Collection[T]
	keyFn              func(T) K
	valueFn            func(T) []V
	persistence        ViewPersistence
	persistenceTimeout time.Duration
	onError            ErrorFunc
	persistSem         chan struct{}
	unsub              func()
	closeOnce          sync.Once

	data atomic.Pointer[indexSnapshotT[K, V]]
}

// NewIndexedViewT creates a grouped, transformed view: keyFn extracts the grouping key and
// valueFn the values under it, concatenated across all source items sharing that key.
func NewIndexedViewT[T any, K comparable, V any](name string, source *Collection[T], keyFn func(T) K, valueFn func(T) []V, opts ...IndexedViewTOption[T, K, V]) *IndexedViewT[T, K, V] {
	v := &IndexedViewT[T, K, V]{
		name:    name,
		source:  source,
		keyFn:   keyFn,
		valueFn: valueFn,
	}

	for _, opt := range opts {
		opt(v)
	}

	if v.persistence != nil {
		v.persistSem = make(chan struct{}, defaultPersistenceMaxConcurrency)
	}

	v.data.Store(&indexSnapshotT[K, V]{index: make(map[K][]V)})

	if !keepWarmStart(v.loadFromPersistence(), source.Version()) {
		v.recompute(source.All(), source.Version())
	}

	v.unsub = source.OnChange(func(_, newItems []T) {
		v.recompute(newItems, source.Version())
	})

	return v
}

// Name returns the view name.
func (v *IndexedViewT[T, K, V]) Name() string {
	return v.name
}

// Close unsubscribes the view from its source; idempotent. Reads stay valid but go stale.
func (v *IndexedViewT[T, K, V]) Close() {
	v.closeOnce.Do(func() {
		if v.unsub != nil {
			v.unsub()
		}
	})
}

// Get returns a copy of the values for the given key, or nil if the key doesn't exist.
func (v *IndexedViewT[T, K, V]) Get(key K) []V {
	items := v.data.Load().index[key]
	if items == nil {
		return nil
	}

	result := make([]V, len(items))
	copy(result, items)

	return result
}

// All returns the full index as a map copy.
func (v *IndexedViewT[T, K, V]) All() map[K][]V {
	snap := v.data.Load().index
	result := make(map[K][]V, len(snap))

	for k, items := range snap {
		copied := make([]V, len(items))
		copy(copied, items)
		result[k] = copied
	}

	return result
}

// Keys returns all unique keys.
func (v *IndexedViewT[T, K, V]) Keys() []K {
	snap := v.data.Load().index
	keys := make([]K, 0, len(snap))

	for k := range snap {
		keys = append(keys, k)
	}

	return keys
}

// Count returns the number of unique keys.
func (v *IndexedViewT[T, K, V]) Count() int {
	return len(v.data.Load().index)
}

// CountFor returns the number of values for a specific key.
func (v *IndexedViewT[T, K, V]) CountFor(key K) int {
	return len(v.data.Load().index[key])
}

// Has reports whether the key exists in the index.
func (v *IndexedViewT[T, K, V]) Has(key K) bool {
	_, ok := v.data.Load().index[key]
	return ok
}

func (v *IndexedViewT[T, K, V]) recompute(items []T, version Version) {
	// Count first so each group is allocated at its exact size; len/4 just seeds the counter.
	keyCounts := make(map[K]int, len(items)/4+1)
	for i := range items {
		key := v.keyFn(items[i])
		keyCounts[key] += len(v.valueFn(items[i]))
	}

	index := make(map[K][]V, len(keyCounts))
	for k, count := range keyCounts {
		index[k] = make([]V, 0, count)
	}

	for i := range items {
		key := v.keyFn(items[i])
		values := v.valueFn(items[i])
		index[key] = append(index[key], values...)
	}

	v.data.Store(&indexSnapshotT[K, V]{
		index:   index,
		version: version,
	})

	if v.persistence != nil {
		select {
		case v.persistSem <- struct{}{}:
			go func() {
				defer func() { <-v.persistSem }()
				v.saveToPersistence(index)
			}()
		default:
			// Semaphore full — skip this save.
		}
	}
}

func (v *IndexedViewT[T, K, V]) persistCtx() (context.Context, context.CancelFunc) {
	timeout := v.persistenceTimeout
	if timeout == 0 {
		timeout = defaultPersistenceTimeout
	}

	return context.WithTimeout(context.Background(), timeout)
}

func (v *IndexedViewT[T, K, V]) saveToPersistence(index map[K][]V) {
	data, err := json.Marshal(index)
	if err != nil {
		if v.onError != nil {
			v.onError(v.name, fmt.Errorf("marshal index: %w", err))
		}
		return
	}

	ctx, cancel := v.persistCtx()
	defer cancel()

	if err := v.persistence.Save(ctx, v.name, data); err != nil {
		if v.onError != nil {
			v.onError(v.name, fmt.Errorf("save index: %w", err))
		}
	}
}

// loadFromPersistence installs a stored index and reports whether it did.
func (v *IndexedViewT[T, K, V]) loadFromPersistence() bool {
	if v.persistence == nil {
		return false
	}

	ctx, cancel := v.persistCtx()
	defer cancel()

	data, err := v.persistence.Load(ctx, v.name)
	if err != nil || len(data) == 0 {
		return false
	}

	var index map[K][]V
	if err := json.Unmarshal(data, &index); err != nil || index == nil {
		return false
	}

	v.data.Store(&indexSnapshotT[K, V]{index: index})

	return true
}
