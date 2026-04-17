package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// indexSnapshot holds a precomputed index.
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

// WithIndexErrorHandler sets an error callback for persistence failures.
func WithIndexErrorHandler[T any, K comparable](fn ErrorFunc) IndexedViewOption[T, K] {
	return func(v *IndexedView[T, K]) {
		v.onError = fn
	}
}

// IndexedView is an auto-updating view that groups collection items by a key.
// It maintains a map[K][]V that recomputes when the source collection changes.
//
// Performance characteristics:
//   - Reads (Get, Keys, Count): O(1) map lookup, lock-free via atomic.Pointer
//   - Recompute: O(n) per source change — runs once per sync, not per read
//   - Memory: one map[K][]T snapshot + the source snapshot
//   - Persistence: async write after recompute (does not block reads)
//
// Example — group articles by category:
//
//	byCategory := config.NewIndexedView("by-category", articles,
//	    func(a Article) string { return a.Category },
//	)
//
//	techArticles := byCategory.Get("tech")     // []Article
//	allCategories := byCategory.Keys()         // []string
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

// NewIndexedView creates an auto-updating grouped view of a Collection.
//
// keyFn extracts the grouping key from each item.
// The view maintains a map[K][]T that recomputes on every source change.
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

	// Try loading from persistence for warm start.
	if v.persistence != nil {
		v.loadFromPersistence()
	}

	// Compute from current source data.
	v.recompute(source.All(), source.Version())

	// Auto-update on source changes.
	v.unsub = source.OnChange(func(_, newItems []T) {
		v.recompute(newItems, source.Version())
	})

	return v
}

// Name returns the view name.
func (v *IndexedView[T, K]) Name() string {
	return v.name
}

// Close unsubscribes the view from its source collection. After Close,
// the view stops recomputing on source changes. It is safe to call
// Close multiple times.
func (v *IndexedView[T, K]) Close() {
	v.closeOnce.Do(func() {
		if v.unsub != nil {
			v.unsub()
		}
	})
}

// Get returns all items for the given key, or nil if the key doesn't exist.
// O(1) map lookup + slice copy.
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

// OnChange registers a callback that fires after the index recomputes.
// Returns a function that removes the hook when called.
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
	// Pre-scan to estimate group sizes for better allocation.
	keyCounts := make(map[K]int, len(items)/4+1)
	for i := range items {
		keyCounts[v.keyFn(items[i])]++
	}

	// Build index with pre-allocated slices.
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

	// Persist asynchronously with bounded concurrency.
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

	for _, fn := range hooks {
		if fn != nil {
			fn(old.index, index)
		}
	}
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

func (v *IndexedView[T, K]) loadFromPersistence() {
	ctx, cancel := v.persistCtx()
	defer cancel()

	data, err := v.persistence.Load(ctx, v.name)
	if err != nil || len(data) == 0 {
		return
	}

	var index map[K][]T
	if err := json.Unmarshal(data, &index); err != nil {
		return
	}

	v.data.Store(&indexSnapshot[K, T]{index: index})
}

// indexSnapshotT holds a transformed index.
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

// WithIndexTErrorHandler sets an error callback for persistence failures.
func WithIndexTErrorHandler[T any, K comparable, V any](fn ErrorFunc) IndexedViewTOption[T, K, V] {
	return func(v *IndexedViewT[T, K, V]) {
		v.onError = fn
	}
}

// IndexedViewT is an auto-updating view that groups and transforms collection items.
// It extracts a key and a value slice from each item, producing map[K][]V.
//
// Performance characteristics:
//   - Reads: O(1) map lookup, lock-free
//   - Recompute: O(n * m) where n=items, m=avg values per item — runs once per sync
//   - Memory: one map[K][]V snapshot
//   - Persistence: async write after recompute
//
// Example — map article names to their tags:
//
//	tagsByArticle := config.NewIndexedViewT("tags-by-article", articles,
//	    func(a Article) string { return a.Name },
//	    func(a Article) []Tag { return a.Tags },
//	)
//
//	tutorialTags := tagsByArticle.Get("Go Tutorial") // []Tag
//	count := tagsByArticle.CountFor("Rust Guide")
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

// NewIndexedViewT creates a grouped, transformed view.
//
// keyFn extracts the grouping key from each source item.
// valueFn extracts the values to collect under that key.
// Values from all source items with the same key are concatenated.
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

	if v.persistence != nil {
		v.loadFromPersistence()
	}

	v.recompute(source.All(), source.Version())

	v.unsub = source.OnChange(func(_, newItems []T) {
		v.recompute(newItems, source.Version())
	})

	return v
}

// Name returns the view name.
func (v *IndexedViewT[T, K, V]) Name() string {
	return v.name
}

// Close unsubscribes the view from its source collection. After Close,
// the view stops recomputing on source changes. It is safe to call
// Close multiple times.
func (v *IndexedViewT[T, K, V]) Close() {
	v.closeOnce.Do(func() {
		if v.unsub != nil {
			v.unsub()
		}
	})
}

// Get returns the values for the given key. O(1) lookup + slice copy.
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
	// First pass: count total values per key for pre-allocation.
	keyCounts := make(map[K]int, len(items)/4+1)
	for i := range items {
		key := v.keyFn(items[i])
		keyCounts[key] += len(v.valueFn(items[i]))
	}

	// Second pass: build index with pre-allocated slices.
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

func (v *IndexedViewT[T, K, V]) loadFromPersistence() {
	ctx, cancel := v.persistCtx()
	defer cancel()

	data, err := v.persistence.Load(ctx, v.name)
	if err != nil || len(data) == 0 {
		return
	}

	var index map[K][]V
	if err := json.Unmarshal(data, &index); err != nil {
		return
	}

	v.data.Store(&indexSnapshotT[K, V]{index: index})
}
