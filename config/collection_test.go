package config_test

import (
	"sync"
	"testing"
	"time"

	"github.com/swchck/director/config"
)

type item struct {
	ID       int
	Name     string
	Category string
	Level    int
}

func v1() config.Version { return config.NewVersion(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) }
func v2() config.Version { return config.NewVersion(time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)) }

func TestCollection_NewCollection_EmptyByDefault(t *testing.T) {
	c := config.NewCollection[item]("items")

	if c.Name() != "items" {
		t.Errorf("Name() = %q, want 'items'", c.Name())
	}

	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0", c.Count())
	}

	if !c.Version().IsZero() {
		t.Errorf("Version() should be zero, got %s", c.Version())
	}

	if items := c.All(); len(items) != 0 {
		t.Errorf("All() = %v, want empty", items)
	}
}

func TestCollection_Swap_UpdatesData(t *testing.T) {
	c := config.NewCollection[item]("items")

	items := []item{
		{ID: 1, Name: "first"},
		{ID: 2, Name: "second"},
	}

	err := c.Swap(v1(), items)
	if err != nil {
		t.Fatalf("Swap error: %v", err)
	}

	if c.Count() != 2 {
		t.Errorf("Count() = %d, want 2", c.Count())
	}

	if c.Version() != v1() {
		t.Errorf("Version() = %s, want %s", c.Version(), v1())
	}
}

func TestCollection_Swap_CopiesInput(t *testing.T) {
	c := config.NewCollection[item]("items")

	items := []item{{ID: 1, Name: "original"}}
	_ = c.Swap(v1(), items)

	// Mutate the original slice — should not affect the collection.
	items[0].Name = "mutated"

	got := c.All()
	if got[0].Name != "original" {
		t.Errorf("mutation leaked: got %q, want 'original'", got[0].Name)
	}
}

func TestCollection_All_ReturnsCopy(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1, Name: "original"}})

	got := c.All()
	got[0].Name = "mutated"

	// Original should be unaffected.
	got2 := c.All()
	if got2[0].Name != "original" {
		t.Errorf("All() returned reference, not copy")
	}
}

func TestCollection_First(t *testing.T) {
	c := config.NewCollection[item]("items")

	// Empty.
	_, ok := c.First()
	if ok {
		t.Error("First() should return false on empty collection")
	}

	_ = c.Swap(v1(), []item{{ID: 1, Name: "first"}, {ID: 2, Name: "second"}})

	first, ok := c.First()
	if !ok {
		t.Fatal("First() returned false")
	}

	if first.ID != 1 {
		t.Errorf("First().ID = %d, want 1", first.ID)
	}
}

func TestCollection_Find(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Name: "alpha"},
		{ID: 2, Name: "beta"},
		{ID: 3, Name: "gamma"},
	})

	found, ok := c.Find(func(i item) bool { return i.ID == 2 })
	if !ok {
		t.Fatal("Find() returned false")
	}

	if found.Name != "beta" {
		t.Errorf("Find() = %+v, want beta", found)
	}

	_, ok = c.Find(func(i item) bool { return i.ID == 999 })
	if ok {
		t.Error("Find() should return false for non-existent item")
	}
}

func TestCollection_FindMany(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a"},
		{ID: 2, Category: "b"},
		{ID: 3, Category: "a"},
	})

	found := c.FindMany(func(i item) bool { return i.Category == "a" })
	if len(found) != 2 {
		t.Errorf("FindMany() returned %d items, want 2", len(found))
	}
}

func TestCollection_OnChange_FiresOnSwap(t *testing.T) {
	c := config.NewCollection[item]("items")

	var oldLen, newLen int
	c.OnChange(func(old, new []item) {
		oldLen = len(old)
		newLen = len(new)
	})

	_ = c.Swap(v1(), []item{{ID: 1}, {ID: 2}})

	if oldLen != 0 || newLen != 2 {
		t.Errorf("OnChange got old=%d new=%d, want old=0 new=2", oldLen, newLen)
	}

	_ = c.Swap(v2(), []item{{ID: 3}})

	if oldLen != 2 || newLen != 1 {
		t.Errorf("OnChange got old=%d new=%d, want old=2 new=1", oldLen, newLen)
	}
}

func TestCollection_Swap_RecoversPanicInHook(t *testing.T) {
	c := config.NewCollection[item]("items")

	c.OnChange(func(_, _ []item) {
		panic("hook exploded")
	})

	err := c.Swap(v1(), []item{{ID: 1}})
	if err == nil {
		t.Fatal("expected error from panicking hook")
	}

	// Data should still be swapped despite the panic.
	if c.Count() != 1 {
		t.Errorf("Count() = %d, want 1 (swap should commit before hooks)", c.Count())
	}
}

func TestCollection_ConcurrentReadsDuringSwap(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1}})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Concurrent readers.
	for range 10 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = c.All()
					_ = c.Count()
					_, _ = c.First()
					_, _ = c.Find(func(i item) bool { return i.ID == 1 })
				}
			}
		})
	}

	// Concurrent writer.
	for i := range 100 {
		_ = c.Swap(v1(), []item{{ID: i}})
	}

	close(stop)
	wg.Wait()
}
