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
