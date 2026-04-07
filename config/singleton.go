package config

import (
	"sync"
	"sync/atomic"
)

// singletonSnapshot holds an immutable point-in-time view of a singleton value.
type singletonSnapshot[T any] struct {
	version Version
	value   *T
}

// Singleton is a thread-safe in-memory store for a Directus singleton collection.
// It holds exactly one value of type T.
type Singleton[T any] struct {
	name string
	data atomic.Pointer[singletonSnapshot[T]]

	mu    sync.RWMutex
	hooks []func(old, new *T)
}

// NewSingleton creates a new Singleton config for the named Directus collection.
func NewSingleton[T any](name string) *Singleton[T] {
	s := &Singleton[T]{name: name}
	s.data.Store(&singletonSnapshot[T]{})

	return s
}

// Name returns the Directus collection name.
func (s *Singleton[T]) Name() string {
	return s.name
}

// Version returns the current version.
func (s *Singleton[T]) Version() Version {
	return s.data.Load().version
}

// Get returns the current value, or false if not yet loaded.
func (s *Singleton[T]) Get() (T, bool) {
	v := s.data.Load().value
	if v == nil {
		var zero T
		return zero, false
	}

	return *v, true
}

// OnChange registers a callback that fires after the value is swapped.
// old may be nil on the first load. Returns a function that removes the hook when called.
//
// If a callback panics during Swap, the panic is recovered and returned as an error.
// The data swap itself is already committed before hooks run.
func (s *Singleton[T]) OnChange(fn func(old, new *T)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := len(s.hooks)
	s.hooks = append(s.hooks, fn)

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		if idx < len(s.hooks) {
			s.hooks[idx] = nil
		}
	}
}

// Swap replaces the current value with a new version and fires OnChange hooks.
// This method is intended for use by the manager package.
//
// The atomic swap always succeeds. If an OnChange hook panics, the panic is
// recovered and returned as an error. The data will already reflect the new version.
func (s *Singleton[T]) Swap(version Version, value T) error {
	v := value // copy to own the value
	old := s.data.Swap(&singletonSnapshot[T]{
		version: version,
		value:   &v,
	})

	s.mu.RLock()
	hooks := s.hooks
	s.mu.RUnlock()

	wrappers := make([]func(), 0, len(hooks))
	for _, fn := range hooks {
		if fn == nil {
			continue
		}

		fn := fn
		wrappers = append(wrappers, func() { fn(old.value, &v) })
	}

	return safeCallHooks(wrappers...)
}
