// Real-time sync from Directus via WebSocket.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/manager"
	pgnotify "github.com/swchck/director/notify/postgres"
	pgregistry "github.com/swchck/director/registry/postgres"
	pgstorage "github.com/swchck/director/storage/postgres"
)

type Item struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	logger := dlog.NewSlog(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dc := directus.NewClient("http://localhost:8055", "e2e-test-token", directus.WithLogger(logger))
	pgPool, _ := pgxpool.New(ctx, "postgres://directus:directus@localhost:5433/directus?sslmode=disable")
	defer pgPool.Close()

	store := pgstorage.NewStorage(pgPool)
	store.Migrate(ctx)
	notif := pgnotify.NewChannel(pgPool)
	defer notif.Close()

	// Ensure collection exists.
	dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "ws_demo",
		Fields:     []directus.FieldInput{directus.PrimaryKeyField("id"), directus.DateCreatedField(), directus.DateUpdatedField(), directus.StringField("name")},
	})

	// WebSocket client for real-time events.
	ws := directus.NewWSClient("http://localhost:8055", "e2e-test-token", directus.WithWSLogger(logger))
	defer ws.Close()

	items := config.NewCollection[Item]("ws_demo")
	items.OnChange(func(old, new []Item) {
		fmt.Printf("⚡ Live update: %d → %d items\n", len(old), len(new))
		for _, item := range new {
			fmt.Printf("   %d: %s\n", item.ID, item.Name)
		}
	})

	mgr := manager.New(store, notif, pgregistry.NewRegistry(pgPool), manager.Options{
		PollInterval: time.Hour, // Long — we rely on WebSocket.
		ServiceName:  "example-06",
	},
		manager.WithLogger(logger),
		manager.WithWebSocket(ws), // Real-time sync.
	)

	manager.RegisterCollection(mgr, items, directus.NewItems[Item](dc, "ws_demo"))

	go mgr.Start(ctx)
	time.Sleep(3 * time.Second)

	fmt.Printf("Initial: %d items\n", items.Count())
	fmt.Println("Create/edit items in Directus UI — updates appear instantly.")
	fmt.Println("Press Ctrl+C to stop.")

	<-ctx.Done()
}
