# Sync Protocol

This document explains how config changes propagate from Directus to all application replicas.

## Participants

- **Directus** — the source of truth for all config data
- **Leader** — the replica that holds the Postgres advisory lock; performs fetches from Directus
- **Followers** — all other replicas; receive data via storage snapshots
- **Postgres** — stores snapshots, apply logs, advisory locks, and instance registry
- **Notify channel** — delivers sync/rollback events (Postgres LISTEN/NOTIFY or Redis Pub/Sub)

## Leader Election

Leader election uses a **Postgres session-level advisory lock** (`pg_try_advisory_lock`).

```
Replica A: pg_try_advisory_lock(987654321) → true  (leader)
Replica B: pg_try_advisory_lock(987654321) → false (follower)
Replica C: pg_try_advisory_lock(987654321) → false (follower)
```

- The lock is attempted at the start of each poll cycle
- If acquired → run leader protocol
- If not acquired → do nothing (follower reacts to notifications)
- Lock is held for the duration of one sync cycle, then released
- If the leader crashes, Postgres automatically releases the session lock

The advisory lock key is configurable via `Options.AdvisoryLockKey`. All instances of the same service must use the same key.

## Leader Protocol

On each poll cycle (default: every 5 minutes), the leader does this for each registered collection:

```
1. VERSION CHECK
   GET /items/{collection}?sort=-date_updated&limit=1&fields=date_updated
   → Parse max(date_updated) as new version
   → Compare with current in-memory version
   → If equal: skip (no change)

2. FETCH
   GET /items/{collection}  (with configured fields, deep, filters)
   → Unmarshal into []T
   → Call Config[T].Swap(newVersion, items)
   → This atomically updates in-memory data + fires OnChange hooks + recomputes Views

3. PERSIST
   INSERT INTO config_snapshots (collection, version, content, status='pending')
   → If cache enabled: write to Redis (sync or async based on strategy)

4. SELF-LOG
   INSERT INTO config_apply_log (instanceID, collection, version, 'applied')

5. NOTIFY
   pg_notify('config_sync', '{"action":"sync","collection":"businesses","version":"2025-01-02T00:00:00Z"}')
   → Or Redis PUBLISH if using Redis notify channel

6. WAIT FOR CONFIRMATIONS
   Loop (500ms interval, 30s timeout):
     SELECT COUNT(*) FROM config_apply_log WHERE collection=$1 AND version=$2 AND status='applied'
     SELECT COUNT(*) FROM config_instances WHERE service_name=$1 AND last_heartbeat > NOW() - 30s
     → If applied_count >= alive_count: all replicas confirmed

7. ACTIVATE
   UPDATE config_snapshots SET status='active' WHERE collection=$1 AND version=$2
   UPDATE config_snapshots SET status='inactive' WHERE collection=$1 AND status='active' AND version!=$2
```

## Follower Protocol

Followers listen on the notification channel. When they receive a sync event:

```
1. RECEIVE EVENT
   {"action": "sync", "collection": "businesses", "version": "2025-01-02T00:00:00Z"}

2. LOAD SNAPSHOT
   SELECT content FROM config_snapshots WHERE collection=$1 AND version=$2

3. APPLY
   Unmarshal content into []T
   Call Config[T].Swap(version, items)
   → Triggers OnChange hooks + View recomputes (same as leader)

4. LOG
   INSERT INTO config_apply_log (instanceID, collection, version, 'applied')
```

## Startup Sequence

When a replica starts, it loads data in this priority order:

```
1. REGISTER
   INSERT INTO config_instances (instanceID, serviceName)

2. LOAD FROM CACHE (optional, if ReadThrough/ReadWriteThrough strategy)
   Redis GET director:{collection}
   → Fastest: no Postgres or Directus needed
   → May be slightly stale depending on TTL

3. LOAD FROM STORAGE
   SELECT content FROM config_snapshots WHERE collection=$1 AND status='active'
   → Only if cache miss or cache version is older

4. INITIAL SYNC (leader only)
   Full leader protocol for all registered collections

5. START EVENT LOOPS
   - Poll ticker (default 5m)
   - Heartbeat ticker (default 10s)
   - Notification listener
```

This means replicas can serve requests immediately after step 2 or 3, before Directus is even contacted.

## Sequence Diagram

```
        Leader              Postgres            Follower A          Follower B
          │                    │                    │                    │
          │ pg_try_advisory_lock                    │                    │
          │───────────────────►│                    │                    │
          │ ◄── true           │                    │                    │
          │                    │                    │                    │
          │ GET /items/biz?sort=-date_updated&limit=1  (to Directus)    │
          │ ◄── date_updated changed                │                    │
          │                    │                    │                    │
          │ GET /items/biz     (full fetch from Directus)               │
          │ ◄── [{id:1,...}]   │                    │                    │
          │                    │                    │                    │
          │ Config.Swap()      │                    │                    │
          │ (local apply)      │                    │                    │
          │                    │                    │                    │
          │ INSERT snapshot    │                    │                    │
          │───────────────────►│                    │                    │
          │                    │                    │                    │
          │ INSERT apply_log   │                    │                    │
          │───────────────────►│                    │                    │
          │                    │                    │                    │
          │ pg_notify(sync)    │                    │                    │
          │───────────────────►│                    │                    │
          │                    │ ──notification────►│                    │
          │                    │ ──notification────────────────────────►│
          │                    │                    │                    │
          │                    │    SELECT snapshot │                    │
          │                    │◄───────────────────│                    │
          │                    │───content─────────►│                    │
          │                    │                    │ Config.Swap()      │
          │                    │                    │                    │
          │                    │                    │ INSERT apply_log   │
          │                    │◄───────────────────│                    │
          │                    │                    │                    │
          │                    │            SELECT snapshot              │
          │                    │◄───────────────────────────────────────│
          │                    │───content──────────────────────────────►│
          │                    │                    │                    │ Config.Swap()
          │                    │                    │                    │
          │                    │                    │     INSERT apply_log
          │                    │◄───────────────────────────────────────│
          │                    │                    │                    │
          │ COUNT(applied) >= COUNT(alive)          │                    │
          │───────────────────►│                    │                    │
          │ ◄── confirmed      │                    │                    │
          │                    │                    │                    │
          │ UPDATE status=active                    │                    │
          │───────────────────►│                    │                    │
          │                    │                    │                    │
          │ pg_advisory_unlock │                    │                    │
          │───────────────────►│                    │                    │
```

## Rollback

If the confirmation timeout expires before all replicas confirm:

1. Leader marks the snapshot as `failed`
2. Leader publishes a `rollback` event
3. All replicas (including leader) load the previous `active` snapshot from storage
4. All replicas swap back to the old data

## Instance Registry & Heartbeat

Each replica registers itself in `config_instances` and sends heartbeats every 10 seconds. The leader uses `AliveCount` (instances with heartbeat newer than 30s) to know how many confirmations to expect.

If a replica dies without deregistering, its heartbeat goes stale and it's excluded from the confirmation count within 30 seconds.

## WebSocket-Triggered Sync

When `WithWebSocket(ws)` is configured, the manager subscribes to Directus WebSocket events for all registered collections. Each subscription uses a UID (`sub_{collection}`) to map events back to collections (Directus WS events don't include the collection name).

### How it differs from polling

| Aspect | Poll-based | WebSocket-triggered |
|---|---|---|
| Latency | Up to PollInterval (default 5m) | Near-instant |
| Version check | Fetches `max(date_updated)`, skips if unchanged | **Forced sync** — skips version comparison |
| Poll interval | Normal (5m) | Safety-net only (15m) |
| Failure mode | Continues polling | Falls back to normal polling |

### Forced sync (`leaderSyncForced`)

When a WebSocket event arrives, we **know** something changed — no need to compare versions. The forced sync:

1. Fetches current version (still needed for snapshot labeling)
2. **Skips** the "is version equal?" check
3. Full fetch → swap → snapshot → notify (same as regular sync)

This is critical because `date_updated` may not be populated for items created via the API when the special metadata isn't applied (a known Directus 11 quirk with API-created fields).

### WS event format

Directus sends subscription events as:

```json
{
    "type": "subscription",
    "uid": "sub_businesses",
    "event": "create",
    "data": [{"id": 1, "name": "New Item", ...}]
}
```

The `init` event (subscription confirmation) is filtered out. Only `create`, `update`, and `delete` events are processed.

### Fallback behavior

If the WebSocket connection drops:
1. The WS channel closes
2. Manager sets `wsEvents = nil` (disables the select case)
3. Poll ticker resets to normal `PollInterval`
4. No panics, no goroutine leaks — seamless fallback
