package diff_test

import (
	"sort"
	"testing"

	"github.com/swchck/director/config/diff"
)

type product struct {
	ID    int
	Name  string
	Price int
}

func keyByID(p product) int { return p.ID }

// sortByID sorts a slice of products by ID for deterministic comparison.
// diff.By guarantees no order; tests sort before asserting.
func sortByID(items []product) []product {
	out := append([]product(nil), items...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func equalSlice(a, b []product) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBy_Added(t *testing.T) {
	old := []product{{ID: 1, Name: "A"}}
	newS := []product{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}

	added, updated, removed := diff.By(old, newS, keyByID)
	if !equalSlice(sortByID(added), []product{{ID: 2, Name: "B"}}) {
		t.Errorf("added = %+v", added)
	}
	if len(updated) != 0 {
		t.Errorf("updated = %+v, want empty", updated)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %+v, want empty", removed)
	}
}

func TestBy_Removed(t *testing.T) {
	old := []product{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}
	newS := []product{{ID: 1, Name: "A"}}

	added, updated, removed := diff.By(old, newS, keyByID)
	if len(added) != 0 {
		t.Errorf("added = %+v", added)
	}
	if len(updated) != 0 {
		t.Errorf("updated = %+v", updated)
	}
	if !equalSlice(sortByID(removed), []product{{ID: 2, Name: "B"}}) {
		t.Errorf("removed = %+v", removed)
	}
}

func TestBy_Updated_ReturnsNewValue(t *testing.T) {
	old := []product{{ID: 1, Name: "A", Price: 10}}
	newS := []product{{ID: 1, Name: "A", Price: 20}}

	added, updated, removed := diff.By(old, newS, keyByID)
	if len(added) != 0 {
		t.Errorf("added = %+v", added)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %+v", removed)
	}
	if len(updated) != 1 || updated[0].Price != 20 {
		t.Errorf("updated = %+v, want [{ID:1 Name:A Price:20}]", updated)
	}
}

func TestBy_Identical_NoChanges(t *testing.T) {
	old := []product{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}
	newS := []product{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}

	added, updated, removed := diff.By(old, newS, keyByID)
	if len(added) != 0 || len(updated) != 0 || len(removed) != 0 {
		t.Errorf("expected empty, got added=%+v updated=%+v removed=%+v", added, updated, removed)
	}
}

func TestBy_AllAddedFromEmptyOld(t *testing.T) {
	old := []product(nil)
	newS := []product{{ID: 1}, {ID: 2}}

	added, updated, removed := diff.By(old, newS, keyByID)
	if len(added) != 2 {
		t.Errorf("added len = %d, want 2", len(added))
	}
	if len(updated) != 0 || len(removed) != 0 {
		t.Errorf("expected only added; got updated=%+v removed=%+v", updated, removed)
	}
}

func TestBy_AllRemovedFromEmptyNew(t *testing.T) {
	old := []product{{ID: 1}, {ID: 2}}
	newS := []product(nil)

	added, updated, removed := diff.By(old, newS, keyByID)
	if len(removed) != 2 {
		t.Errorf("removed len = %d, want 2", len(removed))
	}
	if len(added) != 0 || len(updated) != 0 {
		t.Errorf("expected only removed; got added=%+v updated=%+v", added, updated)
	}
}

func TestBy_BothEmpty(t *testing.T) {
	added, updated, removed := diff.By([]product{}, []product{}, keyByID)
	if len(added) != 0 || len(updated) != 0 || len(removed) != 0 {
		t.Errorf("expected all empty; got added=%+v updated=%+v removed=%+v", added, updated, removed)
	}
}

func TestBy_MixedAddedUpdatedRemoved(t *testing.T) {
	old := []product{
		{ID: 1, Name: "Keep", Price: 10},
		{ID: 2, Name: "Change", Price: 20},
		{ID: 3, Name: "Remove", Price: 30},
	}
	newS := []product{
		{ID: 1, Name: "Keep", Price: 10},
		{ID: 2, Name: "Change", Price: 25}, // updated
		{ID: 4, Name: "Add", Price: 40},    // added
	}

	added, updated, removed := diff.By(old, newS, keyByID)
	if !equalSlice(sortByID(added), []product{{ID: 4, Name: "Add", Price: 40}}) {
		t.Errorf("added = %+v", added)
	}
	if !equalSlice(sortByID(updated), []product{{ID: 2, Name: "Change", Price: 25}}) {
		t.Errorf("updated = %+v", updated)
	}
	if !equalSlice(sortByID(removed), []product{{ID: 3, Name: "Remove", Price: 30}}) {
		t.Errorf("removed = %+v", removed)
	}
}

func TestByEqual_CustomEquality(t *testing.T) {
	// Equality based only on Name; ignore Price.
	nameOnly := func(a, b product) bool { return a.Name == b.Name }

	old := []product{{ID: 1, Name: "A", Price: 10}}
	newS := []product{{ID: 1, Name: "A", Price: 999}}

	added, updated, removed := diff.ByEqual(old, newS, keyByID, nameOnly)
	if len(added) != 0 || len(updated) != 0 || len(removed) != 0 {
		t.Errorf("expected no changes when only Price differs; got added=%+v updated=%+v removed=%+v",
			added, updated, removed)
	}
}

func TestByEqual_DetectsViaCustomEquality(t *testing.T) {
	// Equality based only on Name. Different Name → updated.
	nameOnly := func(a, b product) bool { return a.Name == b.Name }

	old := []product{{ID: 1, Name: "Old"}}
	newS := []product{{ID: 1, Name: "New"}}

	added, updated, removed := diff.ByEqual(old, newS, keyByID, nameOnly)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("expected no add/remove; got added=%+v removed=%+v", added, removed)
	}
	if len(updated) != 1 || updated[0].Name != "New" {
		t.Errorf("updated = %+v, want [{Name:New}]", updated)
	}
}

func TestBy_StringKey(t *testing.T) {
	type entry struct {
		Slug  string
		Value int
	}
	keyFn := func(e entry) string { return e.Slug }

	old := []entry{{Slug: "a", Value: 1}, {Slug: "b", Value: 2}}
	newS := []entry{{Slug: "b", Value: 2}, {Slug: "c", Value: 3}}

	added, updated, removed := diff.ByEqual(old, newS, keyFn, func(a, b entry) bool { return a == b })
	if len(added) != 1 || added[0].Slug != "c" {
		t.Errorf("added = %+v", added)
	}
	if len(removed) != 1 || removed[0].Slug != "a" {
		t.Errorf("removed = %+v", removed)
	}
	if len(updated) != 0 {
		t.Errorf("updated = %+v", updated)
	}
}
