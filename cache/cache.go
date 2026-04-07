package cache

import (
	"context"
	"errors"
)

// Sentinel errors for cache operations.
var (
	ErrCacheMiss = errors.New("cache: miss")
	ErrClosed    = errors.New("cache: closed")
)

// Strategy defines when and how the cache is read/written during config sync.
type Strategy int

const (
	// ReadThrough checks the cache before hitting Directus.
	// On a cache miss, the data is fetched from Directus and then stored in cache.
	// Useful for fast cold starts and Directus unavailability.
	ReadThrough Strategy = iota

	// WriteThrough writes to the cache synchronously after every successful
	// Directus fetch, before the sync is considered complete.
	// Guarantees cache consistency at the cost of slightly slower syncs.
	WriteThrough

	// WriteBehind writes to the cache asynchronously after a successful
	// Directus fetch. The sync completes without waiting for the cache write.
	// Better sync latency but cache may be briefly stale on failures.
	WriteBehind

	// ReadWriteThrough combines ReadThrough and WriteThrough.
	// Reads from cache on cold start, writes to cache on every sync.
	// Most common choice for production use.
	ReadWriteThrough
)

// String returns a human-readable name for the strategy.
func (s Strategy) String() string {
	switch s {
	case ReadThrough:
		return "read-through"
	case WriteThrough:
		return "write-through"
	case WriteBehind:
		return "write-behind"
	case ReadWriteThrough:
		return "read-write-through"
	default:
		return "unknown"
	}
}

// ReadsFromCache reports whether the strategy uses the cache for reads.
func (s Strategy) ReadsFromCache() bool {
	return s == ReadThrough || s == ReadWriteThrough
}

// WritesToCache reports whether the strategy writes to the cache.
func (s Strategy) WritesToCache() bool {
	return s == WriteThrough || s == WriteBehind || s == ReadWriteThrough
}

// IsAsync reports whether cache writes are asynchronous.
func (s Strategy) IsAsync() bool {
	return s == WriteBehind
}

// Entry represents a cached config snapshot.
type Entry struct {
	Collection string
	Version    string
	Content    []byte
}

// Cache defines the interface for an optional caching layer.
//
// The manager uses Cache to speed up cold starts (read from cache instead of
// waiting for Directus) and to survive brief Directus outages.
type Cache interface {
	// Get retrieves a cached snapshot for the given collection.
	// Returns ErrCacheMiss if not found or expired.
	Get(ctx context.Context, collection string) (*Entry, error)

	// Set stores a snapshot in the cache. Implementations may apply a TTL.
	Set(ctx context.Context, entry Entry) error

	// Delete removes a cached snapshot for the given collection.
	Delete(ctx context.Context, collection string) error

	// Close releases cache resources.
	Close() error
}
