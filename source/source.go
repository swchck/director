// Package source defines the data source interfaces used by the manager.
//
// These interfaces decouple the sync engine from any specific CMS or API.
// The library ships with Directus implementations, but you can implement
// these interfaces for any data source: Strapi, Contentful, a custom API, etc.
//
// Usage with Directus (default):
//
//	items := directus.NewItems[Product](dc, "products")
//	manager.RegisterCollection(mgr, products, items)
//
// Usage with a custom source:
//
//	type MyAPI struct { ... }
//	func (a *MyAPI) List(ctx) ([]Product, error) { ... }
//	func (a *MyAPI) LastModified(ctx) (time.Time, error) { ... }
//	manager.RegisterCollection(mgr, products, &MyAPI{})
package source

import (
	"context"
	"time"
)

// CollectionSource provides data for a multi-item collection.
// Implement this interface to sync from any backend.
type CollectionSource[T any] interface {
	// List fetches all items from the source.
	// Called by the manager on every sync cycle.
	List(ctx context.Context) ([]T, error)

	// LastModified returns the most recent modification timestamp.
	// Used for lightweight change detection — if the timestamp hasn't changed
	// since the last sync, the full List() call is skipped.
	// Return time.Time{} (zero) if unknown — the manager will always do a full fetch.
	LastModified(ctx context.Context) (time.Time, error)
}

// SingletonSource provides data for a single-item collection.
// Implement this interface to sync singletons from any backend.
type SingletonSource[T any] interface {
	// Get fetches the singleton value from the source.
	Get(ctx context.Context) (*T, error)

	// LastModified returns the most recent modification timestamp.
	// Same semantics as CollectionSource.LastModified.
	LastModified(ctx context.Context) (time.Time, error)
}
