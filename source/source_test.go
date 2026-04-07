package source_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/swchck/director/directus"
	"github.com/swchck/director/source"
)

type product struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type settings struct {
	SiteName string `json:"site_name"`
	Locale   string `json:"locale"`
}

// FromDirectus (CollectionSource adapter)

func TestFromDirectus_List(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		json.NewEncoder(w).Encode(map[string]any{
			"data": []product{
				{ID: 1, Name: "Widget"},
				{ID: 2, Name: "Gadget"},
			},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "test-token")
	items := directus.NewItems[product](dc, "products")
	src := source.FromDirectus(items)

	_ = now // used only to ensure the test setup is valid

	ctx := context.Background()
	result, err := src.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("List() returned %d items, want 2", len(result))
	}

	if result[0].Name != "Widget" {
		t.Errorf("result[0].Name = %q, want 'Widget'", result[0].Name)
	}

	if result[1].Name != "Gadget" {
		t.Errorf("result[1].Name = %q, want 'Gadget'", result[1].Name)
	}
}

func TestFromDirectus_LastModified(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// The Items.MaxDateUpdated calls with sort=-date_updated&limit=1
		if r.URL.Query().Get("limit") == "1" && r.URL.Query().Get("sort") == "-date_updated" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"date_updated": now.Format(time.RFC3339)},
				},
			})
			return
		}

		// Fallback for date_created query
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "test-token")
	items := directus.NewItems[product](dc, "products")
	src := source.FromDirectus(items)

	ctx := context.Background()
	ts, err := src.LastModified(ctx)
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}

	if ts.IsZero() {
		t.Fatal("LastModified() returned zero time")
	}

	if !ts.Equal(now) {
		t.Errorf("LastModified() = %v, want %v", ts, now)
	}
}

func TestFromDirectus_LastModified_FallsBackToDateCreated(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("sort") == "-date_updated" {
			// date_updated is nil/empty
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"date_updated": nil},
				},
			})
			return
		}

		if r.URL.Query().Get("sort") == "-date_created" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"date_created": now.Format(time.RFC3339)},
				},
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "test-token")
	items := directus.NewItems[product](dc, "products")
	src := source.FromDirectus(items)

	ctx := context.Background()
	ts, err := src.LastModified(ctx)
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}

	if !ts.Equal(now) {
		t.Errorf("LastModified() = %v, want %v", ts, now)
	}
}

func TestFromDirectus_ImplementsCollectionSource(t *testing.T) {
	dc := directus.NewClient("http://localhost", "token")
	items := directus.NewItems[product](dc, "products")
	var _ = source.FromDirectus(items)
}

// FromDirectusSingleton (SingletonSource adapter)

func TestFromDirectusSingleton_Get(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		json.NewEncoder(w).Encode(map[string]any{
			"data": settings{SiteName: "My Site", Locale: "en"},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "test-token")
	singleton := directus.NewSingleton[settings](dc, "settings")
	src := source.FromDirectusSingleton(singleton)

	ctx := context.Background()
	result, err := src.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	if result == nil {
		t.Fatal("Get() returned nil")
	}

	if result.SiteName != "My Site" {
		t.Errorf("SiteName = %q, want 'My Site'", result.SiteName)
	}

	if result.Locale != "en" {
		t.Errorf("Locale = %q, want 'en'", result.Locale)
	}
}

func TestFromDirectusSingleton_LastModified(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// DateUpdated calls with fields=date_updated
		if r.URL.Query().Get("fields[]") == "date_updated" || r.URL.Query().Get("fields") == "date_updated" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"date_updated": now.Format(time.RFC3339),
				},
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "test-token")
	singleton := directus.NewSingleton[settings](dc, "settings")
	src := source.FromDirectusSingleton(singleton)

	ctx := context.Background()
	ts, err := src.LastModified(ctx)
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}

	if ts.IsZero() {
		t.Fatal("LastModified() returned zero time")
	}
}

func TestFromDirectusSingleton_ImplementsSingletonSource(t *testing.T) {
	dc := directus.NewClient("http://localhost", "token")
	singleton := directus.NewSingleton[settings](dc, "settings")
	var _ = source.FromDirectusSingleton(singleton)
}
