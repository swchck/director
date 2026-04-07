// Package config provides in-memory data stores with lock-free reads and
// auto-updating materialized views.
//
// Core types:
//   - Collection[T] holds a versioned slice of items with atomic swap
//   - Singleton[T] holds a single versioned value
//   - View[T] is a filtered/sorted/limited projection of a Collection
//   - IndexedView[T, K] groups items by key for O(1) lookup
//   - RelatedView[T, R] flattens nested M2M/O2M relations
//   - TranslatedView[T, R] transforms items (e.g. flatten translations)
//   - CompositeView[T] merges multiple views into one
//   - SingletonView[T, R] derives a value from a Singleton
//
// All views recompute synchronously when their source changes via OnChange hooks.
package config
