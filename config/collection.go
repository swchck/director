package config

import (
	"sync"
	"sync/atomic"
)

// collectionSnapshot holds an immutable point-in-time view of a collection's items.
type collectionSnapshot[T any] struct {
	version Version
	items   []T
}

// Collection is a thread-safe, queryable in-memory store for a Directus collection
// that contains multiple items.
//
// Read methods (All, First, Find, FindMany, Filter) are lock-free and safe for
// concurrent use. User-provided predicate and comparison functions passed to
// Find, FindMany, and Filter must not panic — if they do, the panic will
// propagate to the caller. Ensure predicate functions handle all edge cases.
type Collection[T any] struct {
	name string
	data atomic.Pointer[collectionSnapshot[T]]

	mu    sync.RWMutex
	hooks []func(old, new []T)
}

// NewCollection creates a new Collection config for the named Directus collection.
func NewCollection[T any](name string) *Collection[T] {
	c := &Collection[T]{name: name}
	c.data.Store(&collectionSnapshot[T]{})

	return c
}

// Name returns the Directus collection name.
func (c *Collection[T]) Name() string {
	return c.name
}

// Version returns the current version.
func (c *Collection[T]) Version() Version {
	return c.data.Load().version
}

// All returns a copy of all items.
func (c *Collection[T]) All() []T {
	items := c.data.Load().items
	result := make([]T, len(items))
	copy(result, items)

	return result
}

// Count returns the number of items.
func (c *Collection[T]) Count() int {
	return len(c.data.Load().items)
}

// First returns the first item, or false if empty.
func (c *Collection[T]) First() (T, bool) {
	items := c.data.Load().items
	if len(items) == 0 {
		var zero T
		return zero, false
	}

	return items[0], true
}

// Find returns the first item matching the predicate, or false if none match.
//
// The predicate must not panic. If it does, the panic propagates to the caller.
func (c *Collection[T]) Find(predicate func(T) bool) (T, bool) {
	for _, item := range c.data.Load().items {
		if predicate(item) {
			return item, true
		}
	}

	var zero T
	return zero, false
}

// FindMany returns all items matching the predicate.
//
// The predicate must not panic. If it does, the panic propagates to the caller.
func (c *Collection[T]) FindMany(predicate func(T) bool) []T {
	var result []T
	for _, item := range c.data.Load().items {
		if predicate(item) {
			result = append(result, item)
		}
	}

	return result
}

// Filter applies a chain of filter options (Where, SortBy, Limit, Offset) and returns the result.
//
// User-provided functions in filter options must not panic.
// If they do, the panic propagates to the caller.
func (c *Collection[T]) Filter(opts ...FilterOption[T]) []T {
	return applyFilters(c.data.Load().items, opts)
}

// OnChange registers a callback that fires after items are swapped.
// The callback receives copies of the old and new item slices.
// Returns a function that removes the hook when called.
//
// If a callback panics during Swap, the panic is recovered and returned as an error.
// The data swap itself is already committed before hooks run.
func (c *Collection[T]) OnChange(fn func(old, new []T)) func() {
	c.mu.Lock()
	defer c.mu.Unlock()

	idx := len(c.hooks)
	c.hooks = append(c.hooks, fn)

	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		if idx < len(c.hooks) {
			c.hooks[idx] = nil
		}
	}
}

// Swap replaces the current items with a new version and fires OnChange hooks.
// This method is intended for use by the manager package.
//
// The atomic swap always succeeds. If an OnChange hook panics, the panic is
// recovered and returned as an error. The data will already reflect the new version.
func (c *Collection[T]) Swap(version Version, items []T) error {
	stored := make([]T, len(items))
	copy(stored, items)

	old := c.data.Swap(&collectionSnapshot[T]{
		version: version,
		items:   stored,
	})

	c.mu.RLock()
	hooks := c.hooks
	c.mu.RUnlock()

	wrappers := make([]func(), 0, len(hooks))
	for _, fn := range hooks {
		if fn == nil {
			continue
		}

		fn := fn
		wrappers = append(wrappers, func() { fn(old.items, stored) })
	}

	return safeCallHooks(wrappers...)
}
