// Package products is a self-contained config unit for the "products" collection.
// It encapsulates the collection, data source, and all derived views.
package products

import (
	"cmp"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/source"
)

// Product is the Directus item type for the "products" collection.
type Product struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Price    float64 `json:"price"`
	Status   string  `json:"status"`
}

// Products holds the collection and all derived views as public read-only fields.
type Products struct {
	// col is unexported — it has Swap() which consumers should not call.
	col *config.Collection[Product]

	// All exposes the collection as read-only.
	All config.ReadableCollection[Product]

	// ByCategory groups products by category for O(1) lookup.
	ByCategory *config.IndexedView[Product, string]

	// Expensive shows the top 20 most expensive products.
	Expensive *config.View[Product]

	// Active shows only published products.
	Active *config.View[Product]

	src source.CollectionSource[Product]
}

// Config creates a Products config unit sourced from Directus.
func Config(dc *directus.Client) *Products {
	col := config.NewCollection[Product]("products")
	return &Products{
		col: col,
		All: col,
		src: source.FromDirectus(directus.NewItems[Product](dc, "products")),
	}
}

// OnChange registers a callback that fires when the products collection updates.
func (p *Products) OnChange(fn func(old, new []Product)) {
	p.col.OnChange(fn)
}

// Register registers the data source and creates all views.
func (p *Products) Register(m *manager.Manager) {
	manager.RegisterCollectionSource(m, p.col, p.src)

	p.ByCategory = config.NewIndexedView("products:by-category", p.col,
		func(prod Product) string { return prod.Category },
	)

	p.Expensive = config.NewView("products:expensive", p.col,
		[]config.FilterOption[Product]{
			config.Where(func(prod Product) bool { return prod.Price > 100 }),
			config.SortBy(func(a, b Product) int { return cmp.Compare(b.Price, a.Price) }),
			config.Limit[Product](20),
		},
	)

	p.Active = config.NewView("products:active", p.col,
		[]config.FilterOption[Product]{
			config.Where(func(prod Product) bool { return prod.Status == "published" }),
		},
	)
}
