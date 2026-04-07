# 06 — Sync via WebSocket

Demonstrates real-time sync: Directus WebSocket events trigger immediate config updates without waiting for the poll interval.

**What you'll learn:**
- Setting up `WSClient` for real-time events
- Connecting WebSocket to the Manager via `WithWebSocket()`
- Instant sync on item create/update/delete
- Automatic fallback to polling if WebSocket disconnects

```bash
task up
go run example/06-sync-websocket/main.go
```

Create items in Directus UI — they appear instantly in the terminal.
