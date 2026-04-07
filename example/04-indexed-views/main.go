// GroupBy views: map[K][]V from a collection.
package main

import (
	"fmt"
	"time"

	"github.com/swchck/director/config"
)

type Business struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Levels   []Level `json:"levels"`
}

type Level struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

func main() {
	businesses := config.NewCollection[Business]("businesses")

	// Group businesses by category.
	byCategory := config.NewIndexedView("by-category", businesses,
		func(b Business) string { return b.Category },
	)

	// Group + transform: extract levels per business name.
	levelsByBiz := config.NewIndexedViewT("levels-by-biz", businesses,
		func(b Business) string { return b.Name },
		func(b Business) []Level { return b.Levels },
	)

	// Simulate sync.
	businesses.Swap(config.NewVersion(time.Now()), []Business{
		{ID: 1, Name: "Pizza Place", Category: "food", Levels: []Level{
			{ID: 10, Name: "Bronze", Price: 100},
			{ID: 11, Name: "Silver", Price: 200},
		}},
		{ID: 2, Name: "Burger Joint", Category: "food", Levels: []Level{
			{ID: 12, Name: "Gold", Price: 500},
		}},
		{ID: 3, Name: "Coffee Shop", Category: "drink", Levels: []Level{
			{ID: 13, Name: "Basic", Price: 50},
		}},
	})

	// IndexedView — group by category.
	fmt.Printf("Categories: %d\n", byCategory.Count())
	for _, cat := range byCategory.Keys() {
		items := byCategory.Get(cat)
		fmt.Printf("  %s: %d businesses\n", cat, len(items))
	}

	// IndexedViewT — levels per business.
	fmt.Println()
	for _, name := range levelsByBiz.Keys() {
		levels := levelsByBiz.Get(name)
		fmt.Printf("  %s: %d levels\n", name, len(levels))
		for _, lv := range levels {
			fmt.Printf("    - %s ($%.0f)\n", lv.Name, lv.Price)
		}
	}

	// O(1) lookup.
	fmt.Printf("\nHas 'food'? %v\n", byCategory.Has("food"))
	fmt.Printf("Pizza Place levels: %d\n", levelsByBiz.CountFor("Pizza Place"))
}
