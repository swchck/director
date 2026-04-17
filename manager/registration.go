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

// ErrValidationFailed is returned by registration entry points when a
// user-supplied validator rejects a fetched or staged value. The error is
// wrapped with %w so callers can match it via errors.Is.
//
// On validation failure, the in-memory config is NOT swapped — the cluster
// stays on the previous known-good version. The leader's snapshot is not
// persisted (eventually-consistent path) or the 2PC round is aborted
// (RequireUnanimousApply path). The leader retries on the next poll/WS cycle;
// once the source data is fixed, the new version is fetched and applied.
//
// Validation failures are logged at most once per (collection, version) tuple
// to avoid flooding the logs while the cluster waits for the source to be
// fixed; the dedup cache resets on the next successful apply.
var ErrValidationFailed = errors.New("manager: validation failed")

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

	// reportFailure records an external failure (e.g. 2PC round aborted on
	// leader because a follower returned prepare_failed) using the same
	// per-version dedup as the internal validator path. The log entry is
	// emitted at most once per (collection, version) tuple.
	reportFailure(ver config.Version, kind string, err error)

	// shouldReport returns true when (ver, kind) hasn't been logged yet by
	// reportFailure, allowing callers to dedup their own contextual log
	// entries without re-emitting the generic warning. Calling shouldReport
	// also marks the (ver, kind) tuple as reported, so subsequent calls with
	// the same arguments return false until the next successful apply.
	shouldReport(ver config.Version, kind string) bool
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

// failureState tracks the most recently reported failure version so the same
// validation/abort error isn't logged repeatedly while the cluster waits for
// the upstream data to be fixed. Cleared on successful apply.
type failureState struct {
	mu      sync.Mutex
	lastVer config.Version
	lastKey string // {kind}:{ver} — guards against same version with different failure kinds being silenced
}

func (f *failureState) shouldLog(ver config.Version, kind string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := kind + ":" + ver.String()
	if f.lastKey == key {
		return false
	}
	f.lastVer = ver
	f.lastKey = key
	return true
}

func (f *failureState) clear() {
	f.mu.Lock()
	f.lastVer = config.Version{}
	f.lastKey = ""
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

func (r *collectionReg[T]) fetchAndSwap(ctx context.Context, ver config.Version) ([]byte, error) {
	items, err := r.src.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("manager: fetch %s: %w", r.cfg.Name(), err)
	}

	items = r.applyDefaults(items)

	if r.validator != nil {
		if vErr := r.validator(items); vErr != nil {
			r.reportFailure(ver, "validator", vErr)
			return nil, fmt.Errorf("manager: %s: %w: %w", r.cfg.Name(), ErrValidationFailed, vErr)
		}
	}

	oldCount := r.cfg.Count()

	data, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("manager: marshal %s: %w", r.cfg.Name(), err)
	}

	if err := r.cfg.Swap(ver, items); err != nil {
		return data, fmt.Errorf("manager: swap %s: %w", r.cfg.Name(), err)
	}

	r.failure.clear()

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

func (r *singletonReg[T]) fetchAndSwap(ctx context.Context, ver config.Version) ([]byte, error) {
	item, err := r.src.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("manager: fetch singleton %s: %w", r.cfg.Name(), err)
	}

	r.applyDefault(item)

	if r.validator != nil {
		if vErr := r.validator(item); vErr != nil {
			r.reportFailure(ver, "validator", vErr)
			return nil, fmt.Errorf("manager: %s: %w: %w", r.cfg.Name(), ErrValidationFailed, vErr)
		}
	}

	data, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("manager: marshal singleton %s: %w", r.cfg.Name(), err)
	}

	if err := r.cfg.Swap(ver, *item); err != nil {
		return data, fmt.Errorf("manager: swap singleton %s: %w", r.cfg.Name(), err)
	}

	r.failure.clear()

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

	r.failure.clear()
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

	s, ok := r.staged[roundID]
	if !ok {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	delete(r.staged, roundID)
}

// CollectionOption configures a registered collection.
// Returned by helpers like WithCollectionValidator.
type CollectionOption[T any] func(*collectionReg[T])

// SingletonOption configures a registered singleton.
type SingletonOption[T any] func(*singletonReg[T])

// WithCollectionDefaults installs a per-item defaults function for a collection.
//
// The function is called for each item after fetch/deserialize and before
// validation. It receives the item and returns it with default values applied
// for any zero-valued fields.
//
// Example:
//
//	manager.WithCollectionDefaults(func(p Product) Product {
//	    if p.Currency == "" { p.Currency = "USD" }
//	    if p.MaxStock == 0 { p.MaxStock = 100 }
//	    return p
//	})
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

// WithCollectionValidator installs a pre-apply validator for a collection.
//
// The validator is invoked after the items are fetched (or received as a 2PC
// snapshot) and before the in-memory config is swapped. If it returns an
// error, the swap is skipped, the cluster stays on the previous version, and
// the leader retries on the next poll/WS cycle. The same (collection, version)
// failure is logged at most once.
//
// In RequireUnanimousApply mode a follower-side rejection causes the leader's
// 2PC round to abort — every replica should typically install the same
// validator to avoid one-sided rejections that block cluster-wide updates.
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

// RegisterCollectionSource registers a collection with a generic data source.
// Use this when implementing a custom backend (not Directus) or to attach
// per-collection options like WithCollectionValidator.
//
// Example with a custom source:
//
//	manager.RegisterCollectionSource(mgr, products, &myCustomAPI{},
//	    manager.WithCollectionValidator(func(items []Product) error {
//	        if len(items) == 0 { return errors.New("empty product list") }
//	        return nil
//	    }))
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

// RegisterCollection registers a collection sourced from Directus.
// This is a convenience wrapper that creates a source.CollectionSource from directus.Items[T].
//
// opts are Directus query options applied to every fetch (e.g. WithFields, WithDeep).
// To attach a validator or other manager-level options, use
// RegisterCollectionSource with source.FromDirectus instead.
func RegisterCollection[T any](m *Manager, cfg *config.Collection[T], items *directus.Items[T], opts ...directus.QueryOption) {
	RegisterCollectionSource(m, cfg, source.FromDirectus(items, opts...))
}

// RegisterSingleton registers a singleton sourced from Directus.
// This is a convenience wrapper that creates a source.SingletonSource from directus.Singleton[T].
// To attach a validator, use RegisterSingletonSource with source.FromDirectusSingleton.
func RegisterSingleton[T any](m *Manager, cfg *config.Singleton[T], singleton *directus.Singleton[T], opts ...directus.QueryOption) {
	RegisterSingletonSource(m, cfg, source.FromDirectusSingleton(singleton, opts...))
}
