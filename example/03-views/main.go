// Auto-updating materialized views over a collection.
package main

import (
	"cmp"
	"fmt"
	"time"

	"github.com/swchck/director/config"
)

type Product struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Price    int    `json:"price"`
}

func main() {
	products := config.NewCollection[Product]("products")

	// View: cheap food items, sorted by price.
	cheapFood := config.NewView("cheap-food", products,
		[]config.FilterOption[Product]{
			config.Where(func(p Product) bool { return p.Category == "food" && p.Price < 20 }),
			config.SortBy(func(a, b Product) int { return cmp.Compare(a.Price, b.Price) }),
		},
	)

	// View: top 3 most expensive items.
	top3 := config.NewView("top-3", products,
		[]config.FilterOption[Product]{
			config.SortBy(func(a, b Product) int { return cmp.Compare(b.Price, a.Price) }),
			config.Limit[Product](3),
		},
	)

	cheapFood.OnChange(func(_, new []Product) {
		fmt.Printf("  [hook] cheap food updated: %d items\n", len(new))
	})

	// Simulate a sync from Directus.
	v1 := config.NewVersion(time.Now())
	products.Swap(v1, []Product{
		{ID: 1, Name: "Pizza", Category: "food", Price: 12},
		{ID: 2, Name: "Burger", Category: "food", Price: 8},
		{ID: 3, Name: "Sushi", Category: "food", Price: 25},
		{ID: 4, Name: "Coffee", Category: "drink", Price: 5},
		{ID: 5, Name: "Steak", Category: "food", Price: 35},
	})

	fmt.Println("After first sync:")
	fmt.Printf("  Cheap food: %d items\n", cheapFood.Count())
	for _, p := range cheapFood.All() {
		fmt.Printf("    %s ($%d)\n", p.Name, p.Price)
	}

	fmt.Printf("  Top 3: %d items\n", top3.Count())
	for _, p := range top3.All() {
		fmt.Printf("    %s ($%d)\n", p.Name, p.Price)
	}

	// Simulate another sync — views auto-recompute.
	v2 := config.NewVersion(time.Now())
	products.Swap(v2, []Product{
		{ID: 1, Name: "Pizza", Category: "food", Price: 12},
		{ID: 2, Name: "Burger", Category: "food", Price: 8},
		{ID: 6, Name: "Salad", Category: "food", Price: 10},
	})

	fmt.Println("\nAfter second sync:")
	fmt.Printf("  Cheap food: %d items\n", cheapFood.Count())
	fmt.Printf("  Top 3: %d items\n", top3.Count())
}
