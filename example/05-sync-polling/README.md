# 05 — Sync via Polling

Demonstrates the Manager's poll-based sync: periodically checks Directus for changes and updates in-memory configs.

**What you'll learn:**
- Manager startup sequence (cache → storage → Directus)
- Poll interval configuration
- How version detection works (date_updated / date_created)
- Forcing an immediate sync with `SyncNow()`

```bash
task up
go run example/05-sync-polling/main.go
```

Create or edit items in Directus UI at http://localhost:8055 and watch the terminal for sync logs.
