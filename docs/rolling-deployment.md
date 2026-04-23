# Rolling Deployments

This guide covers how `director` behaves during Kubernetes rolling updates and how to avoid common pitfalls.

## The Problem

During a rolling deployment with 15 pods and `maxUnavailable=1`:

1. A pod receives `SIGTERM` and begins shutting down
2. Its heartbeat goes stale after `staleThreshold` (default 30s)
3. Meanwhile, the leader pod may be one of the pods being replaced
4. New pods start and need config data before accepting traffic

Three things can go wrong:

- **Leader vacuum**: if the leader dies, no new leader is elected until the next `PollInterval` (default 5m). All followers wait for a notification that never comes.
- **Phantom instances (2PC)**: a dying pod remains in `AliveInstances` for up to 30s. The leader includes it in the prepare target set, it never responds, and the round aborts on timeout.
- **New collection deadlock (2PC)**: when a rolling deploy adds a new collection, old pods don't have it registered. The leader's 2PC prepare for the new collection is silently ignored by old pods, causing a 30s timeout and abort. Since the new pod's readiness probe requires all collections to be loaded, the pod never becomes ready, blocking the rolling deploy.

## Solution

### Fast leader re-election

The manager attempts `pg_try_advisory_lock` on every heartbeat tick (default 10s), not just on poll ticks. This reduces the leader vacuum from `PollInterval` to `HeartbeatInterval`.

The overhead is one `pg_try_advisory_lock` call per pod per heartbeat — trivial. If the lock is acquired, `syncAll` runs version checks and skips the full fetch when versions match.

### Deregister-first shutdown

Call `manager.Stop()` on `SIGTERM`. Stop deregisters the instance from the registry **before** stopping the event loop:

```go
func (m *Manager) Stop() {
    // Deregister first — removes this instance from AliveInstances immediately.
    m.deregisterOnce.Do(func() {
        m.registry.Deregister(context.Background(), m.instanceID)
    })
    // Then cancel the context to stop the event loop.
    m.cancel()
}
```

This removes the instance from `AliveInstances` immediately, reducing the phantom window from 30s to effectively zero.

The deregistration uses `sync.Once` to avoid double-deregister (both `Stop()` and the `defer` in `Start()` call it).

### 2PC acknowledgement for unknown collections

When a follower receives a prepare event for a collection it doesn't manage (e.g. a new collection added in a newer code version), it logs `"prepared"` instead of silently ignoring the event. This allows the leader's 2PC round to succeed without waiting for a 30s timeout.

The semantic is correct: a pod that doesn't manage a collection has no state to protect — any version is acceptable. The commit and abort handlers already return early for unknown collections, so no data is applied.

This works symmetrically:
- **New pods syncing new collections** → old pods ACK
- **Old pods syncing removed collections** → new pods ACK

## Kubernetes Configuration

### Deployment manifest

```yaml
spec:
  replicas: 15
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 1
  template:
    spec:
      terminationGracePeriodSeconds: 30
      containers:
      - name: app
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8080
          initialDelaySeconds: 1
          periodSeconds: 2
          failureThreshold: 30
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
        lifecycle:
          preStop:
            exec:
              command: ["/bin/sh", "-c", "kill -TERM 1 && sleep 5"]
```

### Readiness probe

Use `manager.Ready()` — it returns true when every registered collection has been loaded (non-zero version). A pod without config is non-functional — it cannot serve game settings, product catalogs, or whatever the config contains.

```go
http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
    if !mgr.Ready() {
        w.WriteHeader(http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusOK)
})
```

`Ready()` is lock-free (versions are read via `atomic.Pointer`) and safe to call on every probe.

### Why pods start fast

The manager startup sequence loads data in priority order:

1. **Cache** (Redis) — sub-second, data available immediately
2. **Storage** (Postgres active snapshot) — fast, skipped if cache was fresh
3. **Source sync** — only runs if `hasEmptyConfigs()` (ManualSyncOnly) or always (auto mode)

With a hot cache, `Start()` loads all collections from cache, confirms versions match, and enters the event loop — no source or 2PC round-trips needed.

Do **not** call `SyncNow()` before `Start()`. Since `SyncNow()` runs before cache is loaded, every collection has a zero version, triggering a full source fetch and 2PC round for each one — even when the cache already has fresh data.

### preStop hook

The `preStop` hook sends `SIGTERM` to PID 1 (triggers `manager.Stop()` → deregister) and then sleeps 5s. The sleep gives the pod time to drain in-flight requests after deregistration.

## Signal handling in your application

```go
ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer cancel()

go func() {
    if err := mgr.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
        log.Fatal(err)
    }
}()

<-ctx.Done()
mgr.Stop()
```

## Adding or removing collections

When a rolling deploy adds a new collection, old pods (running previous code) don't have it registered. The 2PC acknowledgement for unknown collections ensures the sync succeeds without deadlock:

1. New pod becomes leader, runs `leaderSync2PC` for the new collection
2. Old pods receive prepare → unknown collection → log `"prepared"`
3. Leader sees all pods prepared → commits → new collection synced
4. Subsequent new pods load from cache → ready in ~1-2 seconds

**Order of operations** for adding a collection:

1. Create the collection in Directus (schema + data)
2. Deploy code that registers it

**Order of operations** for removing a collection:

1. Deploy code without the collection
2. Remove the collection from Directus

This follows the standard expand-then-contract migration pattern.

## Stress testing

The `example/11-rolling-stress/` directory contains a self-contained stress test:

- Mock source generating 100k items (~6MB snapshot)
- HTTP server with `/healthz`, `/readyz`, `/stats` endpoints
- Kubernetes manifests for 15 replicas with Postgres and Redis
- `test-rolling.sh` script that deploys, seeds, and triggers a rolling update

See the example's README for usage.

## Summary

| Scenario | Before | After |
|---|---|---|
| Leader vacuum on pod death | Up to 5 minutes | ~10 seconds |
| Phantom instance in 2PC | Up to 30 seconds | ~0 (immediate deregister) |
| New collection deadlock in 2PC | Permanent (rolling deploy stuck) | ~0 (unknown collection ACK) |
| New pod readiness | ~1-2s (from cache) | ~1-2s (unchanged) |
| Rolling update (15 pods) | Works, but leader gap | Seamless |
