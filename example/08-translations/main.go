// Working with Directus translations in memory.
package main

import (
	"fmt"
	"time"

	"github.com/swchck/director/config"
)

type Product struct {
	ID           int                  `json:"id"`
	SKU          string               `json:"sku"`
	Translations []ProductTranslation `json:"translations"`
}

type ProductTranslation struct {
	LanguagesCode string `json:"languages_code"`
	Name          string `json:"name"`
	Description   string `json:"description"`
}

type LocalizedProduct struct {
	ID          int
	SKU         string
	Name        string
	Description string
}

func langCode(t ProductTranslation) string { return t.LanguagesCode }

func main() {
	products := config.NewCollection[Product]("products")

	// Simulate sync with translation data.
	products.Swap(config.NewVersion(time.Now()), []Product{
		{
			ID: 1, SKU: "PIZZA-01",
			Translations: []ProductTranslation{
				{LanguagesCode: "en-US", Name: "Pizza", Description: "Delicious pizza"},
				{LanguagesCode: "de-DE", Name: "Pizza", Description: "Leckere Pizza"},
			},
		},
		{
			ID: 2, SKU: "SUSHI-01",
			Translations: []ProductTranslation{
				{LanguagesCode: "en-US", Name: "Sushi", Description: "Fresh sushi"},
			},
		},
	})

	// 1. Find a single translation.
	p := products.All()[0]
	tr, ok := config.FindTranslation(p.Translations, langCode, "en-US")
	if ok {
		fmt.Printf("Product 1 (EN): %s — %s\n", tr.Name, tr.Description)
	}

	// 2. Fallback chain: try German, fall back to English.
	tr, ok = config.FindTranslationWithFallback(p.Translations, langCode, "fr-FR", "de-DE", "en-US")
	if ok {
		fmt.Printf("Product 1 (fallback): %s — %s\n", tr.Name, tr.Description)
	}

	// 3. Translation map for O(1) lookup.
	trMap := config.TranslationMap(p.Translations, langCode)
	fmt.Printf("Languages available: %d\n", len(trMap))
	for lang, t := range trMap {
		fmt.Printf("  %s: %s\n", lang, t.Name)
	}

	// 4. TranslatedView — flattened per-language view that auto-updates.
	enProducts := config.NewTranslatedView("products-en", products,
		func(p Product) LocalizedProduct {
			tr, _ := config.FindTranslationWithFallback(p.Translations, langCode, "en-US")
			return LocalizedProduct{ID: p.ID, SKU: p.SKU, Name: tr.Name, Description: tr.Description}
		},
	)

	fmt.Printf("\nEnglish product catalog: %d items\n", enProducts.Count())
	for _, lp := range enProducts.All() {
		fmt.Printf("  [%s] %s: %s\n", lp.SKU, lp.Name, lp.Description)
	}
}
