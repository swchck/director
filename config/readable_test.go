package config_test

import (
	"testing"
	"time"

	"github.com/swchck/director/config"
)

func TestReadableCollection_InterfaceCompliance(t *testing.T) {
	col := config.NewCollection[item]("test")

	var rc config.ReadableCollection[item] = col

	if rc.Name() != "test" {
		t.Errorf("Name() = %q", rc.Name())
	}
	if rc.Count() != 0 {
		t.Errorf("Count() = %d", rc.Count())
	}
	if items := rc.All(); len(items) != 0 {
		t.Errorf("All() = %v", items)
	}
	if _, ok := rc.First(); ok {
		t.Error("First() should return false on empty collection")
	}
	if _, ok := rc.Find(func(item) bool { return true }); ok {
		t.Error("Find() should return false on empty collection")
	}
	if items := rc.FindMany(func(item) bool { return true }); len(items) != 0 {
		t.Errorf("FindMany() = %v", items)
	}
	if items := rc.Filter(); len(items) != 0 {
		t.Errorf("Filter() = %v", items)
	}
}

func TestReadableSingleton_InterfaceCompliance(t *testing.T) {
	type settings struct {
		Debug bool `json:"debug"`
	}

	s := config.NewSingleton[settings]("test-singleton")
	var rs config.ReadableSingleton[settings] = s

	if rs.Name() != "test-singleton" {
		t.Errorf("Name() = %q", rs.Name())
	}
	if _, ok := rs.Get(); ok {
		t.Error("Get() should return false on empty singleton")
	}
}

func TestReadableCollection_HidesSwap(t *testing.T) {
	col := config.NewCollection[item]("products")
	var rc config.ReadableCollection[item] = col

	// rc.Swap() does NOT compile — Swap is not in the interface.
	// But the concrete type still has Swap for the manager.
	_ = col.Swap(config.NewVersion(time.Now()), []item{{ID: 1, Name: "Widget"}})

	// ReadableCollection reflects the swap.
	if rc.Count() != 1 {
		t.Errorf("Count after swap = %d, want 1", rc.Count())
	}

	found, ok := rc.Find(func(i item) bool { return i.Name == "Widget" })
	if !ok || found.ID != 1 {
		t.Errorf("Find Widget = %v, %v", found, ok)
	}
}

func TestReadableCollection_ViewCompliance(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1, Category: "a"}})

	view := config.NewView("a-items", c, nil)

	var rc config.ReadableCollection[item] = view

	if rc.Name() != "a-items" {
		t.Errorf("Name() = %q, want 'a-items'", rc.Name())
	}
	if rc.Count() != 1 {
		t.Errorf("Count() = %d, want 1", rc.Count())
	}
}

func TestReadableCollection_TranslatedViewCompliance(t *testing.T) {
	type product struct {
		ID   int
		Name string
	}
	type localized struct {
		ID   int
		Name string
	}

	c := config.NewCollection[product]("products")
	_ = c.Swap(v1(), []product{{ID: 1, Name: "Apple"}})

	tv := config.NewTranslatedView("products-loc", c, func(p product) localized {
		return localized(p)
	})

	var rc config.ReadableCollection[localized] = tv

	if rc.Count() != 1 {
		t.Errorf("Count() = %d, want 1", rc.Count())
	}
}

func TestReadableCollection_RelatedViewCompliance(t *testing.T) {
	c := config.NewCollection[articleWithTags]("articles")
	_ = c.Swap(v1(), []articleWithTags{
		{ID: 1, Tags: []tag{{ID: 10, Priority: 100}}},
	})

	rv := config.NewRelatedView("tags", c,
		func(a articleWithTags) []tag { return a.Tags },
	)

	var rc config.ReadableCollection[tag] = rv

	if rc.Count() != 1 {
		t.Errorf("Count() = %d, want 1", rc.Count())
	}
}
