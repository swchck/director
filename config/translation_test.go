package config_test

import (
	"testing"

	"github.com/swchck/director/config"
)

type translation struct {
	Lang string
	Name string
}

func langFn(t translation) string { return t.Lang }

func TestFindTranslation(t *testing.T) {
	translations := []translation{
		{Lang: "en-US", Name: "English"},
		{Lang: "de-DE", Name: "German"},
		{Lang: "fr-FR", Name: "French"},
	}

	tr, ok := config.FindTranslation(translations, langFn, "de-DE")
	if !ok {
		t.Fatal("expected to find de-DE")
	}

	if tr.Name != "German" {
		t.Errorf("Name = %q, want 'German'", tr.Name)
	}

	_, ok = config.FindTranslation(translations, langFn, "ja-JP")
	if ok {
		t.Error("should not find ja-JP")
	}
}

func TestFindTranslation_EmptySlice(t *testing.T) {
	_, ok := config.FindTranslation([]translation{}, langFn, "en-US")
	if ok {
		t.Error("should not find anything in empty slice")
	}
}

func TestFindTranslationWithFallback(t *testing.T) {
	translations := []translation{
		{Lang: "en-US", Name: "English"},
		{Lang: "de-DE", Name: "German"},
	}

	// Primary language found.
	tr, ok := config.FindTranslationWithFallback(translations, langFn, "de-DE", "en-US")
	if !ok || tr.Name != "German" {
		t.Errorf("got %+v, ok=%v, want German", tr, ok)
	}

	// Primary not found, fallback used.
	tr, ok = config.FindTranslationWithFallback(translations, langFn, "fr-FR", "en-US")
	if !ok || tr.Name != "English" {
		t.Errorf("got %+v, ok=%v, want English (fallback)", tr, ok)
	}

	// Neither found.
	_, ok = config.FindTranslationWithFallback(translations, langFn, "ja-JP", "zh-CN")
	if ok {
		t.Error("should not find anything")
	}
}

func TestTranslationMap(t *testing.T) {
	translations := []translation{
		{Lang: "en-US", Name: "English"},
		{Lang: "de-DE", Name: "German"},
		{Lang: "fr-FR", Name: "French"},
	}

	m := config.TranslationMap(translations, langFn)

	if len(m) != 3 {
		t.Fatalf("map len = %d, want 3", len(m))
	}

	if m["en-US"].Name != "English" {
		t.Errorf("m[en-US] = %+v", m["en-US"])
	}

	if m["de-DE"].Name != "German" {
		t.Errorf("m[de-DE] = %+v", m["de-DE"])
	}
}

func TestTranslatedView_First(t *testing.T) {
	type product struct {
		ID           int
		Translations []translation
	}

	type localized struct {
		ID   int
		Name string
	}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{
		{ID: 1, Translations: []translation{{Lang: "en-US", Name: "Apple"}}},
		{ID: 2, Translations: []translation{{Lang: "en-US", Name: "Banana"}}},
	})

	enView := config.NewTranslatedView("products-en", c, func(p product) localized {
		tr, _ := config.FindTranslation(p.Translations, langFn, "en-US")
		return localized{ID: p.ID, Name: tr.Name}
	})

	first, ok := enView.First()
	if !ok {
		t.Fatal("First() returned false")
	}

	if first.ID != 1 || first.Name != "Apple" {
		t.Errorf("First() = %+v, want {ID:1, Name:Apple}", first)
	}
}

func TestTranslatedView_First_Empty(t *testing.T) {
	type product struct {
		ID int
	}

	type localized struct {
		ID int
	}

	c := config.NewCollection[product]("products")

	view := config.NewTranslatedView("empty", c, func(p product) localized {
		return localized(p)
	})

	_, ok := view.First()
	if ok {
		t.Error("First() should return false on empty collection")
	}
}

func TestTranslatedView_FindMany(t *testing.T) {
	type product struct {
		ID       int
		Category string
	}

	type localized struct {
		ID       int
		Category string
	}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{
		{ID: 1, Category: "fruit"},
		{ID: 2, Category: "vegetable"},
		{ID: 3, Category: "fruit"},
	})

	view := config.NewTranslatedView("products", c, func(p product) localized {
		return localized(p)
	})

	fruits := view.FindMany(func(l localized) bool { return l.Category == "fruit" })
	if len(fruits) != 2 {
		t.Errorf("FindMany(fruit) = %d items, want 2", len(fruits))
	}

	vegs := view.FindMany(func(l localized) bool { return l.Category == "vegetable" })
	if len(vegs) != 1 {
		t.Errorf("FindMany(vegetable) = %d items, want 1", len(vegs))
	}

	none := view.FindMany(func(l localized) bool { return l.Category == "meat" })
	if len(none) != 0 {
		t.Errorf("FindMany(meat) = %d items, want 0", len(none))
	}
}

func TestTranslatedView_Filter(t *testing.T) {
	type product struct {
		ID    int
		Price float64
	}

	type localized struct {
		ID    int
		Price float64
	}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{
		{ID: 1, Price: 10},
		{ID: 2, Price: 50},
		{ID: 3, Price: 30},
	})

	view := config.NewTranslatedView("products", c, func(p product) localized {
		return localized(p)
	})

	expensive := view.Filter(
		config.Where(func(l localized) bool { return l.Price > 20 }),
	)

	if len(expensive) != 2 {
		t.Errorf("Filter(price>20) = %d items, want 2", len(expensive))
	}
}

func TestTranslatedView_All(t *testing.T) {
	type product struct {
		ID int
	}

	type localized struct {
		ID int
	}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{{ID: 1}, {ID: 2}, {ID: 3}})

	view := config.NewTranslatedView("products", c, func(p product) localized {
		return localized(p)
	})

	all := view.All()
	if len(all) != 3 {
		t.Errorf("All() = %d items, want 3", len(all))
	}

	if all[0].ID != 1 || all[1].ID != 2 || all[2].ID != 3 {
		t.Errorf("All() = %+v, want IDs [1, 2, 3]", all)
	}
}

func TestTranslatedView_Name(t *testing.T) {
	type product struct{ ID int }
	type localized struct{ ID int }

	c := config.NewCollection[product]("products")

	view := config.NewTranslatedView("products-en", c, func(p product) localized {
		return localized(p)
	})

	if view.Name() != "products-en" {
		t.Errorf("Name() = %q, want 'products-en'", view.Name())
	}
}

func TestTranslatedView_TransformsAndAutoUpdates(t *testing.T) {
	type product struct {
		ID           int
		Translations []translation
	}

	type localized struct {
		ID   int
		Name string
	}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{
		{ID: 1, Translations: []translation{{Lang: "en-US", Name: "Apple"}, {Lang: "de-DE", Name: "Apfel"}}},
		{ID: 2, Translations: []translation{{Lang: "en-US", Name: "Banana"}}},
	})

	enView := config.NewTranslatedView("products-en", c, func(p product) localized {
		tr, _ := config.FindTranslation(p.Translations, langFn, "en-US")
		return localized{ID: p.ID, Name: tr.Name}
	})

	if enView.Count() != 2 {
		t.Fatalf("Count() = %d, want 2", enView.Count())
	}

	found, ok := enView.Find(func(l localized) bool { return l.ID == 1 })
	if !ok || found.Name != "Apple" {
		t.Errorf("Find(1) = %+v, ok=%v", found, ok)
	}

	// Update source — translated view should recompute.
	_ = c.Swap(v2(), []product{
		{ID: 1, Translations: []translation{{Lang: "en-US", Name: "Green Apple"}}},
	})

	if enView.Count() != 1 {
		t.Errorf("after swap: Count() = %d, want 1", enView.Count())
	}

	found, ok = enView.Find(func(l localized) bool { return l.ID == 1 })
	if !ok || found.Name != "Green Apple" {
		t.Errorf("after swap: Find(1) = %+v, ok=%v", found, ok)
	}
}

func TestTranslatedView_OnChange(t *testing.T) {
	type product struct {
		ID    int
		Price float64
	}
	type localized struct {
		ID    int
		Price float64
	}

	c := config.NewCollection[product]("prods")
	_ = c.Swap(v1(), []product{{ID: 1, Price: 10}})

	tv := config.NewTranslatedView("local", c, func(p product) localized {
		return localized(p)
	})

	var called bool
	var newCount int
	tv.OnChange(func(_, newItems []localized) {
		called = true
		newCount = len(newItems)
	})

	_ = c.Swap(v2(), []product{{ID: 1, Price: 10}, {ID: 2, Price: 20}})

	if !called {
		t.Fatal("OnChange was not called")
	}
	if newCount != 2 {
		t.Errorf("new count = %d, want 2", newCount)
	}
}
