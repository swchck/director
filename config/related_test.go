package config_test

import (
	"testing"

	"github.com/swchck/director/config"
)

type articleWithTags struct {
	ID   int
	Name string
	Tags []tag
}

type tag struct {
	ID       int
	Name     string
	Priority int
}

func TestRelatedView_FlattensNestedItems(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Name: "Article A", Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
		{ID: 2, Name: "Article B", Tags: []tag{{ID: 12, Priority: 50}}},
		{ID: 3, Name: "Article C", Tags: nil},
	})

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	if allTags.Count() != 3 {
		t.Errorf("Count() = %d, want 3", allTags.Count())
	}

	// Find a specific tag by ID.
	lv, ok := allTags.Find(func(t tag) bool { return t.ID == 11 })
	if !ok || lv.Priority != 200 {
		t.Errorf("Find(11) = %+v, ok=%v", lv, ok)
	}
}

func TestRelatedView_FilterByPriority(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 50}, {ID: 11, Priority: 150}}},
		{ID: 2, Tags: []tag{{ID: 12, Priority: 300}}},
	})

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	expensive := allTags.Filter(
		config.Where(func(t tag) bool { return t.Priority > 100 }),
	)

	if len(expensive) != 2 {
		t.Errorf("expensive tags = %d, want 2", len(expensive))
	}
}

func TestRelatedView_Dedup(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
		{ID: 2, Tags: []tag{{ID: 10, Priority: 100}, {ID: 12, Priority: 50}}}, // ID 10 is shared
	})

	// Without dedup: 4 items.
	noDedupView := config.NewRelatedView("no-dedup", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	if noDedupView.Count() != 4 {
		t.Errorf("without dedup: Count() = %d, want 4", noDedupView.Count())
	}

	// With dedup: 3 items (ID 10 appears once).
	dedupView := config.NewRelatedView("dedup", c,
		func(b articleWithTags) []tag { return b.Tags },
		config.WithDedup[articleWithTags, tag](func(a, b tag) bool { return a.ID == b.ID }),
	)

	if dedupView.Count() != 3 {
		t.Errorf("with dedup: Count() = %d, want 3", dedupView.Count())
	}
}

func TestRelatedView_RecomputesOnParentSwap(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	if allTags.Count() != 1 {
		t.Fatalf("initial: Count() = %d, want 1", allTags.Count())
	}

	// Swap parent — related view should auto-update.
	_ = c.Swap(v2(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
		{ID: 2, Tags: []tag{{ID: 12, Priority: 300}}},
	})

	if allTags.Count() != 3 {
		t.Errorf("after swap: Count() = %d, want 3", allTags.Count())
	}

	lv, ok := allTags.Find(func(t tag) bool { return t.ID == 12 })
	if !ok || lv.Priority != 300 {
		t.Errorf("after swap: Find(12) = %+v, ok=%v", lv, ok)
	}
}

func TestRelatedView_First(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
	})

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	first, ok := allTags.First()
	if !ok {
		t.Fatal("First() returned false")
	}

	if first.ID != 10 {
		t.Errorf("First().ID = %d, want 10", first.ID)
	}
}

func TestRelatedView_First_Empty(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	_, ok := allTags.First()
	if ok {
		t.Error("First() should return false on empty RelatedView")
	}
}

func TestRelatedView_First_EmptyTags(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: nil},
		{ID: 2, Tags: []tag{}},
	})

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	_, ok := allTags.First()
	if ok {
		t.Error("First() should return false when all parents have empty tags")
	}
}

func TestRelatedView_Name(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	if allTags.Name() != "article-tags" {
		t.Errorf("Name() = %q, want 'article-tags'", allTags.Name())
	}
}

func TestRelatedView_Find_NotFound(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	_, ok := allTags.Find(func(t tag) bool { return t.ID == 999 })
	if ok {
		t.Error("Find() should return false for non-existent item")
	}
}

func TestRelatedView_Find_Empty(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	_, ok := allTags.Find(func(t tag) bool { return true })
	if ok {
		t.Error("Find() should return false on empty RelatedView")
	}
}

func TestRelatedView_All_ReturnsCopy(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	got := allTags.All()
	got[0].Priority = 999

	got2 := allTags.All()
	if got2[0].Priority != 100 {
		t.Error("All() returned a reference, not a copy")
	}
}

func TestRelatedView_FindMany(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
		{ID: 2, Tags: []tag{{ID: 12, Priority: 100}}},
	})

	allTags := config.NewRelatedView("article-tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	cheap := allTags.FindMany(func(t tag) bool { return t.Priority == 100 })
	if len(cheap) != 2 {
		t.Errorf("FindMany(price==100) = %d items, want 2", len(cheap))
	}
}

func TestRelatedView_RecoversPanicInHook(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	var errCaught error
	allTags := config.NewRelatedView("tags", c,
		func(b articleWithTags) []tag { return b.Tags },
		config.WithRelatedViewErrorHandler[articleWithTags, tag](func(_ string, err error) {
			errCaught = err
		}),
	)

	allTags.OnChange(func(_, _ []tag) {
		panic("hook exploded")
	})

	// Swap the source — RelatedView recompute should recover the panic.
	_ = c.Swap(v2(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
	})

	if errCaught == nil {
		t.Fatal("expected error from panicking hook")
	}

	// Data should still be updated despite the panic.
	if allTags.Count() != 2 {
		t.Errorf("Count() = %d, want 2 (recompute should commit before hooks)", allTags.Count())
	}
}

func TestRelatedView_OnChange(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	allTags := config.NewRelatedView("tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	var called bool
	var oldCount, newCount int
	allTags.OnChange(func(old, new []tag) {
		called = true
		oldCount = len(old)
		newCount = len(new)
	})

	_ = c.Swap(v2(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
		{ID: 2, Tags: []tag{{ID: 12, Priority: 50}}},
	})

	if !called {
		t.Fatal("OnChange was not called")
	}
	if oldCount != 1 {
		t.Errorf("old count = %d, want 1", oldCount)
	}
	if newCount != 3 {
		t.Errorf("new count = %d, want 3", newCount)
	}
}

func TestRelatedView_Close_StopsRecomputing(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	allTags := config.NewRelatedView("tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	if allTags.Count() != 1 {
		t.Fatalf("initial Count() = %d, want 1", allTags.Count())
	}

	allTags.Close()

	_ = c.Swap(v2(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
	})

	if allTags.Count() != 1 {
		t.Errorf("Count() after Close = %d, want 1 (should not recompute)", allTags.Count())
	}
}

func TestRelatedView_Version_MatchesSource(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	ver := v1()
	_ = c.Swap(ver, []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	allTags := config.NewRelatedView("tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	if allTags.Version() != ver {
		t.Errorf("Version() = %v, want %v", allTags.Version(), ver)
	}

	ver2 := v2()
	_ = c.Swap(ver2, []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
	})

	if allTags.Version() != ver2 {
		t.Errorf("Version() after swap = %v, want %v", allTags.Version(), ver2)
	}
}

func TestRelatedView_PanicInOneHookDoesNotBlockOthers(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	allTags := config.NewRelatedView("tags", c,
		func(b articleWithTags) []tag { return b.Tags },
	)

	// First hook panics.
	allTags.OnChange(func(_, _ []tag) {
		panic("first hook exploded")
	})

	// Second hook should still run.
	var secondCalled bool
	allTags.OnChange(func(_, _ []tag) {
		secondCalled = true
	})

	_ = c.Swap(v2(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}, {ID: 11, Priority: 200}}},
	})

	if !secondCalled {
		t.Error("second hook should run even when first panics")
	}
}
