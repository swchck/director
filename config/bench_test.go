package config_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/swchck/director/config"
)

// benchItem is a realistic struct for benchmarking.
type benchItem struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Price    float64 `json:"price"`
	Active   bool    `json:"active"`
}

func makeBenchItems(n int) []benchItem {
	categories := []string{"electronics", "food", "clothing", "books", "sports"}
	items := make([]benchItem, n)
	for i := range items {
		items[i] = benchItem{
			ID:       i + 1,
			Name:     fmt.Sprintf("item-%d", i+1),
			Category: categories[i%len(categories)],
			Price:    float64(i%1000) + 0.99,
			Active:   i%3 != 0,
		}
	}

	return items
}

func benchVersion() config.Version {
	return config.NewVersion(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
}

// --- Collection benchmarks ---

func BenchmarkCollection_All(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			_ = c.Swap(benchVersion(), makeBenchItems(size))

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_ = c.All()
			}
		})
	}
}

func BenchmarkCollection_Count(b *testing.B) {
	c := config.NewCollection[benchItem]("bench")
	_ = c.Swap(benchVersion(), makeBenchItems(10_000))

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = c.Count()
	}
}

func BenchmarkCollection_Find(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			_ = c.Swap(benchVersion(), makeBenchItems(size))
			targetID := size / 2 // middle element — worst average case

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_, _ = c.Find(func(i benchItem) bool { return i.ID == targetID })
			}
		})
	}
}

func BenchmarkCollection_Filter(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			_ = c.Swap(benchVersion(), makeBenchItems(size))

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_ = c.Filter(
					config.Where(func(i benchItem) bool { return i.Active }),
					config.Limit[benchItem](50),
				)
			}
		})
	}
}

func BenchmarkCollection_Swap(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			items := makeBenchItems(size)

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_ = c.Swap(benchVersion(), items)
			}
		})
	}
}

func BenchmarkCollection_Swap_WithHooks(b *testing.B) {
	for _, numHooks := range []int{1, 5, 10} {
		b.Run(fmt.Sprintf("hooks=%d", numHooks), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			items := makeBenchItems(1_000)

			for range numHooks {
				c.OnChange(func(_, _ []benchItem) {
					// no-op hook
				})
			}

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_ = c.Swap(benchVersion(), items)
			}
		})
	}
}

// --- View benchmarks ---

func BenchmarkView_All(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			_ = c.Swap(benchVersion(), makeBenchItems(size))

			view := config.NewView("filtered", c,
				[]config.FilterOption[benchItem]{
					config.Where(func(i benchItem) bool { return i.Active }),
				},
			)

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_ = view.All()
			}
		})
	}
}

func BenchmarkView_Recompute(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			items := makeBenchItems(size)

			_ = config.NewView("filtered-sorted", c,
				[]config.FilterOption[benchItem]{
					config.Where(func(i benchItem) bool { return i.Active }),
					config.SortBy(func(a, b benchItem) int {
						if a.Price < b.Price {
							return -1
						}
						if a.Price > b.Price {
							return 1
						}
						return 0
					}),
					config.Limit[benchItem](100),
				},
			)

			b.ResetTimer()
			b.ReportAllocs()
			// Each Swap triggers view recompute via OnChange.
			for b.Loop() {
				_ = c.Swap(benchVersion(), items)
			}
		})
	}
}

// --- IndexedView benchmarks ---

func BenchmarkIndexedView_Get(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			_ = c.Swap(benchVersion(), makeBenchItems(size))

			idx := config.NewIndexedView("by-category", c,
				func(i benchItem) string { return i.Category },
			)

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_ = idx.Get("electronics")
			}
		})
	}
}

func BenchmarkIndexedView_Recompute(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			c := config.NewCollection[benchItem]("bench")
			items := makeBenchItems(size)

			_ = config.NewIndexedView("by-category", c,
				func(i benchItem) string { return i.Category },
			)

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_ = c.Swap(benchVersion(), items)
			}
		})
	}
}

// --- Concurrent read benchmarks ---

func BenchmarkCollection_All_Parallel(b *testing.B) {
	c := config.NewCollection[benchItem]("bench")
	_ = c.Swap(benchVersion(), makeBenchItems(10_000))

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = c.All()
		}
	})
}

func BenchmarkIndexedView_Get_Parallel(b *testing.B) {
	c := config.NewCollection[benchItem]("bench")
	_ = c.Swap(benchVersion(), makeBenchItems(10_000))

	idx := config.NewIndexedView("by-category", c,
		func(i benchItem) string { return i.Category },
	)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = idx.Get("electronics")
		}
	})
}
