package config_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/swchck/director/config"
)

type articleWithTag struct {
	ID       int
	Name     string
	Category string
	Tags     []tag
}

func TestIndexedView_GroupByCategory(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Category: "food", Tags: []tag{{ID: 1, Priority: 10}}},
		{ID: 2, Name: "Burger", Category: "food", Tags: []tag{{ID: 2, Priority: 20}}},
		{ID: 3, Name: "Coffee", Category: "drink", Tags: []tag{{ID: 3, Priority: 5}}},
		{ID: 4, Name: "Sushi", Category: "food", Tags: []tag{{ID: 4, Priority: 30}}},
	})

	byCategory := config.NewIndexedView("by-cat", c,
		func(b articleWithTag) string { return b.Category },
	)

	// Check counts.
	if byCategory.Count() != 2 {
		t.Errorf("Count() = %d, want 2 (food, drink)", byCategory.Count())
	}

	if byCategory.CountFor("food") != 3 {
		t.Errorf("CountFor(food) = %d, want 3", byCategory.CountFor("food"))
	}

	if byCategory.CountFor("drink") != 1 {
		t.Errorf("CountFor(drink) = %d, want 1", byCategory.CountFor("drink"))
	}

	// Get items by key.
	food := byCategory.Get("food")
	if len(food) != 3 {
		t.Fatalf("Get(food) = %d items, want 3", len(food))
	}

	names := make(map[string]bool)
	for _, b := range food {
		names[b.Name] = true
	}

	if !names["Pizza"] || !names["Burger"] || !names["Sushi"] {
		t.Errorf("food names = %v", names)
	}

	// Non-existent key.
	if byCategory.Get("retail") != nil {
		t.Error("Get(retail) should be nil")
	}

	// Keys.
	keys := byCategory.Keys()
	if len(keys) != 2 {
		t.Errorf("Keys() = %v, want 2 keys", keys)
	}

	// All.
	all := byCategory.All()
	if len(all) != 2 {
		t.Errorf("All() has %d keys, want 2", len(all))
	}
}

func TestIndexedView_RecomputesOnSwap(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Category: "food"},
	})

	byCategory := config.NewIndexedView("by-cat", c,
		func(b articleWithTag) string { return b.Category },
	)

	if byCategory.Count() != 1 {
		t.Fatalf("initial: Count() = %d, want 1", byCategory.Count())
	}

	// Swap — add more items.
	_ = c.Swap(v2(), []articleWithTag{
		{ID: 1, Name: "Pizza", Category: "food"},
		{ID: 2, Name: "Coffee", Category: "drink"},
		{ID: 3, Name: "Bar", Category: "drink"},
	})

	if byCategory.Count() != 2 {
		t.Errorf("after swap: Count() = %d, want 2", byCategory.Count())
	}

	if byCategory.CountFor("drink") != 2 {
		t.Errorf("after swap: CountFor(drink) = %d, want 2", byCategory.CountFor("drink"))
	}
}

func TestIndexedViewT_GroupAndTransform(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Category: "food", Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
		{ID: 2, Name: "Burger", Category: "food", Tags: []tag{{ID: 12, Priority: 50}}},
		{ID: 3, Name: "Coffee", Category: "drink", Tags: []tag{{ID: 13, Priority: 10}}},
	})

	// Group by name, extract tags.
	tagsByArticle := config.NewIndexedViewT("tags-by-article", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
	)

	pizzaTags := tagsByArticle.Get("Pizza")
	if len(pizzaTags) != 2 {
		t.Fatalf("Pizza tags = %d, want 2", len(pizzaTags))
	}

	if pizzaTags[0].Priority != 100 || pizzaTags[1].Priority != 200 {
		t.Errorf("Pizza tags = %+v", pizzaTags)
	}

	coffeeTags := tagsByArticle.Get("Coffee")
	if len(coffeeTags) != 1 || coffeeTags[0].Priority != 10 {
		t.Errorf("Coffee tags = %+v", coffeeTags)
	}

	if tagsByArticle.Count() != 3 {
		t.Errorf("Count() = %d, want 3", tagsByArticle.Count())
	}

	// Group by category, extract tags (multiple articles contribute).
	tagsByCat := config.NewIndexedViewT("tags-by-cat", c,
		func(b articleWithTag) string { return b.Category },
		func(b articleWithTag) []tag { return b.Tags },
	)

	foodTags := tagsByCat.Get("food")
	if len(foodTags) != 3 {
		t.Errorf("food tags = %d, want 3 (Pizza:2 + Burger:1)", len(foodTags))
	}
}

func TestIndexedViewT_RecomputesOnSwap(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10, Priority: 100}}},
	})

	tagsByArticle := config.NewIndexedViewT("tags", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
	)

	if tagsByArticle.CountFor("Pizza") != 1 {
		t.Fatalf("initial: Pizza = %d tags", tagsByArticle.CountFor("Pizza"))
	}

	_ = c.Swap(v2(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}, {ID: 12, Priority: 300}}},
	})

	if tagsByArticle.CountFor("Pizza") != 3 {
		t.Errorf("after swap: Pizza = %d tags, want 3", tagsByArticle.CountFor("Pizza"))
	}
}

func TestIndexedView_Has(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Category: "food"},
		{ID: 2, Category: "drink"},
	})

	byCategory := config.NewIndexedView("by-cat", c,
		func(b articleWithTag) string { return b.Category },
	)

	if !byCategory.Has("food") {
		t.Error("Has(food) = false, want true")
	}

	if !byCategory.Has("drink") {
		t.Error("Has(drink) = false, want true")
	}

	if byCategory.Has("retail") {
		t.Error("Has(retail) = true, want false")
	}
}

func TestIndexedView_Has_EmptyCollection(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")

	byCategory := config.NewIndexedView("by-cat", c,
		func(b articleWithTag) string { return b.Category },
	)

	if byCategory.Has("food") {
		t.Error("Has(food) on empty should be false")
	}
}

func TestIndexedViewT_Has(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10, Priority: 100}}},
		{ID: 2, Name: "Burger", Tags: []tag{{ID: 20, Priority: 50}}},
	})

	tagsByArticle := config.NewIndexedViewT("tags", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
	)

	if !tagsByArticle.Has("Pizza") {
		t.Error("Has(Pizza) = false, want true")
	}

	if !tagsByArticle.Has("Burger") {
		t.Error("Has(Burger) = false, want true")
	}

	if tagsByArticle.Has("Sushi") {
		t.Error("Has(Sushi) = true, want false")
	}
}

func TestIndexedViewT_Has_EmptyCollection(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")

	tagsByArticle := config.NewIndexedViewT("tags", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
	)

	if tagsByArticle.Has("Pizza") {
		t.Error("Has(Pizza) on empty should be false")
	}
}

func TestIndexedView_Has_AfterSwap(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Category: "food"},
	})

	byCategory := config.NewIndexedView("by-cat", c,
		func(b articleWithTag) string { return b.Category },
	)

	if !byCategory.Has("food") {
		t.Fatal("initial: Has(food) should be true")
	}

	// Swap to different categories.
	_ = c.Swap(v2(), []articleWithTag{
		{ID: 2, Category: "drink"},
	})

	if byCategory.Has("food") {
		t.Error("after swap: Has(food) should be false")
	}

	if !byCategory.Has("drink") {
		t.Error("after swap: Has(drink) should be true")
	}
}

func TestIndexedView_WithPersistence(t *testing.T) {
	store := &mockIndexPersistence{data: make(map[string][]byte)}

	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Category: "food"},
	})

	_ = config.NewIndexedView("idx-persist", c,
		func(b articleWithTag) string { return b.Category },
		config.WithIndexPersistence[articleWithTag, string](store),
	)

	// Allow async persistence goroutine.
	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	_, ok := store.data["idx-persist"]
	store.mu.Unlock()

	if !ok {
		t.Error("expected persistence Save to be called")
	}
}

func TestIndexedView_WithErrorHandler(t *testing.T) {
	var mu sync.Mutex
	var capturedErr error

	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{{ID: 1, Category: "food"}})

	_ = config.NewIndexedView("idx-err", c,
		func(b articleWithTag) string { return b.Category },
		config.WithIndexPersistence[articleWithTag, string](&failingIndexPersistence{}),
		config.WithIndexErrorHandler[articleWithTag, string](func(_ string, err error) {
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

func TestIndexedViewT_WithPersistence(t *testing.T) {
	store := &mockIndexPersistence{data: make(map[string][]byte)}

	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10, Priority: 100}}},
	})

	_ = config.NewIndexedViewT("idxT-persist", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
		config.WithIndexTPersistence[articleWithTag, string, tag](store),
	)

	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	_, ok := store.data["idxT-persist"]
	store.mu.Unlock()

	if !ok {
		t.Error("expected persistence Save to be called")
	}
}

func TestIndexedViewT_WithErrorHandler(t *testing.T) {
	var mu sync.Mutex
	var capturedErr error

	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10, Priority: 100}}},
	})

	_ = config.NewIndexedViewT("idxT-err", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
		config.WithIndexTPersistence[articleWithTag, string, tag](&failingIndexPersistence{}),
		config.WithIndexTErrorHandler[articleWithTag, string, tag](func(_ string, err error) {
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

// Index persistence helpers

type mockIndexPersistence struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *mockIndexPersistence) Save(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data[key] = make([]byte, len(data))
	copy(m.data[key], data)

	return nil
}

func (m *mockIndexPersistence) Load(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.data[key]
	if !ok {
		return nil, nil
	}

	return d, nil
}

type failingIndexPersistence struct{}

func (f *failingIndexPersistence) Save(_ context.Context, _ string, _ []byte) error {
	return fmt.Errorf("save failed")
}

func (f *failingIndexPersistence) Load(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("load failed")
}

func TestIndexedView_Name(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")

	idx := config.NewIndexedView("my-index", c,
		func(b articleWithTag) string { return b.Category },
	)

	if idx.Name() != "my-index" {
		t.Errorf("Name() = %q, want 'my-index'", idx.Name())
	}
}

func TestIndexedViewT_Name(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")

	idx := config.NewIndexedViewT("my-index-t", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
	)

	if idx.Name() != "my-index-t" {
		t.Errorf("Name() = %q, want 'my-index-t'", idx.Name())
	}
}

func TestIndexedViewT_All(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10, Priority: 100}}},
		{ID: 2, Name: "Burger", Tags: []tag{{ID: 20, Priority: 50}}},
	})

	idx := config.NewIndexedViewT("all-test", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
	)

	all := idx.All()
	if len(all) != 2 {
		t.Errorf("All() has %d keys, want 2", len(all))
	}

	if len(all["Pizza"]) != 1 || all["Pizza"][0].Priority != 100 {
		t.Errorf("All()[Pizza] = %+v", all["Pizza"])
	}
}

func TestIndexedViewT_Keys(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10}}},
		{ID: 2, Name: "Burger", Tags: []tag{{ID: 20}}},
	})

	idx := config.NewIndexedViewT("keys-test", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
	)

	keys := idx.Keys()
	if len(keys) != 2 {
		t.Errorf("Keys() = %d, want 2", len(keys))
	}
}

func TestIndexedViewT_Get_NonExistent(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10}}},
	})

	idx := config.NewIndexedViewT("get-test", c,
		func(b articleWithTag) string { return b.Name },
		func(b articleWithTag) []tag { return b.Tags },
	)

	if idx.Get("Nonexistent") != nil {
		t.Error("Get(Nonexistent) should return nil")
	}
}

func TestIndexedView_OnChange(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{{ID: 1, Category: "food"}})

	idx := config.NewIndexedView("by-cat", c,
		func(b articleWithTag) string { return b.Category },
	)

	var hookCalled bool
	idx.OnChange(func(old, new map[string][]articleWithTag) {
		hookCalled = true
		if len(new) != 2 {
			t.Errorf("hook: new has %d keys, want 2", len(new))
		}
	})

	_ = c.Swap(v2(), []articleWithTag{
		{ID: 1, Category: "food"},
		{ID: 2, Category: "drink"},
	})

	if !hookCalled {
		t.Error("OnChange hook not called")
	}
}

func TestIndexedView_Close_StopsRecomputing(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Category: "tech"},
	})

	byCategory := config.NewIndexedView("by-category", c,
		func(a articleWithTag) string { return a.Category },
	)

	if byCategory.Count() != 1 {
		t.Fatalf("initial Count() = %d, want 1", byCategory.Count())
	}

	byCategory.Close()

	_ = c.Swap(v2(), []articleWithTag{
		{ID: 1, Category: "tech"},
		{ID: 2, Category: "food"},
	})

	if byCategory.Count() != 1 {
		t.Errorf("Count() after Close = %d, want 1 (should not recompute)", byCategory.Count())
	}
}

func TestIndexedViewT_Close_StopsRecomputing(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10, Priority: 100}}},
	})

	byName := config.NewIndexedViewT("tags-by-name", c,
		func(a articleWithTag) string { return a.Name },
		func(a articleWithTag) []tag { return a.Tags },
	)

	if byName.Count() != 1 {
		t.Fatalf("initial Count() = %d, want 1", byName.Count())
	}

	byName.Close()

	_ = c.Swap(v2(), []articleWithTag{
		{ID: 1, Name: "Pizza", Tags: []tag{{ID: 10, Priority: 100}}},
		{ID: 2, Name: "Salad", Tags: []tag{{ID: 20, Priority: 200}}},
	})

	if byName.Count() != 1 {
		t.Errorf("Count() after Close = %d, want 1 (should not recompute)", byName.Count())
	}
}

func TestIndexedView_OnChange_PanicRecovery(t *testing.T) {
	c := config.NewCollection[articleWithTag]("articles")
	_ = c.Swap(v1(), []articleWithTag{{ID: 1, Category: "food"}})

	var errReported error
	byCategory := config.NewIndexedView("by-cat", c,
		func(b articleWithTag) string { return b.Category },
		config.WithIndexErrorHandler[articleWithTag, string](func(_ string, err error) {
			errReported = err
		}),
	)

	// Register a panicking hook followed by a normal hook.
	var secondHookCalled bool
	byCategory.OnChange(func(_, _ map[string][]articleWithTag) {
		panic("boom")
	})
	byCategory.OnChange(func(_, _ map[string][]articleWithTag) {
		secondHookCalled = true
	})

	// Swap triggers recompute → hooks should recover the panic.
	_ = c.Swap(v2(), []articleWithTag{{ID: 2, Category: "drinks"}})

	if !secondHookCalled {
		t.Error("second hook was not called after first hook panicked")
	}

	if errReported == nil {
		t.Error("expected error to be reported via onError")
	}

	// Verify the view still has correct data despite the panic.
	if byCategory.Count() != 1 {
		t.Errorf("Count() = %d, want 1", byCategory.Count())
	}
	if !byCategory.Has("drinks") {
		t.Error("expected drinks category after swap")
	}
}
