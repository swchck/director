//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/swchck/director/cache"
	rediscache "github.com/swchck/director/cache/redis"
	dcfg "github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
	pgnotify "github.com/swchck/director/notify/postgres"
	pgregistry "github.com/swchck/director/registry/postgres"
	pgstorage "github.com/swchck/director/storage/postgres"
)

// TestE2E_WebSocket_SubscriptionReceivesEvents tests that the Directus WebSocket
// delivers change events when items are created/updated/deleted.
func TestE2E_WebSocket_SubscriptionReceivesEvents(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Cleanup(func() { cleanupCollection(t, dc, "e2e_ws_events") })

	// Create collection.
	err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_ws_events",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			{Field: "value", Type: directus.FieldTypeString},
		},
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	// Connect WebSocket and subscribe.
	token := getAdminJWT(t)
	ws := directus.NewWSClient(testDirectusURL, token,
		directus.WithWSLogger(testLogger(t)),
	)
	defer ws.Close()

	events, err := ws.SubscribeWith(ctx,
		directus.WSSubscription{
			Collection: "e2e_ws_events",
			Query: &directus.SubscriptionQuery{
				Fields: []string{"id", "value"},
			},
		},
	)
	if err != nil {
		t.Fatalf("ws subscribe: %v", err)
	}

	// Wait a moment for subscription to be active.
	time.Sleep(500 * time.Millisecond)

	// Create an item — should trigger a WS event.
	type WSItem struct {
		Value string `json:"value"`
	}

	createItems := directus.NewItems[WSItem](dc, "e2e_ws_events")
	_, err = createItems.Create(ctx, &WSItem{Value: "hello-ws"})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	// Wait for the WebSocket event.
	select {
	case event := <-events:
		if event.Action != "create" {
			t.Errorf("event.Action = %q, want 'create'", event.Action)
		}

		if len(event.Data) == 0 {
			t.Error("event.Data is empty, expected item data")
		}

		t.Logf("WS event received: action=%s, data=%s", event.Action, string(event.Data))

	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for WebSocket event")
	}
}

// TestE2E_WebSocket_ManagerAutoSyncsOnChange tests the full flow:
// 1. Manager starts with WebSocket enabled
// 2. Initial sync loads existing items
// 3. A new item is created in Directus
// 4. WS event triggers manager to re-sync
// 5. Config collection auto-updates with the new item
func TestE2E_WebSocket_ManagerAutoSyncsOnChange(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Cleanup(func() { cleanupCollection(t, dc, "e2e_ws_sync") })

	// Create collection with date_updated field (added separately for special metadata).
	err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_ws_sync",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			{Field: "name", Type: directus.FieldTypeString},
			{Field: "score", Type: directus.FieldTypeInteger},
		},
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	if err := dc.CreateField(ctx, "e2e_ws_sync", directus.DateCreatedField()); err != nil {
		t.Fatalf("create date_created: %v", err)
	}

	if err := dc.CreateField(ctx, "e2e_ws_sync", directus.DateUpdatedField()); err != nil {
		t.Fatalf("create date_updated: %v", err)
	}

	// Seed initial data.
	type SyncItem struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Score int    `json:"score"`
	}

	type SyncCreate struct {
		Name  string `json:"name"`
		Score int    `json:"score"`
	}

	syncItems := directus.NewItems[SyncItem](dc, "e2e_ws_sync")
	createSyncItems := directus.NewItems[SyncCreate](dc, "e2e_ws_sync")

	_, err = createSyncItems.Create(ctx, &SyncCreate{Name: "initial", Score: 10})
	if err != nil {
		t.Fatalf("seed item: %v", err)
	}

	// Touch item so date_updated is populated.
	items, _ := syncItems.List(ctx)
	if len(items) > 0 {
		_, _ = syncItems.Update(ctx, "1", &SyncItem{Name: "initial", Score: 10})
	}

	// Set up infrastructure.
	pgPool := testPgPool(t)
	rdb := testRedisClient(t)

	store := pgstorage.NewStorage(pgPool)
	_ = store.Migrate(ctx)

	notif := pgnotify.NewChannel(pgPool, pgnotify.WithLogger(testLogger(t)))
	t.Cleanup(func() { notif.Close() })

	reg := pgregistry.NewRegistry(pgPool)
	redisCache := rediscache.NewCache(rdb, rediscache.WithTTL(5*time.Minute))

	// Create WebSocket client.
	token := getAdminJWT(t)
	ws := directus.NewWSClient(testDirectusURL, token,
		directus.WithWSLogger(testLogger(t)),
	)
	t.Cleanup(func() { ws.Close() })

	// Define config.
	config := dcfg.NewCollection[SyncItem]("e2e_ws_sync")

	// Create manager with WebSocket.
	mgr := manager.New(store, notif, reg,
		manager.Options{
			PollInterval:             time.Hour, // Long — we rely on WS, not polling.
			WaitConfirmationsTimeout: 5 * time.Second,
			ServiceName:              "e2e-ws-test",
		},
		manager.WithLogger(testLogger(t)),
		manager.WithCache(redisCache, cache.ReadWriteThrough),
		manager.WithWebSocket(ws),
	)

	manager.RegisterCollection(mgr, config, syncItems)

	// Start manager.
	mgrCtx, mgrCancel := context.WithCancel(ctx)
	defer mgrCancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(mgrCtx) }()

	// Wait for initial sync.
	time.Sleep(3 * time.Second)

	// Verify initial state.
	if config.Count() != 1 {
		t.Fatalf("initial: Count() = %d, want 1", config.Count())
	}

	initial, ok := config.Find(func(i SyncItem) bool { return i.Name == "initial" })
	if !ok {
		t.Fatal("initial item not found")
	}

	if initial.Score != 10 {
		t.Errorf("initial.Score = %d, want 10", initial.Score)
	}

	t.Log("initial sync OK, creating new item to trigger WS event...")

	// Create a new item — this should trigger a WS event → manager resync.
	_, err = createSyncItems.Create(ctx, &SyncCreate{Name: "ws-triggered", Score: 99})
	if err != nil {
		t.Fatalf("create ws-triggered item: %v", err)
	}

	// Also touch the new item to set date_updated for version detection.
	newItems, _ := syncItems.List(ctx, directus.WithFilter(directus.Field("name", "_eq", "ws-triggered")))
	if len(newItems) > 0 {
		_, _ = syncItems.Update(ctx, fmt.Sprintf("%d", newItems[0].ID),
			&SyncItem{Name: "ws-triggered", Score: 99})
	}

	// Wait for WS event → manager sync cycle.
	// The WS event should trigger syncOne → leaderSync → full refetch.
	deadline := time.After(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	synced := false
	for !synced {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for WS-triggered sync. Count=%d, want 2", config.Count())
		case <-ticker.C:
			if config.Count() == 2 {
				synced = true
			}
		}
	}

	// Verify the new item is in the config.
	wsItem, ok := config.Find(func(i SyncItem) bool { return i.Name == "ws-triggered" })
	if !ok {
		t.Fatal("ws-triggered item not found after WS sync")
	}

	if wsItem.Score != 99 {
		t.Errorf("ws-triggered item score = %d, want 99", wsItem.Score)
	}

	t.Log("WebSocket-triggered sync verified successfully!")

	mgrCancel()
	<-errCh
}

// TestE2E_WebSocket_UpdateTriggersResync tests that updating an existing item
// via Directus triggers a WS event that causes the manager to resync.
func TestE2E_WebSocket_UpdateTriggersResync(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Cleanup(func() { cleanupCollection(t, dc, "e2e_ws_update") })

	err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_ws_update",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			{Field: "name", Type: directus.FieldTypeString},
			{Field: "value", Type: directus.FieldTypeInteger},
		},
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	if err := dc.CreateField(ctx, "e2e_ws_update", directus.DateUpdatedField()); err != nil {
		t.Fatalf("create date_updated: %v", err)
	}

	type UpdateItem struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	type UpdateCreate struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	items := directus.NewItems[UpdateItem](dc, "e2e_ws_update")
	createItems := directus.NewItems[UpdateCreate](dc, "e2e_ws_update")

	_, err = createItems.Create(ctx, &UpdateCreate{Name: "target", Value: 100})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Touch to populate date_updated.
	_, _ = items.Update(ctx, "1", &UpdateItem{Name: "target", Value: 100})

	// Infrastructure.
	pgPool := testPgPool(t)
	rdb := testRedisClient(t)

	store := pgstorage.NewStorage(pgPool)
	_ = store.Migrate(ctx)

	notif := pgnotify.NewChannel(pgPool)
	t.Cleanup(func() { notif.Close() })

	token := getAdminJWT(t)
	ws := directus.NewWSClient(testDirectusURL, token)
	t.Cleanup(func() { ws.Close() })

	config := dcfg.NewCollection[UpdateItem]("e2e_ws_update")

	mgr := manager.New(store, notif, pgregistry.NewRegistry(pgPool),
		manager.Options{
			PollInterval:             time.Hour,
			WaitConfirmationsTimeout: 5 * time.Second,
			ServiceName:              "e2e-ws-update",
		},
		manager.WithCache(rediscache.NewCache(rdb, rediscache.WithTTL(5*time.Minute)), cache.ReadWriteThrough),
		manager.WithWebSocket(ws),
	)

	manager.RegisterCollection(mgr, config, items)

	mgrCtx, mgrCancel := context.WithCancel(ctx)
	defer mgrCancel()

	go mgr.Start(mgrCtx)
	time.Sleep(3 * time.Second)

	// Verify initial.
	found, ok := config.Find(func(i UpdateItem) bool { return i.Name == "target" })
	if !ok {
		t.Fatal("target not found")
	}

	if found.Value != 100 {
		t.Fatalf("initial value = %d, want 100", found.Value)
	}

	t.Log("initial OK, updating item to trigger WS event...")

	// Update the item — should trigger WS → resync.
	_, err = items.Update(ctx, "1", &UpdateItem{Name: "target", Value: 999})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Wait for the update to propagate.
	deadline := time.After(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			current, _ := config.Find(func(i UpdateItem) bool { return i.Name == "target" })
			t.Fatalf("timed out: value=%d, want 999", current.Value)
		case <-ticker.C:
			if item, ok := config.Find(func(i UpdateItem) bool { return i.Name == "target" }); ok && item.Value == 999 {
				t.Log("Update propagated via WebSocket!")
				mgrCancel()
				return
			}
		}
	}
}
