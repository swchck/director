// Package cache defines the caching interface and strategies for the director library.
//
// Implementations live in sub-packages:
//   - cache/redis — Redis-backed cache and view persistence
//   - cache/memory — in-memory view persistence for testing and single-replica use
package cache
