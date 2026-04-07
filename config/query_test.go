package config_test

import (
	"cmp"
	"testing"

	"github.com/swchck/director/config"
)

func TestWhere(t *testing.T) {
	items := []item{
		{ID: 1, Category: "a"},
		{ID: 2, Category: "b"},
		{ID: 3, Category: "a"},
	}

	result := config.Where(func(i item) bool { return i.Category == "a" })(items)
	if len(result) != 2 {
		t.Errorf("Where returned %d items, want 2", len(result))
	}

	if result[0].ID != 1 || result[1].ID != 3 {
		t.Errorf("Where result = %+v", result)
	}
}

func TestSortBy(t *testing.T) {
	items := []item{
		{ID: 3, Level: 30},
		{ID: 1, Level: 10},
		{ID: 2, Level: 20},
	}

	result := config.SortBy(func(a, b item) int {
		return cmp.Compare(a.Level, b.Level)
	})(items)

	if result[0].ID != 1 || result[1].ID != 2 || result[2].ID != 3 {
		t.Errorf("SortBy result = %+v", result)
	}

	// Original should be unchanged.
	if items[0].ID != 3 {
		t.Error("SortBy mutated the original slice")
	}
}

func TestLimit(t *testing.T) {
	items := []item{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}

	result := config.Limit[item](3)(items)
	if len(result) != 3 {
		t.Errorf("Limit(3) returned %d items", len(result))
	}

	// Limit larger than slice.
	result = config.Limit[item](100)(items)
	if len(result) != 5 {
		t.Errorf("Limit(100) returned %d items, want 5", len(result))
	}
}

func TestOffset(t *testing.T) {
	items := []item{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}

	result := config.Offset[item](2)(items)
	if len(result) != 3 || result[0].ID != 3 {
		t.Errorf("Offset(2) = %+v", result)
	}

	// Offset beyond slice.
	result = config.Offset[item](10)(items)
	if result != nil {
		t.Errorf("Offset(10) = %v, want nil", result)
	}
}

func TestFilterPipeline(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a", Level: 30},
		{ID: 2, Category: "b", Level: 10},
		{ID: 3, Category: "a", Level: 20},
		{ID: 4, Category: "a", Level: 40},
		{ID: 5, Category: "b", Level: 50},
	})

	result := c.Filter(
		config.Where(func(i item) bool { return i.Category == "a" }),
		config.SortBy(func(a, b item) int { return cmp.Compare(a.Level, b.Level) }),
		config.Limit[item](2),
	)

	if len(result) != 2 {
		t.Fatalf("Filter pipeline returned %d items, want 2", len(result))
	}

	if result[0].ID != 3 || result[1].ID != 1 {
		t.Errorf("Filter pipeline = %+v, want IDs [3, 1]", result)
	}
}
