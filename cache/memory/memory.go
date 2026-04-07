// Package memory implements config.ViewPersistence using an in-memory map.
//
// Useful for:
//   - Testing without Redis
//   - Single-replica deployments where you want warm-start behavior
//     (views survive recreation within the same process)
//   - Sharing precomputed views between multiple View instances in the same process
//
// Data does NOT survive process restarts — use cache/redis.ViewStore for that.
package memory

import (
	"context"
	"sync"
)

// ViewStore implements config.ViewPersistence using an in-memory map.
type ViewStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewViewStore creates a new in-memory view store.
func NewViewStore() *ViewStore {
	return &ViewStore{
		data: make(map[string][]byte),
	}
}

// Save stores view data in memory.
func (s *ViewStore) Save(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := make([]byte, len(data))
	copy(stored, data)

	s.data[key] = stored

	return nil
}

// Load retrieves view data from memory.
// Returns nil data and nil error if the key does not exist.
func (s *ViewStore) Load(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, ok := s.data[key]
	if !ok {
		return nil, nil
	}

	result := make([]byte, len(data))
	copy(result, data)

	return result, nil
}
