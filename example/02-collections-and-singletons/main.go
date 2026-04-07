// In-memory Collection[T] and Singleton[T] synced from Directus.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
	pgnotify "github.com/swchck/director/notify/postgres"
	pgregistry "github.com/swchck/director/registry/postgres"
	pgstorage "github.com/swchck/director/storage/postgres"
)

type Product struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Price    int    `json:"price"`
}

type Settings struct {
	ID         int  `json:"id"`
	MaxPlayers int  `json:"max_players"`
	Debug      bool `json:"debug"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dc := directus.NewClient(envOr("DIRECTUS_URL", "http://localhost:8055"), envOr("DIRECTUS_TOKEN", "e2e-test-token"))
	pgPool, _ := pgxpool.New(ctx, envOr("DATABASE_URL", "postgres://directus:directus@localhost:5433/directus?sslmode=disable"))
	defer pgPool.Close()

	store := pgstorage.NewStorage(pgPool)
	store.Migrate(ctx)
	notif := pgnotify.NewChannel(pgPool)
	defer notif.Close()

	// Define in-memory configs.
	products := config.NewCollection[Product]("products")
	settings := config.NewSingleton[Settings]("game_settings")

	// React to changes.
	products.OnChange(func(old, new []Product) {
		fmt.Printf("Products changed: %d → %d items\n", len(old), len(new))
	})

	// Set up manager.
	mgr := manager.New(store, notif, pgregistry.NewRegistry(pgPool), manager.Options{
		PollInterval: 5 * time.Minute,
		ServiceName:  "example-02",
	})

	manager.RegisterCollection(mgr, products, directus.NewItems[Product](dc, "products"))
	manager.RegisterSingleton(mgr, settings, directus.NewSingleton[Settings](dc, "game_settings"))

	go mgr.Start(ctx)
	time.Sleep(3 * time.Second)

	// Query — lock-free, instant.
	fmt.Printf("Products: %d\n", products.Count())
	if p, ok := products.First(); ok {
		fmt.Printf("First: %s ($%d)\n", p.Name, p.Price)
	}

	cheap := products.FindMany(func(p Product) bool { return p.Price < 20 })
	fmt.Printf("Cheap products: %d\n", len(cheap))

	if s, ok := settings.Get(); ok {
		fmt.Printf("Settings: max_players=%d debug=%v\n", s.MaxPlayers, s.Debug)
	}

	<-ctx.Done()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
