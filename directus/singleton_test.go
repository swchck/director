package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/swchck/director/directus"
)

type testSettings struct {
	MaxPlayers int  `json:"max_players"`
	Debug      bool `json:"debug"`
}

func TestSingleton_Get(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/items/settings" {
			t.Errorf("path = %s, want /items/settings", r.URL.Path)
		}

		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}

		writeJSONData(w, testSettings{MaxPlayers: 100, Debug: true})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	item, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if item.MaxPlayers != 100 || !item.Debug {
		t.Errorf("got %+v", item)
	}
}

func TestSingleton_Get_WithQueryOptions(t *testing.T) {
	var gotFields string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotFields = r.URL.Query().Get("fields")
		writeJSONData(w, testSettings{MaxPlayers: 50})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	_, err := s.Get(context.Background(), directus.WithFields("max_players"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotFields != "max_players" {
		t.Errorf("fields = %q, want 'max_players'", gotFields)
	}
}

func TestSingleton_Update(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}

		var body testSettings
		json.NewDecoder(r.Body).Decode(&body)

		body.MaxPlayers = 200
		writeJSONData(w, body)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	item := &testSettings{MaxPlayers: 200}
	updated, err := s.Update(context.Background(), item)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.MaxPlayers != 200 {
		t.Errorf("MaxPlayers = %d, want 200", updated.MaxPlayers)
	}
}

func TestSingleton_DateUpdated(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		fields := r.URL.Query().Get("fields")
		if fields != "date_updated" {
			t.Errorf("fields = %q, want 'date_updated'", fields)
		}

		writeJSONData(w, map[string]any{
			"date_updated": now.Format(time.RFC3339),
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	got, err := s.DateUpdated(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.Equal(now) {
		t.Errorf("got %v, want %v", got, now)
	}
}

func TestSingleton_Collection(t *testing.T) {
	client := directus.NewClient("http://example.com", "token")
	s := directus.NewSingleton[testSettings](client, "my_settings")

	if s.Collection() != "my_settings" {
		t.Errorf("Collection() = %q, want 'my_settings'", s.Collection())
	}
}
