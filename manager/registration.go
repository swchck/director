package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/source"
)

// stagedRef is an opaque handle to a staged-but-not-committed config value.
// Produced by fetchAndStage / stageFromBytes, consumed by commitStaged.
type stagedRef interface {
	// roundID returns the sync round this staging belongs to.
	roundID() string
}

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
	// Used by the eventually-consistent sync protocol.
	fetchAndSwap(ctx context.Context, ver config.Version) ([]byte, error)

	// swapFromBytes deserializes content and swaps the in-memory config.
	// Used by followers loading from storage or cache.
	swapFromBytes(ver config.Version, data []byte) error

	// fetchAndStage fetches and deserializes data from the source, stores it
	// as a staged value under roundID, and returns the serialized bytes for
	// snapshot/cache. The staged value is NOT applied until commitStaged is
	// called. Used by the leader side of the 2PC protocol.
	fetchAndStage(ctx context.Context, ver config.Version, roundID string, ttl time.Duration) (content []byte, staged stagedRef, err error)

	// stageFromBytes deserializes content and stores it as a staged value
	// under roundID, without applying it. Used by followers during 2PC prepare.
	stageFromBytes(ver config.Version, roundID string, data []byte, ttl time.Duration) (stagedRef, error)

	// commitStaged atomically swaps the staged value live. If the staged ref
	// is unknown (e.g., TTL expired), returns an error — the caller should
	// fall back to reloading from storage and swapping directly.
	commitStaged(staged stagedRef) error

	// commitByRoundID is a convenience used by the follower commit handler
	// when it only knows the roundID (not the original stagedRef).
	commitByRoundID(roundID string) (found bool, err error)

	// abortStaged discards the staged value; no-op if unknown.
	abortStaged(staged stagedRef)

	// abortByRoundID discards the staged value by roundID; no-op if unknown.
	abortByRoundID(roundID string)
}

// stagedCollection holds a staged collection value awaiting commit.
type stagedCollection[T any] struct {
	id    string
	ver   config.Version
	items []T
	timer *time.Timer
}

func (s *stagedCollection[T]) roundID() string { return s.id }

// stagedSingleton holds a staged singleton value awaiting commit.
type stagedSingleton[T any] struct {
	id    string
	ver   config.Version
	value T
	timer *time.Timer
}

func (s *stagedSingleton[T]) roundID() string { return s.id }

type collectionReg[T any] struct {
	cfg    *config.Collection[T]
	src    source.CollectionSource[T]
	logger dlog.Logger

	stageMu sync.Mutex
	staged  map[string]*stagedCollection[T]
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

func (r *collectionReg[T]) fetchAndStage(ctx context.Context, ver config.Version, roundID string, ttl time.Duration) ([]byte, stagedRef, error) {
	items, err := r.src.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("manager: fetch %s: %w", r.cfg.Name(), err)
	}

	data, err := json.Marshal(items)
	if err != nil {
		return nil, nil, fmt.Errorf("manager: marshal %s: %w", r.cfg.Name(), err)
	}

	staged := r.stash(ver, roundID, items, ttl)

	r.logger.Debug("manager: collection staged",
		dlog.String("collection", r.cfg.Name()),
		dlog.String("round_id", roundID),
		dlog.Int("items", len(items)),
		dlog.String("version", ver.String()),
	)

	return data, staged, nil
}

func (r *collectionReg[T]) stageFromBytes(ver config.Version, roundID string, data []byte, ttl time.Duration) (stagedRef, error) {
	var items []T
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("manager: unmarshal %s: %w", r.cfg.Name(), err)
	}

	return r.stash(ver, roundID, items, ttl), nil
}

func (r *collectionReg[T]) stash(ver config.Version, roundID string, items []T, ttl time.Duration) *stagedCollection[T] {
	r.stageMu.Lock()
	defer r.stageMu.Unlock()

	if r.staged == nil {
		r.staged = make(map[string]*stagedCollection[T])
	}

	// Drop any previous staged entry for this round (upsert).
	if prev, ok := r.staged[roundID]; ok && prev.timer != nil {
		prev.timer.Stop()
	}

	s := &stagedCollection[T]{id: roundID, ver: ver, items: items}
	if ttl > 0 {
		s.timer = time.AfterFunc(ttl, func() {
			r.stageMu.Lock()
			if cur, ok := r.staged[roundID]; ok && cur == s {
				delete(r.staged, roundID)
			}
			r.stageMu.Unlock()
			r.logger.Warn("manager: staged collection dropped after TTL",
				dlog.String("collection", r.cfg.Name()),
				dlog.String("round_id", roundID),
			)
		})
	}
	r.staged[roundID] = s
	return s
}

func (r *collectionReg[T]) commitStaged(staged stagedRef) error {
	s, ok := staged.(*stagedCollection[T])
	if !ok || s == nil {
		return fmt.Errorf("manager: commit %s: invalid staged ref", r.cfg.Name())
	}

	r.stageMu.Lock()
	cur, present := r.staged[s.id]
	if present && cur == s {
		delete(r.staged, s.id)
		if s.timer != nil {
			s.timer.Stop()
		}
	}
	r.stageMu.Unlock()

	if !present {
		return fmt.Errorf("manager: commit %s: staged entry %q not found (TTL expired?)", r.cfg.Name(), s.id)
	}

	if err := r.cfg.Swap(s.ver, s.items); err != nil {
		return fmt.Errorf("manager: commit swap %s: %w", r.cfg.Name(), err)
	}

	r.logger.Debug("manager: collection committed from stage",
		dlog.String("collection", r.cfg.Name()),
		dlog.String("round_id", s.id),
		dlog.String("version", s.ver.String()),
	)
	return nil
}

func (r *collectionReg[T]) commitByRoundID(roundID string) (bool, error) {
	r.stageMu.Lock()
	s, ok := r.staged[roundID]
	if ok {
		delete(r.staged, roundID)
		if s.timer != nil {
			s.timer.Stop()
		}
	}
	r.stageMu.Unlock()

	if !ok {
		return false, nil
	}

	if err := r.cfg.Swap(s.ver, s.items); err != nil {
		return true, fmt.Errorf("manager: commit swap %s: %w", r.cfg.Name(), err)
	}
	return true, nil
}

func (r *collectionReg[T]) abortStaged(staged stagedRef) {
	if staged == nil {
		return
	}
	r.abortByRoundID(staged.roundID())
}

func (r *collectionReg[T]) abortByRoundID(roundID string) {
	r.stageMu.Lock()
	defer r.stageMu.Unlock()

	s, ok := r.staged[roundID]
	if !ok {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	delete(r.staged, roundID)
}

type singletonReg[T any] struct {
	cfg    *config.Singleton[T]
	src    source.SingletonSource[T]
	logger dlog.Logger

	stageMu sync.Mutex
	staged  map[string]*stagedSingleton[T]
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

func (r *singletonReg[T]) fetchAndStage(ctx context.Context, ver config.Version, roundID string, ttl time.Duration) ([]byte, stagedRef, error) {
	item, err := r.src.Get(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("manager: fetch singleton %s: %w", r.cfg.Name(), err)
	}

	data, err := json.Marshal(item)
	if err != nil {
		return nil, nil, fmt.Errorf("manager: marshal singleton %s: %w", r.cfg.Name(), err)
	}

	staged := r.stash(ver, roundID, *item, ttl)

	r.logger.Debug("manager: singleton staged",
		dlog.String("singleton", r.cfg.Name()),
		dlog.String("round_id", roundID),
		dlog.String("version", ver.String()),
	)

	return data, staged, nil
}

func (r *singletonReg[T]) stageFromBytes(ver config.Version, roundID string, data []byte, ttl time.Duration) (stagedRef, error) {
	var item T
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, fmt.Errorf("manager: unmarshal singleton %s: %w", r.cfg.Name(), err)
	}

	return r.stash(ver, roundID, item, ttl), nil
}

func (r *singletonReg[T]) stash(ver config.Version, roundID string, value T, ttl time.Duration) *stagedSingleton[T] {
	r.stageMu.Lock()
	defer r.stageMu.Unlock()

	if r.staged == nil {
		r.staged = make(map[string]*stagedSingleton[T])
	}

	if prev, ok := r.staged[roundID]; ok && prev.timer != nil {
		prev.timer.Stop()
	}

	s := &stagedSingleton[T]{id: roundID, ver: ver, value: value}
	if ttl > 0 {
		s.timer = time.AfterFunc(ttl, func() {
			r.stageMu.Lock()
			if cur, ok := r.staged[roundID]; ok && cur == s {
				delete(r.staged, roundID)
			}
			r.stageMu.Unlock()
			r.logger.Warn("manager: staged singleton dropped after TTL",
				dlog.String("singleton", r.cfg.Name()),
				dlog.String("round_id", roundID),
			)
		})
	}
	r.staged[roundID] = s
	return s
}

func (r *singletonReg[T]) commitStaged(staged stagedRef) error {
	s, ok := staged.(*stagedSingleton[T])
	if !ok || s == nil {
		return fmt.Errorf("manager: commit singleton %s: invalid staged ref", r.cfg.Name())
	}

	r.stageMu.Lock()
	cur, present := r.staged[s.id]
	if present && cur == s {
		delete(r.staged, s.id)
		if s.timer != nil {
			s.timer.Stop()
		}
	}
	r.stageMu.Unlock()

	if !present {
		return fmt.Errorf("manager: commit singleton %s: staged entry %q not found (TTL expired?)", r.cfg.Name(), s.id)
	}

	if err := r.cfg.Swap(s.ver, s.value); err != nil {
		return fmt.Errorf("manager: commit swap singleton %s: %w", r.cfg.Name(), err)
	}
	return nil
}

func (r *singletonReg[T]) commitByRoundID(roundID string) (bool, error) {
	r.stageMu.Lock()
	s, ok := r.staged[roundID]
	if ok {
		delete(r.staged, roundID)
		if s.timer != nil {
			s.timer.Stop()
		}
	}
	r.stageMu.Unlock()

	if !ok {
		return false, nil
	}

	if err := r.cfg.Swap(s.ver, s.value); err != nil {
		return true, fmt.Errorf("manager: commit swap singleton %s: %w", r.cfg.Name(), err)
	}
	return true, nil
}

func (r *singletonReg[T]) abortStaged(staged stagedRef) {
	if staged == nil {
		return
	}
	r.abortByRoundID(staged.roundID())
}

func (r *singletonReg[T]) abortByRoundID(roundID string) {
	r.stageMu.Lock()
	defer r.stageMu.Unlock()

	s, ok := r.staged[roundID]
	if !ok {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	delete(r.staged, roundID)
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
