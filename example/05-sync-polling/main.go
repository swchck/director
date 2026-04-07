// Poll-based sync from Directus.
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
		Collection: "poll_demo",
		Fields:     []directus.FieldInput{directus.PrimaryKeyField("id"), directus.DateCreatedField(), directus.DateUpdatedField(), directus.StringField("name")},
	})

	items := config.NewCollection[Item]("poll_demo")
	items.OnChange(func(old, new []Item) {
		fmt.Printf("Synced: %d → %d items\n", len(old), len(new))
	})

	mgr := manager.New(store, notif, pgregistry.NewRegistry(pgPool), manager.Options{
		PollInterval: 30 * time.Second, // Check every 30s.
		ServiceName:  "example-05",
	}, manager.WithLogger(logger))

	manager.RegisterCollection(mgr, items, directus.NewItems[Item](dc, "poll_demo"))

	go mgr.Start(ctx)
	time.Sleep(3 * time.Second)

	fmt.Printf("Initial: %d items. Waiting for poll cycles...\n", items.Count())
	fmt.Println("Create items in Directus UI and watch them appear here.")
	fmt.Println("Press Ctrl+C to stop.")

	<-ctx.Done()
}
