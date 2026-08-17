package config

import (
	"sync"
	"sync/atomic"
)

type collectionSnapshot[T any] struct {
	version Version
	items   []T
}

// Collection is a thread-safe, queryable in-memory store for a collection of items.
// Reads are lock-free; predicates passed to them run unrecovered on the caller's goroutine.
type Collection[T any] struct {
	name string
	data atomic.Pointer[collectionSnapshot[T]]

	mu    sync.RWMutex
	hooks []func(old, new []T)
}

// NewCollection creates an empty Collection under the given source collection name.
func NewCollection[T any](name string) *Collection[T] {
	c := &Collection[T]{name: name}
	c.data.Store(&collectionSnapshot[T]{})

	return c
}

// Name returns the source collection name.
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
func (c *Collection[T]) Filter(opts ...FilterOption[T]) []T {
	return applyFilters(c.data.Load().items, opts)
}

// OnChange registers a callback fired after each Swap with copies of the old and new
// slices; a panic in it becomes a Swap error. The returned unregister is idempotent.
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

// Swap sets a new version and items, then fires OnChange hooks. It publishes the snapshot
// before any hook runs, so an error means a hook panicked, never that the old data is live.
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

	return safeCallHooks(wrappers...)
}
