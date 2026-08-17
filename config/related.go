package config

import (
	"sync"
	"sync/atomic"
)

type relatedSnapshot[R any] struct {
	items   []R
	version Version
}

// RelatedView flattens nested related items (M2M, O2M) out of a parent Collection into one
// queryable view, re-extracting them on every source change.
type RelatedView[T any, R any] struct {
	name    string
	source  *Collection[T]
	extract func(T) []R
	dedup   func(R, R) bool // optional: returns true if two items are the same
	onError   ErrorFunc
	unsub     func()
	closeOnce sync.Once

	data atomic.Pointer[relatedSnapshot[R]]

	mu    sync.RWMutex
	hooks []func(old, new []R)
}

// RelatedViewOption configures a RelatedView.
type RelatedViewOption[T any, R any] func(*RelatedView[T, R])

// WithDedup sets a same-entity predicate so an M2M item under several parents is kept once.
// Dedup is a linear scan per item — O(n²); at scale dedup inside extract or use an IndexedView.
func WithDedup[T any, R any](fn func(a, b R) bool) RelatedViewOption[T, R] {
	return func(v *RelatedView[T, R]) {
		v.dedup = fn
	}
}

// WithRelatedViewErrorHandler sets an error callback for hook panics, which are otherwise
// recovered and silently dropped.
func WithRelatedViewErrorHandler[T any, R any](fn ErrorFunc) RelatedViewOption[T, R] {
	return func(v *RelatedView[T, R]) {
		v.onError = fn
	}
}

// NewRelatedView creates an auto-updating view calling extract on each parent and
// flattening the results into one slice. See WithDedup for items shared between parents.
func NewRelatedView[T any, R any](name string, source *Collection[T], extract func(T) []R, opts ...RelatedViewOption[T, R]) *RelatedView[T, R] {
	v := &RelatedView[T, R]{
		name:    name,
		source:  source,
		extract: extract,
	}

	for _, opt := range opts {
		opt(v)
	}

	v.data.Store(&relatedSnapshot[R]{})

	v.recompute(source.All(), source.Version())

	v.unsub = source.OnChange(func(_, newItems []T) {
		v.recompute(newItems, source.Version())
	})

	return v
}

// Name returns the view name.
func (v *RelatedView[T, R]) Name() string {
	return v.name
}

// Version returns the current snapshot version.
func (v *RelatedView[T, R]) Version() Version {
	return v.data.Load().version
}

// Close unsubscribes the view from its source; idempotent. Reads stay valid but go stale.
func (v *RelatedView[T, R]) Close() {
	v.closeOnce.Do(func() {
		if v.unsub != nil {
			v.unsub()
		}
	})
}

// All returns a copy of all flattened related items.
func (v *RelatedView[T, R]) All() []R {
	items := v.data.Load().items
	result := make([]R, len(items))
	copy(result, items)

	return result
}

// Count returns the number of flattened related items.
func (v *RelatedView[T, R]) Count() int {
	return len(v.data.Load().items)
}

// First returns the first related item, or false if empty.
func (v *RelatedView[T, R]) First() (R, bool) {
	items := v.data.Load().items
	if len(items) == 0 {
		var zero R
		return zero, false
	}

	return items[0], true
}

// Find returns the first related item matching the predicate.
func (v *RelatedView[T, R]) Find(pred func(R) bool) (R, bool) {
	for _, item := range v.data.Load().items {
		if pred(item) {
			return item, true
		}
	}

	var zero R
	return zero, false
}

// FindMany returns all related items matching the predicate.
func (v *RelatedView[T, R]) FindMany(pred func(R) bool) []R {
	var result []R
	for _, item := range v.data.Load().items {
		if pred(item) {
			result = append(result, item)
		}
	}

	return result
}

// Filter applies filter options to the flattened related items.
func (v *RelatedView[T, R]) Filter(opts ...FilterOption[R]) []R {
	return applyFilters(v.data.Load().items, opts)
}

// OnChange registers a callback fired after each recompute; the unregister is idempotent.
func (v *RelatedView[T, R]) OnChange(fn func(old, new []R)) func() {
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

func (v *RelatedView[T, R]) recompute(parents []T, version Version) {
	var items []R
	for _, parent := range parents {
		related := v.extract(parent)
		if v.dedup == nil {
			items = append(items, related...)
			continue
		}

		for _, r := range related {
			if !v.contains(items, r) {
				items = append(items, r)
			}
		}
	}

	old := v.data.Load()
	v.data.Store(&relatedSnapshot[R]{
		items:   items,
		version: version,
	})

	v.mu.RLock()
	hooks := v.hooks
	v.mu.RUnlock()

	// Copies, shared between hooks: they isolate the published snapshot, not each other.
	oldCopy := make([]R, len(old.items))
	copy(oldCopy, old.items)
	newCopy := make([]R, len(items))
	copy(newCopy, items)

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

func (v *RelatedView[T, R]) contains(items []R, candidate R) bool {
	for _, item := range items {
		if v.dedup(item, candidate) {
			return true
		}
	}

	return false
}
