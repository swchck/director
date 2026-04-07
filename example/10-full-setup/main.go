// Full setup example — shows how to wire all components manually.
//
// This is the recommended pattern for production use: you control every
// dependency and can swap any implementation (storage, notifier, registry).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swchck/director/directus"
	"github.com/swchck/director/example/10-full-setup/configs/products"
	"github.com/swchck/director/example/10-full-setup/configs/settings"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/manager"
	pgnotify "github.com/swchck/director/notify/postgres"
	pgregistry "github.com/swchck/director/registry/postgres"
	pgstorage "github.com/swchck/director/storage/postgres"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	logger := dlog.NewSlog(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// --- Infrastructure ---

	pgPool, err := pgxpool.New(ctx, envOr("DATABASE_URL", "postgres://directus:directus@localhost:5433/directus?sslmode=disable"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg: %v\n", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	store := pgstorage.NewStorage(pgPool)
	if err := store.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	notif := pgnotify.NewChannel(pgPool, pgnotify.WithLogger(logger))
	defer notif.Close()

	reg := pgregistry.NewRegistry(pgPool)

	// --- Directus client + WebSocket ---

	dc := directus.NewClient(
		envOr("DIRECTUS_URL", "http://localhost:8055"),
		envOr("DIRECTUS_TOKEN", "e2e-test-token"),
		directus.WithLogger(logger),
	)

	ws := directus.NewWSClient(
		envOr("DIRECTUS_URL", "http://localhost:8055"),
		envOr("DIRECTUS_TOKEN", "e2e-test-token"),
		directus.WithWSLogger(logger),
	)

	// --- Ensure demo data (idempotent) ---

	ensureProducts(ctx, dc, logger)
	ensureSettings(ctx, dc, logger)

	// --- Config units ---

	prods := products.Config(dc)
	appSettings := settings.Config(dc)

	// --- Manager ---

	mgr := manager.New(store, notif, reg,
		manager.Options{
			PollInterval: 30 * time.Second,
			ServiceName:  "full-setup-example",
		},
		manager.WithLogger(logger),
		manager.WithWebSocket(ws),
	)

	prods.Register(mgr)
	appSettings.Register(mgr)

	// --- React to changes ---

	prods.OnChange(func(old, new []products.Product) {
		fmt.Printf("\n[products] %d → %d items\n", len(old), len(new))
		for cat, items := range prods.ByCategory.All() {
			fmt.Printf("  %s: %d\n", cat, len(items))
		}
	})

	appSettings.OnChange(func(old, new *settings.AppSettings) {
		fmt.Printf("\n[settings] maintenance=%v, max_players=%d, tick_rate=%.1f\n",
			new.MaintenanceMode, new.MaxPlayers, new.TickRate)
	})

	// --- Start ---

	go func() {
		if err := mgr.Start(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "manager: %v\n", err)
		}
	}()

	time.Sleep(2 * time.Second)

	fmt.Printf("Products: %d total, %d expensive, %d active\n",
		prods.All.Count(), prods.Expensive.Count(), prods.Active.Count())

	if s, ok := appSettings.All.Get(); ok {
		fmt.Printf("Settings: max_players=%d, tick_rate=%.1f, maintenance=%v\n",
			s.MaxPlayers, s.TickRate, s.MaintenanceMode)
	}

	fmt.Println("\nPress Ctrl+C to exit (try editing in Directus UI)")
	<-ctx.Done()
	mgr.Stop()
}

func ensureProducts(ctx context.Context, dc *directus.Client, logger dlog.Logger) {
	dc.GrantAdminAccess(ctx)
	dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "products",
		Meta:       &directus.CollectionMeta{Icon: "inventory_2"},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateCreatedField(),
			directus.DateUpdatedField(),
			directus.StatusField(),
			directus.StringField("name"),
			directus.StringField("category"),
			directus.FloatField("price"),
		},
	})

	type p struct {
		Name     string  `json:"name"`
		Category string  `json:"category"`
		Price    float64 `json:"price"`
		Status   string  `json:"status"`
	}
	items := directus.NewItems[p](dc, "products")
	if existing, _ := items.List(ctx, directus.WithLimit(1)); len(existing) > 0 {
		return
	}
	logger.Info("seeding products...")
	for _, s := range []p{
		{Name: "Laptop", Category: "electronics", Price: 999, Status: "published"},
		{Name: "Phone", Category: "electronics", Price: 699, Status: "published"},
		{Name: "T-Shirt", Category: "clothing", Price: 29, Status: "published"},
		{Name: "Jacket", Category: "clothing", Price: 149, Status: "published"},
		{Name: "Coffee Mug", Category: "home", Price: 12, Status: "published"},
		{Name: "Desk Lamp", Category: "home", Price: 89, Status: "draft"},
	} {
		items.Create(ctx, &s)
	}
}

func ensureSettings(ctx context.Context, dc *directus.Client, logger dlog.Logger) {
	dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "app_settings",
		Meta:       &directus.CollectionMeta{Singleton: true, Icon: "settings"},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateCreatedField(),
			directus.DateUpdatedField(),
			directus.IntegerField("max_players"),
			directus.FloatField("tick_rate"),
			directus.BooleanField("maintenance_mode"),
		},
	})

	type s struct {
		MaxPlayers      int     `json:"max_players"`
		TickRate        float64 `json:"tick_rate"`
		MaintenanceMode bool    `json:"maintenance_mode"`
	}
	singleton := directus.NewSingleton[s](dc, "app_settings")
	if cur, _ := singleton.Get(ctx); cur != nil && cur.MaxPlayers > 0 {
		return
	}
	logger.Info("seeding settings...")
	singleton.Update(ctx, &s{MaxPlayers: 100, TickRate: 60.0})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
