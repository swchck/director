package directus_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/swchck/director/directus"
)

type testItem struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

func newTestServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func writeJSONData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func TestItems_List(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/items/test_items" {
			t.Errorf("path = %s, want /items/test_items", r.URL.Path)
		}

		writeJSONData(w, []testItem{
			{ID: 1, Name: "first", Category: "a"},
			{ID: 2, Name: "second", Category: "b"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	result, err := items.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("got %d items, want 2", len(result))
	}

	if result[0].Name != "first" || result[1].Name != "second" {
		t.Errorf("items = %+v", result)
	}
}

func TestItems_List_WithQueryOptions(t *testing.T) {
	var gotQuery url.Values

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		writeJSONData(w, []testItem{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "products")

	_, _ = items.List(context.Background(),
		directus.WithFilter(directus.Field("category", "_eq", "food")),
		directus.WithSort("-name"),
		directus.WithLimit(10),
		directus.WithOffset(5),
		directus.WithFields("id", "name"),
	)

	if gotQuery.Get("sort") != "-name" {
		t.Errorf("sort = %q, want '-name'", gotQuery.Get("sort"))
	}

	if gotQuery.Get("limit") != "10" {
		t.Errorf("limit = %q, want '10'", gotQuery.Get("limit"))
	}

	if gotQuery.Get("offset") != "5" {
		t.Errorf("offset = %q, want '5'", gotQuery.Get("offset"))
	}

	if gotQuery.Get("fields") != "id,name" {
		t.Errorf("fields = %q, want 'id,name'", gotQuery.Get("fields"))
	}

	filter := gotQuery.Get("filter")
	if filter == "" {
		t.Fatal("expected filter param")
	}

	var f map[string]any
	json.Unmarshal([]byte(filter), &f)
	catFilter := f["category"].(map[string]any)
	if catFilter["_eq"] != "food" {
		t.Errorf("filter = %v, want category._eq=food", f)
	}
}

func TestItems_Get(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/items/test_items/42" {
			t.Errorf("path = %s, want /items/test_items/42", r.URL.Path)
		}

		writeJSONData(w, testItem{ID: 42, Name: "found"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	item, err := items.Get(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if item.ID != 42 || item.Name != "found" {
		t.Errorf("item = %+v", item)
	}
}

func TestItems_Get_NotFound(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "not found"}},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	_, err := items.Get(context.Background(), "999")
	if !errors.Is(err, directus.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestItems_Create(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}

		writeJSONData(w, testItem{ID: 99, Name: "created", Category: "new"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	item := &testItem{Name: "created", Category: "new"}
	created, err := items.Create(context.Background(), item)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if created.ID != 99 {
		t.Errorf("created.ID = %d, want 99", created.ID)
	}
}

func TestItems_Update(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}

		if r.URL.Path != "/items/test_items/1" {
			t.Errorf("path = %s, want /items/test_items/1", r.URL.Path)
		}

		writeJSONData(w, testItem{ID: 1, Name: "updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	item := &testItem{Name: "updated"}
	updated, err := items.Update(context.Background(), "1", item)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.Name != "updated" {
		t.Errorf("updated.Name = %q, want 'updated'", updated.Name)
	}
}

func TestItems_Delete(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	err := items.Delete(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestItems_MaxDateUpdated(t *testing.T) {
	now := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("sort") != "-date_updated" {
			t.Errorf("sort = %q, want '-date_updated'", q.Get("sort"))
		}

		if q.Get("limit") != "1" {
			t.Errorf("limit = %q, want '1'", q.Get("limit"))
		}

		// Verify _nnull filter is applied to exclude NULL values.
		filter := q.Get("filter")
		if filter == "" {
			t.Fatal("expected filter param with _nnull")
		}

		var f map[string]any
		json.Unmarshal([]byte(filter), &f)
		duFilter, ok := f["date_updated"].(map[string]any)
		if !ok {
			t.Fatalf("expected date_updated filter, got %v", f)
		}
		if duFilter["_nnull"] != true {
			t.Errorf("expected _nnull=true filter, got %v", duFilter)
		}

		writeJSONData(w, []map[string]any{
			{"date_updated": now.Format(time.RFC3339)},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	got, err := items.MaxDateUpdated(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.Equal(now) {
		t.Errorf("got %v, want %v", got, now)
	}
}

func TestItems_MaxDateUpdated_EmptyCollection(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		writeJSONData(w, []any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	got, err := items.MaxDateUpdated(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.IsZero() {
		t.Errorf("expected zero time, got %v", got)
	}
}

func TestItems_Collection(t *testing.T) {
	client := directus.NewClient("http://example.com", "token")
	items := directus.NewItems[testItem](client, "my_collection")

	if items.Collection() != "my_collection" {
		t.Errorf("Collection() = %q, want 'my_collection'", items.Collection())
	}
}
