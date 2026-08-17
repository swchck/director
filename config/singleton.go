package config

import (
	"sync"
	"sync/atomic"
)

type singletonSnapshot[T any] struct {
	version Version
	value   *T
}

// Singleton is a thread-safe in-memory store holding exactly one value of type T.
// Reads are lock-free.
type Singleton[T any] struct {
	name string
	data atomic.Pointer[singletonSnapshot[T]]

	mu    sync.RWMutex
	hooks []func(old, new *T)
}

// NewSingleton creates an unloaded Singleton under the given source collection name.
func NewSingleton[T any](name string) *Singleton[T] {
	s := &Singleton[T]{name: name}
	s.data.Store(&singletonSnapshot[T]{})

	return s
}

// Name returns the source collection name.
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

// OnChange registers a callback fired after each Swap (old is nil on the first load); a
// panic in it becomes a Swap error. The returned unregister is idempotent.
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

// Swap sets a new version and value, then fires OnChange hooks. As with Collection.Swap it
// publishes the snapshot first, so an error means a hook panicked, never a rejected swap.
func (s *Singleton[T]) Swap(version Version, value T) error {
	v := value // own the value, so the snapshot pointer cannot alias the caller's copy
	old := s.data.Swap(&singletonSnapshot[T]{
		version: version,
		value:   &v,
	})

	s.mu.RLock()
	hooks := s.hooks
	s.mu.RUnlock()

	// Copies, shared between hooks: they isolate the published snapshot, not each other.
	var oldPtr *T
	if old.value != nil {
		oldCopy := *old.value
		oldPtr = &oldCopy
	}
	newCopy := v

	wrappers := make([]func(), 0, len(hooks))
	for _, fn := range hooks {
		if fn == nil {
			continue
		}

		fn := fn
		wrappers = append(wrappers, func() { fn(oldPtr, &newCopy) })
	}

	return safeCallHooks(wrappers...)
}
