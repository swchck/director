package config_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

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

func TestTranslatedView_Close_StopsRecomputing(t *testing.T) {
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
	})

	enView := config.NewTranslatedView("products-en", c, func(p product) localized {
		tr, _ := config.FindTranslation(p.Translations, langFn, "en-US")
		return localized{ID: p.ID, Name: tr.Name}
	})

	if enView.Count() != 1 {
		t.Fatalf("initial Count() = %d, want 1", enView.Count())
	}

	enView.Close()

	_ = c.Swap(v2(), []product{
		{ID: 1, Translations: []translation{{Lang: "en-US", Name: "Apple"}}},
		{ID: 2, Translations: []translation{{Lang: "en-US", Name: "Banana"}}},
	})

	if enView.Count() != 1 {
		t.Errorf("Count() after Close = %d, want 1 (should not recompute)", enView.Count())
	}
}

func TestTranslatedView_WithPersistence_SavesOnRecompute(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	store := &mockPersistence{data: make(map[string][]byte)}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{{ID: 1, Name: "Apple"}})

	_ = config.NewTranslatedView("products-loc", c,
		func(p product) localized { return localized(p) },
		config.WithTranslatedViewPersistence[product, localized](store),
	)

	// Allow async persistence goroutine to complete.
	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	saved, ok := store.data["products-loc"]
	store.mu.Unlock()

	if !ok || len(saved) == 0 {
		t.Error("expected persistence Save to be called with data")
	}
}

func TestTranslatedView_WithPersistence_SavesOnSwap(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	store := &mockPersistence{data: make(map[string][]byte)}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{{ID: 1, Name: "Apple"}})

	_ = config.NewTranslatedView("products-loc", c,
		func(p product) localized { return localized(p) },
		config.WithTranslatedViewPersistence[product, localized](store),
	)

	time.Sleep(50 * time.Millisecond)

	// Swap triggers recompute which triggers save.
	_ = c.Swap(v2(), []product{{ID: 1, Name: "Apple"}, {ID: 2, Name: "Banana"}})

	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	saved := store.data["products-loc"]
	store.mu.Unlock()

	// Should contain 2 serialized items.
	if len(saved) == 0 {
		t.Fatal("expected persistence Save to be called after swap")
	}

	var items []localized
	if err := json.Unmarshal(saved, &items); err != nil {
		t.Fatalf("unmarshal saved data: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("saved %d items, want 2", len(items))
	}
}

func TestTranslatedView_Version_MatchesSource(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	c := config.NewCollection[product]("products")
	ver := v1()
	_ = c.Swap(ver, []product{{ID: 1, Name: "Apple"}})

	tv := config.NewTranslatedView("products-loc", c, func(p product) localized {
		return localized(p)
	})

	if tv.Version() != ver {
		t.Errorf("Version() = %v, want %v", tv.Version(), ver)
	}

	ver2 := v2()
	_ = c.Swap(ver2, []product{{ID: 1, Name: "Apple"}, {ID: 2, Name: "Banana"}})

	if tv.Version() != ver2 {
		t.Errorf("Version() after swap = %v, want %v", tv.Version(), ver2)
	}
}

func TestTranslatedView_WithPersistence_WarmStart(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	store := &mockPersistence{data: make(map[string][]byte)}

	// Pre-populate persistence with cached data.
	cached, _ := json.Marshal([]localized{{ID: 99, Name: "Cached"}})
	store.data["products-loc"] = cached

	// Source is empty — but persistence has data.
	c := config.NewCollection[product]("products")

	tv := config.NewTranslatedView("products-loc", c,
		func(p product) localized { return localized(p) },
		config.WithTranslatedViewPersistence[product, localized](store),
	)

	// After constructor: source is empty → transform produces empty → overwrites warm start.
	// This is expected: warm start is only useful until the first real sync.
	// The warm start data is visible between loadFromPersistence and the initial compute.
	// In production, source.All() returns real data, so the warm start is overwritten.
	if tv.Count() != 0 {
		t.Errorf("Count() = %d, want 0 (source is empty, transform overwrites warm start)", tv.Count())
	}
}

func TestTranslatedView_WithPersistence_WarmStartWithData(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	store := &mockPersistence{data: make(map[string][]byte)}

	// Pre-populate persistence.
	cached, _ := json.Marshal([]localized{{ID: 1, Name: "Old"}})
	store.data["products-loc"] = cached

	// Source has data — persistence value should be overwritten by transform.
	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{{ID: 1, Name: "Fresh"}})

	tv := config.NewTranslatedView("products-loc", c,
		func(p product) localized { return localized(p) },
		config.WithTranslatedViewPersistence[product, localized](store),
	)

	if tv.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", tv.Count())
	}

	item, ok := tv.First()
	if !ok || item.Name != "Fresh" {
		t.Errorf("First() = %+v, want {ID:1, Name:Fresh}", item)
	}
}

func TestTranslatedView_WithErrorHandler_OnPersistFailure(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	var mu sync.Mutex
	var capturedErr error

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{{ID: 1, Name: "Apple"}})

	_ = config.NewTranslatedView("tv-err", c,
		func(p product) localized { return localized(p) },
		config.WithTranslatedViewPersistence[product, localized](&failingPersistence{}),
		config.WithTranslatedViewErrorHandler[product, localized](func(_ string, err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		}),
	)

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if capturedErr == nil {
		t.Error("expected error handler to be called")
	}
}

func TestTranslatedView_Close_StopsPersistence(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	store := &mockPersistence{data: make(map[string][]byte)}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{{ID: 1, Name: "Apple"}})

	tv := config.NewTranslatedView("products-loc", c,
		func(p product) localized { return localized(p) },
		config.WithTranslatedViewPersistence[product, localized](store),
	)

	// Wait for initial persistence write.
	time.Sleep(50 * time.Millisecond)

	tv.Close()

	// Clear the store to detect new writes.
	store.mu.Lock()
	delete(store.data, "products-loc")
	store.mu.Unlock()

	// Swap source after Close — should NOT trigger persistence.
	_ = c.Swap(v2(), []product{{ID: 1, Name: "Apple"}, {ID: 2, Name: "Banana"}})

	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	_, saved := store.data["products-loc"]
	store.mu.Unlock()

	if saved {
		t.Error("persistence Save should not be called after Close")
	}
}

func TestTranslatedView_WithPersistenceTimeout(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	sp := newSlowPersistence()

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{{ID: 1, Name: "Apple"}})

	_ = config.NewTranslatedView("tv-timeout", c,
		func(p product) localized { return localized(p) },
		config.WithTranslatedViewPersistence[product, localized](sp),
		config.WithTranslatedViewPersistenceTimeout[product, localized](10*time.Millisecond),
	)

	// slowPersistence blocks in Save until context is cancelled.
	// With a 10ms timeout, Save should receive a context.DeadlineExceeded.
	select {
	case err := <-sp.saveErr:
		if err == nil {
			t.Error("expected non-nil error from slow Save")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for persistence timeout to fire")
	}
}
