package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/source"
)

// ErrValidationFailed wraps a validator rejection: nothing swaps, nothing is
// persisted or activated, and the same version is retried on the next cycle.
var ErrValidationFailed = errors.New("manager: validation failed")

// stagedRef is an opaque handle to a staged-but-not-committed config value.
// Produced by fetchAndStage / stageFromBytes, consumed by commitStaged.
type stagedRef interface {
	roundID() string
}

// registrable type-erases a collection or singleton registration, so the manager
// can drive any config without knowing its generic parameter.
type registrable interface {
	name() string

	version() config.Version

	// fetchVersion reports the source's latest modification timestamp.
	fetchVersion(ctx context.Context) (time.Time, error)

	// swapFromBytes deserializes content and swaps the in-memory config. Used when
	// loading from storage or cache rather than from the source.
	swapFromBytes(ver config.Version, data []byte) error

	// fetchAndStage fetches from the source and stages it under roundID without
	// applying, returning the bytes for the snapshot. Both leader paths use it.
	fetchAndStage(ctx context.Context, ver config.Version, roundID string, ttl time.Duration) (content []byte, staged stagedRef, err error)

	// stageFromBytes deserializes content and stores it as a staged value
	// under roundID, without applying it. Used by followers during 2PC prepare.
	stageFromBytes(ver config.Version, roundID string, data []byte, ttl time.Duration) (stagedRef, error)

	// commitStaged swaps the staged value live, erroring on an unknown ref (its TTL
	// dropped it) so the caller can reload from storage instead.
	commitStaged(staged stagedRef) error

	// commitByRoundID is a convenience used by the follower commit handler
	// when it only knows the roundID (not the original stagedRef).
	commitByRoundID(roundID string) (found bool, err error)

	// abortStaged discards the staged value; no-op if unknown.
	abortStaged(staged stagedRef)

	abortByRoundID(roundID string)

	// dropExpiredStages discards every staged value past its TTL and returns their
	// round IDs. Only sweepExpiredStages calls it.
	dropExpiredStages(now time.Time) []string

	// reportFailure records an external failure (e.g. a 2PC round the leader
	// aborted) under the same per-(collection, version) dedup as the validator.
	reportFailure(ver config.Version, kind string, err error)

	// shouldReport reports whether (ver, kind) is still unlogged, and marks it, so
	// a caller can emit its own contextual entry under the same dedup.
	shouldReport(ver config.Version, kind string) bool

	kind() ConfigKind

	// itemCount reports the items held in memory; singletons return 0.
	itemCount() int
}

// stagedCollection holds a staged collection value awaiting commit.
// A zero expiresAt means the value is held until commit or abort.
type stagedCollection[T any] struct {
	id        string
	ver       config.Version
	items     []T
	expiresAt time.Time
}

func (s *stagedCollection[T]) roundID() string { return s.id }

// stagedSingleton holds a staged singleton value awaiting commit.
type stagedSingleton[T any] struct {
	id        string
	ver       config.Version
	value     T
	expiresAt time.Time
}

func (s *stagedSingleton[T]) roundID() string { return s.id }

// failureState records the last reported failure version per kind, so a persistent
// fault logs once and a per-tick kind cannot evict a once-per-version kind.
type failureState struct {
	mu       sync.Mutex
	lastVers map[string]config.Version
}

func (f *failureState) shouldLog(ver config.Version, kind string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	if last, ok := f.lastVers[kind]; ok && last.Equal(ver) {
		return false
	}

	if f.lastVers == nil {
		f.lastVers = make(map[string]config.Version)
	}
	f.lastVers[kind] = ver

	return true
}

func (f *failureState) clear() {
	f.mu.Lock()
	f.lastVers = nil
	f.mu.Unlock()
}

type collectionReg[T any] struct {
	cfg       *config.Collection[T]
	src       source.CollectionSource[T]
	logger    dlog.Logger
	metrics   Metrics
	defaults  func(T) T
	validator func([]T) error

	stageMu sync.Mutex
	staged  map[string]*stagedCollection[T]

	failure failureState
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

func (r *collectionReg[T]) kind() ConfigKind { return ConfigKindCollection }

func (r *collectionReg[T]) itemCount() int { return r.cfg.Count() }

func (r *collectionReg[T]) reportFailure(ver config.Version, kind string, err error) {
	if !r.failure.shouldLog(ver, kind) {
		return
	}
	r.logger.Warn("manager: config update rejected",
		dlog.String("collection", r.cfg.Name()),
		dlog.String("version", ver.String()),
		dlog.String("kind", kind),
		dlog.Err(err),
	)
	if r.metrics != nil {
		r.metrics.ValidationFailed(r.cfg.Name())
	}
}

func (r *collectionReg[T]) shouldReport(ver config.Version, kind string) bool {
	return r.failure.shouldLog(ver, kind)
}

func (r *collectionReg[T]) applyDefaults(items []T) []T {
	if r.defaults == nil {
		return items
	}
	for i := range items {
		items[i] = r.defaults(items[i])
	}
	return items
}

func (r *collectionReg[T]) swapFromBytes(ver config.Version, data []byte) error {
	var items []T
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("manager: unmarshal %s: %w", r.cfg.Name(), err)
	}

	items = r.applyDefaults(items)

	if r.validator != nil {
		if vErr := r.validator(items); vErr != nil {
			r.reportFailure(ver, "validator", vErr)
			return fmt.Errorf("manager: %s: %w: %w", r.cfg.Name(), ErrValidationFailed, vErr)
		}
	}

	oldCount := r.cfg.Count()

	if err := r.cfg.Swap(ver, items); err != nil {
		return fmt.Errorf("manager: swap %s from bytes: %w", r.cfg.Name(), err)
	}

	r.failure.clear()

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

	items = r.applyDefaults(items)

	if r.validator != nil {
		if vErr := r.validator(items); vErr != nil {
			r.reportFailure(ver, "validator", vErr)
			return nil, nil, fmt.Errorf("manager: %s: %w: %w", r.cfg.Name(), ErrValidationFailed, vErr)
		}
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

	items = r.applyDefaults(items)

	if r.validator != nil {
		if vErr := r.validator(items); vErr != nil {
			r.reportFailure(ver, "validator", vErr)
			return nil, fmt.Errorf("manager: %s: %w: %w", r.cfg.Name(), ErrValidationFailed, vErr)
		}
	}

	return r.stash(ver, roundID, items, ttl), nil
}

func (r *collectionReg[T]) stash(ver config.Version, roundID string, items []T, ttl time.Duration) *stagedCollection[T] {
	r.stageMu.Lock()
	defer r.stageMu.Unlock()

	if r.staged == nil {
		r.staged = make(map[string]*stagedCollection[T])
	}

	s := &stagedCollection[T]{id: roundID, ver: ver, items: items}
	if ttl > 0 {
		s.expiresAt = time.Now().Add(ttl)
	}

	r.staged[roundID] = s

	return s
}

func (r *collectionReg[T]) dropExpiredStages(now time.Time) []string {
	r.stageMu.Lock()
	defer r.stageMu.Unlock()

	var dropped []string
	for id, s := range r.staged {
		if s.expiresAt.IsZero() || now.Before(s.expiresAt) {
			continue
		}

		delete(r.staged, id)
		dropped = append(dropped, id)
	}

	return dropped
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
	}
	r.stageMu.Unlock()

	if !present {
		return fmt.Errorf("manager: commit %s: staged entry %q not found (expired or aborted?)", r.cfg.Name(), s.id)
	}

	if err := r.cfg.Swap(s.ver, s.items); err != nil {
		return fmt.Errorf("manager: commit swap %s: %w", r.cfg.Name(), err)
	}

	r.failure.clear()

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
	}
	r.stageMu.Unlock()

	if !ok {
		return false, nil
	}

	if err := r.cfg.Swap(s.ver, s.items); err != nil {
		return true, fmt.Errorf("manager: commit swap %s: %w", r.cfg.Name(), err)
	}

	r.failure.clear()
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

	delete(r.staged, roundID)
}

type singletonReg[T any] struct {
	cfg       *config.Singleton[T]
	src       source.SingletonSource[T]
	logger    dlog.Logger
	metrics   Metrics
	defaults  func(T) T
	validator func(*T) error

	stageMu sync.Mutex
	staged  map[string]*stagedSingleton[T]

	failure failureState
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

func (r *singletonReg[T]) kind() ConfigKind { return ConfigKindSingleton }

func (r *singletonReg[T]) itemCount() int { return 0 }

func (r *singletonReg[T]) reportFailure(ver config.Version, kind string, err error) {
	if !r.failure.shouldLog(ver, kind) {
		return
	}
	r.logger.Warn("manager: config update rejected",
		dlog.String("singleton", r.cfg.Name()),
		dlog.String("version", ver.String()),
		dlog.String("kind", kind),
		dlog.Err(err),
	)
	if r.metrics != nil {
		r.metrics.ValidationFailed(r.cfg.Name())
	}
}

func (r *singletonReg[T]) shouldReport(ver config.Version, kind string) bool {
	return r.failure.shouldLog(ver, kind)
}

func (r *singletonReg[T]) applyDefault(item *T) {
	if r.defaults == nil || item == nil {
		return
	}
	*item = r.defaults(*item)
}

func (r *singletonReg[T]) swapFromBytes(ver config.Version, data []byte) error {
	var item T
	if err := json.Unmarshal(data, &item); err != nil {
		return fmt.Errorf("manager: unmarshal singleton %s: %w", r.cfg.Name(), err)
	}

	r.applyDefault(&item)

	if r.validator != nil {
		if vErr := r.validator(&item); vErr != nil {
			r.reportFailure(ver, "validator", vErr)
			return fmt.Errorf("manager: %s: %w: %w", r.cfg.Name(), ErrValidationFailed, vErr)
		}
	}

	if err := r.cfg.Swap(ver, item); err != nil {
		return fmt.Errorf("manager: swap singleton %s from bytes: %w", r.cfg.Name(), err)
	}

	r.failure.clear()

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

	r.applyDefault(item)

	if r.validator != nil {
		if vErr := r.validator(item); vErr != nil {
			r.reportFailure(ver, "validator", vErr)
			return nil, nil, fmt.Errorf("manager: %s: %w: %w", r.cfg.Name(), ErrValidationFailed, vErr)
		}
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

	r.applyDefault(&item)

	if r.validator != nil {
		if vErr := r.validator(&item); vErr != nil {
			r.reportFailure(ver, "validator", vErr)
			return nil, fmt.Errorf("manager: %s: %w: %w", r.cfg.Name(), ErrValidationFailed, vErr)
		}
	}

	return r.stash(ver, roundID, item, ttl), nil
}

func (r *singletonReg[T]) stash(ver config.Version, roundID string, value T, ttl time.Duration) *stagedSingleton[T] {
	r.stageMu.Lock()
	defer r.stageMu.Unlock()

	if r.staged == nil {
		r.staged = make(map[string]*stagedSingleton[T])
	}

	s := &stagedSingleton[T]{id: roundID, ver: ver, value: value}
	if ttl > 0 {
		s.expiresAt = time.Now().Add(ttl)
	}

	r.staged[roundID] = s

	return s
}

func (r *singletonReg[T]) dropExpiredStages(now time.Time) []string {
	r.stageMu.Lock()
	defer r.stageMu.Unlock()

	var dropped []string
	for id, s := range r.staged {
		if s.expiresAt.IsZero() || now.Before(s.expiresAt) {
			continue
		}

		delete(r.staged, id)
		dropped = append(dropped, id)
	}

	return dropped
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
	}
	r.stageMu.Unlock()

	if !present {
		return fmt.Errorf("manager: commit singleton %s: staged entry %q not found (expired or aborted?)", r.cfg.Name(), s.id)
	}

	if err := r.cfg.Swap(s.ver, s.value); err != nil {
		return fmt.Errorf("manager: commit swap singleton %s: %w", r.cfg.Name(), err)
	}

	r.failure.clear()
	return nil
}

func (r *singletonReg[T]) commitByRoundID(roundID string) (bool, error) {
	r.stageMu.Lock()
	s, ok := r.staged[roundID]
	if ok {
		delete(r.staged, roundID)
	}
	r.stageMu.Unlock()

	if !ok {
		return false, nil
	}

	if err := r.cfg.Swap(s.ver, s.value); err != nil {
		return true, fmt.Errorf("manager: commit swap singleton %s: %w", r.cfg.Name(), err)
	}

	r.failure.clear()
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

	delete(r.staged, roundID)
}

// CollectionOption configures a registered collection.
// Returned by helpers like WithCollectionValidator.
type CollectionOption[T any] func(*collectionReg[T])

// SingletonOption configures a registered singleton.
type SingletonOption[T any] func(*singletonReg[T])

// WithCollectionDefaults installs a per-item defaults function, called for each item
// after fetch/deserialize and before validation.
func WithCollectionDefaults[T any](fn func(T) T) CollectionOption[T] {
	return func(r *collectionReg[T]) {
		r.defaults = fn
	}
}

// WithSingletonDefaults installs a defaults function for a singleton.
// See WithCollectionDefaults for full semantics.
func WithSingletonDefaults[T any](fn func(T) T) SingletonOption[T] {
	return func(r *singletonReg[T]) {
		r.defaults = fn
	}
}

// WithCollectionValidator installs a validator run before any swap; on error nothing
// swaps. Under 2PC install the same one everywhere — a lone rejection aborts rounds.
func WithCollectionValidator[T any](v func([]T) error) CollectionOption[T] {
	return func(r *collectionReg[T]) {
		r.validator = v
	}
}

// WithSingletonValidator installs a pre-apply validator for a singleton.
// See WithCollectionValidator for full semantics.
func WithSingletonValidator[T any](v func(*T) error) SingletonOption[T] {
	return func(r *singletonReg[T]) {
		r.validator = v
	}
}

// RegisterCollectionSource registers a collection with a generic data source: any
// non-Directus backend, or to attach options like WithCollectionValidator.
func RegisterCollectionSource[T any](m *Manager, cfg *config.Collection[T], src source.CollectionSource[T], opts ...CollectionOption[T]) {
	r := &collectionReg[T]{
		cfg:     cfg,
		src:     src,
		logger:  m.logger,
		metrics: m.metrics,
	}
	for _, opt := range opts {
		opt(r)
	}
	m.register(r)
}

// RegisterSingletonSource registers a singleton with a generic data source.
// See RegisterCollectionSource for the options pattern.
func RegisterSingletonSource[T any](m *Manager, cfg *config.Singleton[T], src source.SingletonSource[T], opts ...SingletonOption[T]) {
	r := &singletonReg[T]{
		cfg:     cfg,
		src:     src,
		logger:  m.logger,
		metrics: m.metrics,
	}
	for _, opt := range opts {
		opt(r)
	}
	m.register(r)
}

// RegisterCollection registers a collection sourced from Directus. opts are Directus
// query options; for manager-level options use RegisterCollectionSource.
func RegisterCollection[T any](m *Manager, cfg *config.Collection[T], items *directus.Items[T], opts ...directus.QueryOption) {
	var sample T
	m.schemaCheckEntries = append(m.schemaCheckEntries, schemaCheckEntry{
		collection: items.Collection(),
		client:     items.Client(),
		sample:     sample,
	})
	RegisterCollectionSource(m, cfg, source.FromDirectus(items, opts...))
}

// RegisterSingleton registers a singleton sourced from Directus. For manager-level
// options use RegisterSingletonSource with source.FromDirectusSingleton.
func RegisterSingleton[T any](m *Manager, cfg *config.Singleton[T], singleton *directus.Singleton[T], opts ...directus.QueryOption) {
	var sample T
	m.schemaCheckEntries = append(m.schemaCheckEntries, schemaCheckEntry{
		collection: singleton.Collection(),
		client:     singleton.Client(),
		sample:     sample,
	})
	RegisterSingletonSource(m, cfg, source.FromDirectusSingleton(singleton, opts...))
}
