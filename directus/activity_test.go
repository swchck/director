package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListActivity(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/activity" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, []directus.Activity{
			{ID: 1, Action: "create", Collection: "products"},
			{ID: 2, Action: "update", Collection: "products"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items, err := client.ListActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	if items[0].Action != "create" {
		t.Errorf("items[0].Action = %q", items[0].Action)
	}
}

func TestListActivity_WithOptions(t *testing.T) {
	var gotQuery string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSONData(w, []directus.Activity{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListActivity(context.Background(),
		directus.WithLimit(5),
		directus.WithSort("-timestamp"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery == "" {
		t.Error("expected query params")
	}
}

func TestGetActivity(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/activity/42" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Activity{ID: 42, Action: "update", Collection: "settings"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	item, err := client.GetActivity(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}

	if item.ID != 42 || item.Action != "update" {
		t.Errorf("activity = %+v", item)
	}
}

func TestGetActivity_NotFound(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "not found"}},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetActivity(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error")
	}
}
