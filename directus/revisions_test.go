package directus_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListRevisions(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/revisions" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Revision{
			{ID: 1, Collection: "products", Item: "1"},
			{ID: 2, Collection: "products", Item: "2"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	revisions, err := client.ListRevisions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(revisions) != 2 {
		t.Fatalf("got %d revisions, want 2", len(revisions))
	}

	if revisions[0].Collection != "products" {
		t.Errorf("revisions[0].Collection = %q", revisions[0].Collection)
	}
}

func TestListRevisions_WithOptions(t *testing.T) {
	var gotQuery string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSONData(w, []directus.Revision{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListRevisions(context.Background(),
		directus.WithLimit(5),
		directus.WithFilter(directus.Field("collection", "_eq", "products")),
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery == "" {
		t.Error("expected query params")
	}
}

func TestGetRevision(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/revisions/42" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Revision{ID: 42, Collection: "settings", Item: "1"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	rev, err := client.GetRevision(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}

	if rev.ID != 42 || rev.Collection != "settings" {
		t.Errorf("revision = %+v", rev)
	}
}

func TestGetRevision_NotFound(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errors":[{"message":"not found"}]}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetRevision(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error")
	}
}
