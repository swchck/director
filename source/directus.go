package source

import (
	"context"
	"time"

	"github.com/swchck/director/directus"
)

// DirectusCollection adapts directus.Items[T] to the CollectionSource interface.
type DirectusCollection[T any] struct {
	items *directus.Items[T]
	opts  []directus.QueryOption
}

// FromDirectus wraps a directus.Items[T] with optional query options as a CollectionSource.
//
// Example:
//
//	src := source.FromDirectus(directus.NewItems[Product](dc, "products"),
//	    directus.WithFields("*", "translations.*"),
//	)
//	manager.RegisterCollection(mgr, products, src)
func FromDirectus[T any](items *directus.Items[T], opts ...directus.QueryOption) CollectionSource[T] {
	return &DirectusCollection[T]{items: items, opts: opts}
}

func (d *DirectusCollection[T]) List(ctx context.Context) ([]T, error) {
	return d.items.List(ctx, d.opts...)
}

func (d *DirectusCollection[T]) LastModified(ctx context.Context) (time.Time, error) {
	return d.items.MaxDateUpdated(ctx)
}

// DirectusSingleton adapts directus.Singleton[T] to the SingletonSource interface.
type DirectusSingleton[T any] struct {
	singleton *directus.Singleton[T]
	opts      []directus.QueryOption
}

// FromDirectusSingleton wraps a directus.Singleton[T] as a SingletonSource.
//
// Example:
//
//	src := source.FromDirectusSingleton(directus.NewSingleton[Settings](dc, "settings"))
//	manager.RegisterSingleton(mgr, settings, src)
func FromDirectusSingleton[T any](s *directus.Singleton[T], opts ...directus.QueryOption) SingletonSource[T] {
	return &DirectusSingleton[T]{singleton: s, opts: opts}
}

func (d *DirectusSingleton[T]) Get(ctx context.Context) (*T, error) {
	return d.singleton.Get(ctx, d.opts...)
}

func (d *DirectusSingleton[T]) LastModified(ctx context.Context) (time.Time, error) {
	return d.singleton.DateUpdated(ctx)
}
