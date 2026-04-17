# Rolling Deployments

This guide covers how `director` behaves during Kubernetes rolling updates and how to avoid common pitfalls.

## The Problem

During a rolling deployment with 15 pods and `maxUnavailable=1`:

1. A pod receives `SIGTERM` and begins shutting down
2. Its heartbeat goes stale after `staleThreshold` (default 30s)
3. Meanwhile, the leader pod may be one of the pods being replaced
4. New pods start and need config data before accepting traffic

Two things can go wrong:

- **Leader vacuum**: if the leader dies, no new leader is elected until the next `PollInterval` (default 5m). All followers wait for a notification that never comes.
- **Phantom instances (2PC)**: a dying pod remains in `AliveInstances` for up to 30s. The leader includes it in the prepare target set, it never responds, and the round aborts on timeout.

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

The readiness endpoint should return 200 only when **all** config data is loaded and usable. A pod without config is non-functional — it cannot serve game settings, product catalogs, or whatever the config contains.

```go
func readyzHandler(collection *config.Collection[Item], settings *config.Singleton[Settings]) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if collection.Count() == 0 {
            w.WriteHeader(http.StatusServiceUnavailable)
            return
        }
        if _, ok := settings.Get(); !ok {
            w.WriteHeader(http.StatusServiceUnavailable)
            return
        }
        w.WriteHeader(http.StatusOK)
    }
}
```

### Why pods start fast

The manager startup sequence loads data in priority order:

1. **Cache** (Redis) — sub-second, data available immediately
2. **Storage** (Postgres active snapshot) — fast, skipped if cache was fresh
3. **Source sync** — may be slow, but pod is already ready from step 1

With `ReadWriteThrough` cache strategy, pods become ready within ~350ms of starting — well before the source is contacted.

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
| New pod readiness | ~350ms (from cache) | ~350ms (unchanged) |
| Rolling update (15 pods) | Works, but leader gap | Seamless |
