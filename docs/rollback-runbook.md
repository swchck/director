# Runbook: Rolling a Collection Back

Use this when a bad config version is live across the cluster and you need every
replica back on the previous version without restarting pods.

Every automatic path is **monotonic**: follower catch-up, the handling of a `sync`
event, and the leader itself only ever move a collection to a *newer* version (see
[Forward-Only Leader](sync-protocol.md#forward-only-leader) and
[Follower Catch-Up](sync-protocol.md#follower-catch-up)). Flipping the `active` row
alone therefore no longer reverts anything — replicas refuse it, warn
`refusing to move to an older active version`, and keep serving what they have.
The `rollback` event below is the only sanctioned way a replica moves backwards.

## Before you start

Read this first, because it decides how many steps you need:

**The leader re-advances.** A rollback changes what replicas hold in memory and
what the `active` row names. It does *not* change the source. While the source
still reports a `LastModified` newer than the version you rolled back to, the
next leader cycle sees a version change, fetches, activates, and announces the
bad version again — undoing your rollback within one reconcile tick
(`HeartbeatInterval`, default 10s).

So a durable rollback is always two things:

1. Stop the roll-forward (step 1 below, or fix the source).
2. Move the cluster back (steps 2–3).

If you can fix the source quickly — revert the change in the CMS — do that
instead and skip this runbook entirely: the leader picks up the new
`LastModified` and rolls the whole cluster forward onto the corrected content on
its own, with no SQL and no lock held. This runbook is for when the source cannot
be corrected fast enough.

Values below use the defaults. Substitute your own if you changed them:
`AdvisoryLockKey` (default `987654321`), the notify channel name (default
`config_sync`), and the collection name.

## Step 1 — Freeze the roll-forward

Open a `psql` session and keep it open for the whole procedure:

```sql
SELECT pg_advisory_lock(987654321);
```

What it does: leader election is `pg_try_advisory_lock` on this key, so while you
hold it **no replica can become leader**. Nothing fetches from the source,
nothing activates a snapshot, and no 2PC round starts. The blocking form also
means the statement returns only once no leader is mid-cycle, so you are not
racing a sync in flight.

Notes:

- The lock is **session-scoped**. If the session drops, the lock is released and
  the next tick elects a leader again. Keep the terminal open; do not run this
  through a connection pooler in transaction mode.
- Followers keep serving, keep heartbeating, and keep reacting to notify events.
  Only leader work is frozen.
- Maintenance (snapshot/instance GC) is also frozen. That is harmless.

Verify no replica holds it any more:

```sql
SELECT pid, granted FROM pg_locks
WHERE locktype = 'advisory' AND objid = 987654321;
```

You should see exactly one granted row — your session.

## Step 2 — Activate the target snapshot

Find the version you want:

```sql
SELECT version, status, created_at
FROM director.config_snapshots
WHERE collection_name = 'articles'
ORDER BY created_at DESC
LIMIT 10;
```

Flip the `active` row in one transaction:

```sql
BEGIN;
UPDATE director.config_snapshots
   SET status = 'inactive'
 WHERE collection_name = 'articles' AND status = 'active';

UPDATE director.config_snapshots
   SET status = 'active'
 WHERE collection_name = 'articles' AND version = '<target-version>';
COMMIT;
```

What it does: makes the target version the one every replica loads on restart and
the one catch-up reconciles against. On its own it changes nothing in memory.

Verify exactly one active row:

```sql
SELECT version FROM director.config_snapshots
WHERE collection_name = 'articles' AND status = 'active';
```

If that returns zero rows, **stop** — a restarting pod would come up with no
config for the collection. Re-run the transaction.

## Step 3 — Announce the rollback

```sql
SELECT pg_notify('config_sync', '{"action":"rollback","collection":"articles"}');
```

With Redis as the notify transport, publish the same payload instead:

```
PUBLISH config_sync '{"action":"rollback","collection":"articles"}'
```

What it does: every replica loads the active snapshot and swaps to it, in either
direction. Note what is deliberately absent from the payload:

- **No `instance_id`.** Replicas drop events carrying their own instance ID, so
  an operator event must be unstamped to reach all of them — including whichever
  replica is currently leader.
- **No `version`.** The handler ignores it; the `active` row from step 2 decides.
  This is why step 2 comes first.

Each replica logs, at Warn:

```
manager: operator rollback applied to the active snapshot collection=articles from=<bad> to=<target> backwards=true
```

Each replica also rewrites its own cache entry to the target version, so a pod
that starts later warms from the target rather than from the version you are
rolling back. Nothing else can do this during the freeze — the leader-side cache
repair needs the advisory lock, which you are holding — which is why it is part of
this step and not a separate one. Replicas that missed the event do not rewrite
theirs; step 4 is how you find them.

Notify delivery is lossy (16-slot subscription buffer). If a replica misses the
event it stays on the bad version and does **not** self-heal — catch-up will not
move it backwards. Verify, and re-publish if the count is short.

## Step 4 — Verify every replica applied

```sql
SELECT l.instance_id
FROM director.config_apply_log l
WHERE l.collection_name = 'articles'
  AND l.version = '<target-version>'
  AND l.status = 'rolled_back';
```

Compare against the replicas that are alive:

```sql
SELECT instance_id FROM director.config_instances
WHERE service_name = '<your-service>'
  AND last_heartbeat > NOW() - INTERVAL '30 seconds';
```

Every alive instance must appear in the first result, **except instances that
started after step 3** — see below. Any other that does not either missed the event
(re-publish step 3) or failed to apply it — check its logs for
`manager: rollback swap`, which reports a deserialize or validator failure
against the target payload. A pre-apply validator that rejects the older payload
will refuse the rollback on that replica; there is no override.

`Manager.Status()` is the per-replica cross-check if you expose it: `Configs[].Version`
must equal the target version. It is the authoritative check — the apply-log query
above only sees replicas that were alive when you published.

A replica still ahead of the target reports it on every reconcile tick, so you do not
have to poll the queries above: watch the `FollowerFailed` metric for
`ErrActiveSnapshotBehind` (a replica holding a version newer than the active snapshot)
and re-publish step 3 for as long as it is non-zero. That counter reaching zero is the
end condition for this step.

## Pods that start during the freeze

Nothing to do. A pod that starts while you hold the lock comes up on the target
version on its own, and cannot undo your work:

- Startup loads the `active` snapshot, which is the target from step 2. The active
  snapshot overrides whatever the cache offered, in either direction, so a pod
  whose cache entry was never rewritten (it was not running at step 3) still lands
  on the target.
- It cannot become leader while you hold the lock, so it neither fetches from the
  source nor activates anything.

It will **not** appear in the step-4 `rolled_back` query — it never applied a
rollback, it simply started on the target version. Use `Manager.Status()` for those
replicas, or its `manager: loaded from storage` log line naming the target version.

## Step 5 — Fix the source, then release the lock

While the lock is held the cluster is frozen on the target version and nothing
syncs. Fix the source (revert the CMS change), then release:

```sql
SELECT pg_advisory_unlock(987654321);
```

Or just close the session.

Within one reconcile tick (default 10s) a replica becomes leader, reads the
source, and rolls the cluster forward onto whatever the source now says:

- Source corrected → cluster converges on the corrected version. Done.
- Source **not** corrected → the leader re-activates and re-announces the bad
  version. You are back where you started. Do not release the lock until the
  source is fixed.

One case does not converge on its own: if you fixed the source by **deleting**
rows, `max(date_updated)` can end up behind the version the cluster holds. The
leader then declines to sync and logs `declining to sync a source version behind
the one held` once, with `local` and `reported`
([why](sync-protocol.md#forward-only-leader)). With WebSocket enabled the delete
event forces the sync through and this does not arise. Without it, touch any
remaining item in the collection to give the source a forward timestamp.

## What this procedure does not do

- **It does not stop an in-flight 2PC round from finishing.** Step 1 waits for
  the current cycle rather than interrupting it, so by the time you hold the lock
  no round is running.
- **It does not clear staged values.** A replica that logged `prepared` for a
  round still honours a matching `commit` — that is what makes strict mode
  unanimous. In practice step 1 removes the leader that would send the `commit`,
  and the run loop drops the staged value on the first reconcile tick after
  `PrepareTTL`.
- **It does not delete the bad snapshot.** Deliberate: the row is the evidence.
  Once the incident is closed, mark it `failed` so it is never activated by
  accident:

  ```sql
  UPDATE director.config_snapshots SET status = 'failed'
  WHERE collection_name = 'articles' AND version = '<bad-version>';
  ```

  Never mark the *active* row failed — a restarting pod would find no config.

## If you run with `ManualSyncOnly`

Nothing auto-advances in manual mode: only `SyncNow` triggers a leader sync.
Step 1 is then optional — skip it as long as nothing calls `SyncNow` (a webhook,
a cron, a deploy hook) before the source is fixed. Steps 2–4 are unchanged.
