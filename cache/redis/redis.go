// Package redis implements cache.Cache and config.ViewPersistence using Redis.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/swchck/director/cache"
)

const (
	defaultTTL       = 10 * time.Minute
	defaultKeyPrefix = "director:"

	defaultViewTTL       = 10 * time.Minute
	defaultViewKeyPrefix = "director:view:"
)

// Cache implements cache.Cache using Redis with configurable TTL.
type Cache struct {
	client    goredis.UniversalClient
	keyPrefix string
	ttl       time.Duration
}

// CacheOption configures a Cache.
type CacheOption func(*Cache)

// WithTTL sets the time-to-live for cached entries.
// Default is 10 minutes. Set to 0 for no expiration.
func WithTTL(ttl time.Duration) CacheOption {
	return func(c *Cache) {
		c.ttl = ttl
	}
}

// WithKeyPrefix sets the Redis key prefix.
// Default is "director:".
func WithKeyPrefix(prefix string) CacheOption {
	return func(c *Cache) {
		c.keyPrefix = prefix
	}
}

// NewCache creates a new Redis-backed Cache.
//
// Uses redis.UniversalClient for standalone, cluster, and sentinel compatibility.
func NewCache(client goredis.UniversalClient, opts ...CacheOption) *Cache {
	c := &Cache{
		client:    client,
		keyPrefix: defaultKeyPrefix,
		ttl:       defaultTTL,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// redisEntry is the JSON structure stored in Redis.
type redisEntry struct {
	Collection string `json:"collection"`
	Version    string `json:"version"`
	Content    []byte `json:"content"`
}

// Get retrieves a cached entry. Returns cache.ErrCacheMiss if not found or expired.
func (c *Cache) Get(ctx context.Context, collection string) (*cache.Entry, error) {
	key := c.key(collection)

	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil, cache.ErrCacheMiss
		}

		return nil, fmt.Errorf("cache/redis: get %s: %w", collection, err)
	}

	var re redisEntry
	if err := json.Unmarshal(data, &re); err != nil {
		return nil, fmt.Errorf("cache/redis: unmarshal cached %s: %w", collection, err)
	}

	return &cache.Entry{
		Collection: re.Collection,
		Version:    re.Version,
		Content:    re.Content,
	}, nil
}

// Set stores an entry in Redis with the configured TTL.
func (c *Cache) Set(ctx context.Context, entry cache.Entry) error {
	re := redisEntry{
		Collection: entry.Collection,
		Version:    entry.Version,
		Content:    entry.Content,
	}

	data, err := json.Marshal(re)
	if err != nil {
		return fmt.Errorf("cache/redis: marshal entry %s: %w", entry.Collection, err)
	}

	key := c.key(entry.Collection)

	if err := c.client.Set(ctx, key, data, c.ttl).Err(); err != nil {
		return fmt.Errorf("cache/redis: set %s: %w", entry.Collection, err)
	}

	return nil
}

// Delete removes a cached entry.
func (c *Cache) Delete(ctx context.Context, collection string) error {
	key := c.key(collection)

	if err := c.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("cache/redis: del %s: %w", collection, err)
	}

	return nil
}

// Close is a no-op — the caller owns the Redis client lifecycle.
func (c *Cache) Close() error {
	return nil
}

func (c *Cache) key(collection string) string {
	return c.keyPrefix + collection
}

// ViewStore implements config.ViewPersistence using Redis.
// It allows View results to be shared across replicas.
type ViewStore struct {
	client    goredis.UniversalClient
	keyPrefix string
	ttl       time.Duration
}

// ViewStoreOption configures a ViewStore.
type ViewStoreOption func(*ViewStore)

// WithViewTTL sets the time-to-live for cached view entries.
// Default is 10 minutes. Set to 0 for no expiration.
func WithViewTTL(ttl time.Duration) ViewStoreOption {
	return func(s *ViewStore) {
		s.ttl = ttl
	}
}

// WithViewKeyPrefix sets the Redis key prefix for view entries.
// Default is "director:view:".
func WithViewKeyPrefix(prefix string) ViewStoreOption {
	return func(s *ViewStore) {
		s.keyPrefix = prefix
	}
}

// NewViewStore creates a new Redis-backed ViewStore.
func NewViewStore(client goredis.UniversalClient, opts ...ViewStoreOption) *ViewStore {
	s := &ViewStore{
		client:    client,
		keyPrefix: defaultViewKeyPrefix,
		ttl:       defaultViewTTL,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Save stores view data in Redis with the configured TTL.
func (s *ViewStore) Save(ctx context.Context, key string, data []byte) error {
	redisKey := s.keyPrefix + key

	if err := s.client.Set(ctx, redisKey, data, s.ttl).Err(); err != nil {
		return fmt.Errorf("cache/redis: save view %s: %w", key, err)
	}

	return nil
}

// Load retrieves view data from Redis.
// Returns nil data and nil error if the key does not exist.
func (s *ViewStore) Load(ctx context.Context, key string) ([]byte, error) {
	redisKey := s.keyPrefix + key

	data, err := s.client.Get(ctx, redisKey).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil, nil
		}

		return nil, fmt.Errorf("cache/redis: load view %s: %w", key, err)
	}

	return data, nil
}
