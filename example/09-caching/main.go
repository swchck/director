// Caching strategies and view persistence.
package main

import (
	"cmp"
	"fmt"
	"time"

	"github.com/swchck/director/cache"
	memcache "github.com/swchck/director/cache/memory"
	"github.com/swchck/director/config"
)

type Product struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Price    int    `json:"price"`
}

func main() {
	// --- Cache strategies explained ---
	fmt.Println("Cache strategies:")
	for _, s := range []cache.Strategy{cache.ReadThrough, cache.WriteThrough, cache.WriteBehind, cache.ReadWriteThrough} {
		fmt.Printf("  %-20s reads=%v writes=%v async=%v\n", s, s.ReadsFromCache(), s.WritesToCache(), s.IsAsync())
	}
	fmt.Println()

	// --- MemoryViewStore: in-process view persistence ---
	memStore := memcache.NewViewStore()
	products := config.NewCollection[Product]("products")

	// View with in-memory persistence — survives view recreation within the process.
	cheapView := config.NewView("cheap", products,
		[]config.FilterOption[Product]{
			config.Where(func(p Product) bool { return p.Price < 20 }),
			config.SortBy(func(a, b Product) int { return cmp.Compare(a.Price, b.Price) }),
		},
		config.WithPersistence[Product](memStore),
	)

	// Indexed view with persistence.
	byCategory := config.NewIndexedView("by-cat", products,
		func(p Product) string { return p.Category },
		config.WithIndexPersistence[Product, string](memStore),
	)

	// Simulate sync.
	products.Swap(config.NewVersion(time.Now()), []Product{
		{ID: 1, Name: "Pizza", Category: "food", Price: 12},
		{ID: 2, Name: "Steak", Category: "food", Price: 35},
		{ID: 3, Name: "Coffee", Category: "drink", Price: 5},
	})

	fmt.Printf("Cheap products: %d\n", cheapView.Count())
	fmt.Printf("Categories: %v\n", byCategory.Keys())

	// The MemoryViewStore now holds the precomputed results.
	// If you create a NEW view with the same name and store, it loads from cache first.
	cheapView2 := config.NewView("cheap", config.NewCollection[Product]("empty"),
		[]config.FilterOption[Product]{},
		config.WithPersistence[Product](memStore),
	)

	fmt.Printf("Warm-started view: %d items (loaded from MemoryViewStore)\n", cheapView2.Count())

	// --- Redis persistence (code only, requires running Redis) ---
	//
	// import rediscache "github.com/swchck/director/cache/redis"
	// import "github.com/redis/go-redis/v9"
	//
	// rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	// redisStore := rediscache.NewViewStore(rdb, rediscache.WithViewTTL(10*time.Minute))
	//
	// view := config.NewView("food-sorted", products, filters,
	//     config.WithPersistence[Product](redisStore),
	// )
	//
	// This persists view results to Redis — other replicas can warm-start from it.
}
