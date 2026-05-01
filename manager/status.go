package manager

import (
	"maps"
	"sort"
	"time"
)

// ConfigKind distinguishes between the two registration shapes.
type ConfigKind string

const (
	// ConfigKindCollection identifies a multi-item collection registration.
	ConfigKindCollection ConfigKind = "collection"
	// ConfigKindSingleton identifies a single-row singleton registration.
	ConfigKindSingleton ConfigKind = "singleton"
)

// ConfigStatus reports the runtime state of a single registered config.
type ConfigStatus struct {
	// Name is the unique collection name used for storage, cache, and notify.
	Name string

	// Kind is "collection" or "singleton".
	Kind ConfigKind

	// Version is the in-memory version, formatted as RFC3339. Empty when the
	// config has not been loaded yet (cache miss + storage miss + no sync).
	Version string

	// ItemCount is the number of items currently held in memory. Always 0
	// for singletons.
	ItemCount int

	// LastSyncAt is the wall-clock time of the most recent sync attempt
	// (success or failure) targeting this config. Zero before the first
	// attempt completes.
	LastSyncAt time.Time

	// LastSyncErr is the error message from the most recent sync attempt.
	// Empty if the most recent attempt succeeded or none has run.
	LastSyncErr string
}

// Status reports the runtime state of the manager and all registered configs.
//
// Intended for /healthz/details endpoints, debug pages, and incident response
// — somewhere a human can see which collections are loaded, on what version,
// and whether the most recent sync succeeded.
//
// The returned value is a read-only snapshot. Calling Status does not block
// readers, sync cycles, or 2PC rounds.
type Status struct {
	// InstanceID is the unique identifier of this Manager instance.
	InstanceID string

	// ServiceName is the logical service name shared by all replicas.
	ServiceName string

	// ManualSync is true when Options.ManualSyncOnly was set — automatic
	// poll-based sync is disabled and only SyncNow advances state.
	ManualSync bool

	// Strict2PC is true when Options.RequireUnanimousApply was set — the
	// cluster runs strict 2PC instead of the default eventually-consistent
	// protocol.
	Strict2PC bool

	// IsLeader reports whether this instance held the advisory lock at the
	// last sync attempt. Best-effort signal: leadership is reacquired on
	// every poll, so a transient false during a poll cycle is normal.
	IsLeader bool

	// Configs lists every registered config sorted by Name for deterministic
	// output.
	Configs []ConfigStatus
}

// Status returns a read-only snapshot of the manager's runtime state.
//
// Safe to call from any goroutine, before or after Start. Before Start
// returns, IsLeader is false and per-config LastSyncAt is zero. After Start,
// the returned slice reflects the most recent sync activity.
func (m *Manager) Status() Status {
	m.syncStateMu.RLock()
	syncStates := make(map[string]syncStateEntry, len(m.syncState))
	maps.Copy(syncStates, m.syncState)
	m.syncStateMu.RUnlock()

	configs := make([]ConfigStatus, 0, len(m.configs))
	for _, reg := range m.configs {
		ver := reg.version()
		verStr := ""
		if !ver.IsZero() {
			verStr = ver.String()
		}

		st := syncStates[reg.name()]
		errMsg := ""
		if st.err != nil {
			errMsg = st.err.Error()
		}

		configs = append(configs, ConfigStatus{
			Name:        reg.name(),
			Kind:        reg.kind(),
			Version:     verStr,
			ItemCount:   reg.itemCount(),
			LastSyncAt:  st.at,
			LastSyncErr: errMsg,
		})
	}

	sort.Slice(configs, func(i, j int) bool { return configs[i].Name < configs[j].Name })

	return Status{
		InstanceID:  m.instanceID,
		ServiceName: m.opts.ServiceName,
		ManualSync:  m.opts.ManualSyncOnly,
		Strict2PC:   m.opts.RequireUnanimousApply,
		IsLeader:    m.isLeader.Load(),
		Configs:     configs,
	}
}

// syncStateEntry tracks the most recent sync attempt outcome for one config.
// Stored in Manager.syncState, keyed by collection name.
type syncStateEntry struct {
	at  time.Time
	err error
}

// recordSync stamps the most recent sync attempt for collection. err == nil
// means the attempt succeeded; a non-nil err is the reason it failed.
func (m *Manager) recordSync(collection string, err error) {
	m.syncStateMu.Lock()
	if m.syncState == nil {
		m.syncState = make(map[string]syncStateEntry)
	}
	m.syncState[collection] = syncStateEntry{at: time.Now(), err: err}
	m.syncStateMu.Unlock()
}
