package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/source"
)

// registrable is a type-erased interface that both collection and singleton
// registrations satisfy. It allows the manager to work with any config type
// without knowing the concrete generic parameter.
type registrable interface {
	// name returns the collection name.
	name() string

	// version returns the current in-memory config version.
	version() config.Version

	// fetchVersion fetches the latest modification timestamp for change detection.
	fetchVersion(ctx context.Context) (time.Time, error)

	// fetchAndSwap fetches all data from the source, swaps the in-memory config,
	// and returns the serialized content for storage/caching.
	fetchAndSwap(ctx context.Context, ver config.Version) ([]byte, error)

	// swapFromBytes deserializes content and swaps the in-memory config.
	// Used by followers loading from storage or cache.
	swapFromBytes(ver config.Version, data []byte) error
}

type collectionReg[T any] struct {
	cfg    *config.Collection[T]
	src    source.CollectionSource[T]
	logger dlog.Logger
}

func (r *collectionReg[T]) name() string {
	return r.cfg.Name()
}

func (r *collectionReg[T]) version() config.Version {
	return r.cfg.Version()
}

func (r *collectionReg[T]) fetchVersion(ctx context.Context) (time.Time, error) {
	return r.src.LastModified(ctx)
}

func (r *collectionReg[T]) fetchAndSwap(ctx context.Context, ver config.Version) ([]byte, error) {
	items, err := r.src.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("manager: fetch %s: %w", r.cfg.Name(), err)
	}

	oldCount := r.cfg.Count()

	data, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("manager: marshal %s: %w", r.cfg.Name(), err)
	}

	if err := r.cfg.Swap(ver, items); err != nil {
		return data, fmt.Errorf("manager: swap %s: %w", r.cfg.Name(), err)
	}

	r.logger.Debug("manager: collection swapped",
		dlog.String("collection", r.cfg.Name()),
		dlog.Int("old_count", oldCount),
		dlog.Int("new_count", len(items)),
		dlog.String("version", ver.String()),
	)

	return data, nil
}

func (r *collectionReg[T]) swapFromBytes(ver config.Version, data []byte) error {
	var items []T
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("manager: unmarshal %s: %w", r.cfg.Name(), err)
	}

	oldCount := r.cfg.Count()

	if err := r.cfg.Swap(ver, items); err != nil {
		return fmt.Errorf("manager: swap %s from bytes: %w", r.cfg.Name(), err)
	}

	r.logger.Debug("manager: collection swapped from snapshot",
		dlog.String("collection", r.cfg.Name()),
		dlog.Int("old_count", oldCount),
		dlog.Int("new_count", len(items)),
		dlog.String("version", ver.String()),
	)

	return nil
}

type singletonReg[T any] struct {
	cfg    *config.Singleton[T]
	src    source.SingletonSource[T]
	logger dlog.Logger
}

func (r *singletonReg[T]) name() string {
	return r.cfg.Name()
}

func (r *singletonReg[T]) version() config.Version {
	return r.cfg.Version()
}

func (r *singletonReg[T]) fetchVersion(ctx context.Context) (time.Time, error) {
	return r.src.LastModified(ctx)
}

func (r *singletonReg[T]) fetchAndSwap(ctx context.Context, ver config.Version) ([]byte, error) {
	item, err := r.src.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("manager: fetch singleton %s: %w", r.cfg.Name(), err)
	}

	data, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("manager: marshal singleton %s: %w", r.cfg.Name(), err)
	}

	if err := r.cfg.Swap(ver, *item); err != nil {
		return data, fmt.Errorf("manager: swap singleton %s: %w", r.cfg.Name(), err)
	}

	r.logger.Debug("manager: singleton swapped",
		dlog.String("singleton", r.cfg.Name()),
		dlog.String("version", ver.String()),
	)

	return data, nil
}

func (r *singletonReg[T]) swapFromBytes(ver config.Version, data []byte) error {
	var item T
	if err := json.Unmarshal(data, &item); err != nil {
		return fmt.Errorf("manager: unmarshal singleton %s: %w", r.cfg.Name(), err)
	}

	if err := r.cfg.Swap(ver, item); err != nil {
		return fmt.Errorf("manager: swap singleton %s from bytes: %w", r.cfg.Name(), err)
	}

	r.logger.Debug("manager: singleton swapped from snapshot",
		dlog.String("singleton", r.cfg.Name()),
		dlog.String("version", ver.String()),
	)

	return nil
}

// RegisterCollectionSource registers a collection with a generic data source.
// Use this when implementing a custom backend (not Directus).
//
// Example with a custom source:
//
//	manager.RegisterCollectionSource(mgr, products, &myCustomAPI{})
func RegisterCollectionSource[T any](m *Manager, cfg *config.Collection[T], src source.CollectionSource[T]) {
	m.register(&collectionReg[T]{
		cfg:    cfg,
		src:    src,
		logger: m.logger,
	})
}

// RegisterSingletonSource registers a singleton with a generic data source.
func RegisterSingletonSource[T any](m *Manager, cfg *config.Singleton[T], src source.SingletonSource[T]) {
	m.register(&singletonReg[T]{
		cfg:    cfg,
		src:    src,
		logger: m.logger,
	})
}

// RegisterCollection registers a collection sourced from Directus.
// This is a convenience wrapper that creates a source.CollectionSource from directus.Items[T].
//
// opts are Directus query options applied to every fetch (e.g. WithFields, WithDeep).
func RegisterCollection[T any](m *Manager, cfg *config.Collection[T], items *directus.Items[T], opts ...directus.QueryOption) {
	RegisterCollectionSource(m, cfg, source.FromDirectus(items, opts...))
}

// RegisterSingleton registers a singleton sourced from Directus.
// This is a convenience wrapper that creates a source.SingletonSource from directus.Singleton[T].
func RegisterSingleton[T any](m *Manager, cfg *config.Singleton[T], singleton *directus.Singleton[T], opts ...directus.QueryOption) {
	RegisterSingletonSource(m, cfg, source.FromDirectusSingleton(singleton, opts...))
}
