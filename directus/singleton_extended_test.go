package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/swchck/director/directus"
)

func TestSingleton_DateUpdated_FallsBackToDateCreated(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	callCount := 0

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		fields := r.URL.Query().Get("fields")

		switch fields {
		case "date_updated":
			// Return null date_updated
			writeJSONData(w, map[string]any{
				"date_updated": nil,
			})
		case "date_created":
			writeJSONData(w, map[string]any{
				"date_created": now.Format(time.RFC3339),
			})
		default:
			t.Errorf("unexpected fields = %q", fields)
		}
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

	if callCount != 2 {
		t.Errorf("expected 2 API calls (date_updated + date_created), got %d", callCount)
	}
}

func TestSingleton_DateUpdated_NeitherFieldExists(t *testing.T) {
	callCount := 0

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		fields := r.URL.Query().Get("fields")

		switch fields {
		case "date_updated":
			writeJSONData(w, map[string]any{"date_updated": nil})
		case "date_created":
			// Simulate field not existing by returning error
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{
				"errors": []map[string]any{{"message": "field not found"}},
			})
		}
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	got, err := s.DateUpdated(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v (date_created not existing should not be fatal)", err)
	}

	if !got.IsZero() {
		t.Errorf("expected zero time, got %v", got)
	}
}

func TestSingleton_Get_Error(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "internal error"}},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	_, err := s.Get(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSingleton_Update_Error(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "forbidden"}},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	_, err := s.Update(context.Background(), &testSettings{MaxPlayers: 200})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSingleton_DateUpdated_FieldForbidden_FallsBackToDateCreated(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		fields := r.URL.Query().Get("fields")

		switch fields {
		case "date_updated":
			// 403 — field not accessible / doesn't exist
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]any{
				"errors": []map[string]any{{"message": "forbidden"}},
			})
		case "date_created":
			writeJSONData(w, map[string]any{
				"date_created": now.Format(time.RFC3339),
			})
		}
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	got, err := s.DateUpdated(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v (should fallback on 403)", err)
	}

	if !got.Equal(now) {
		t.Errorf("got %v, want %v", got, now)
	}
}

func TestSingleton_DateUpdated_BothFieldsMissing_ReturnsZero(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "forbidden"}},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")

	got, err := s.DateUpdated(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v (both fields missing should return zero)", err)
	}

	if !got.IsZero() {
		t.Errorf("expected zero time, got %v", got)
	}
}

func TestItems_MaxDateUpdated_FallsBackToDateCreated(t *testing.T) {
	now := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	callCount := 0

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query()
		sort := q.Get("sort")

		switch sort {
		case "-date_updated":
			// Return empty - no items have been updated
			writeJSONData(w, []map[string]any{
				{"date_updated": nil},
			})
		case "-date_created":
			writeJSONData(w, []map[string]any{
				{"date_created": now.Format(time.RFC3339)},
			})
		}
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	got, err := items.MaxDateUpdated(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !got.Equal(now) {
		t.Errorf("got %v, want %v", got, now)
	}

	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestItems_MaxDateUpdated_FieldForbidden_FallsBackToDateCreated(t *testing.T) {
	now := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		sort := r.URL.Query().Get("sort")

		switch sort {
		case "-date_updated":
			// 403 — field not accessible / doesn't exist
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]any{
				"errors": []map[string]any{{"message": "forbidden"}},
			})
		case "-date_created":
			writeJSONData(w, []map[string]any{
				{"date_created": now.Format(time.RFC3339)},
			})
		}
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	got, err := items.MaxDateUpdated(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v (should fallback on 403)", err)
	}

	if !got.Equal(now) {
		t.Errorf("got %v, want %v", got, now)
	}
}

func TestItems_MaxDateUpdated_DateCreatedError(t *testing.T) {
	callCount := 0

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query()
		sort := q.Get("sort")

		switch sort {
		case "-date_updated":
			writeJSONData(w, []map[string]any{})
		case "-date_created":
			// Simulate error - field doesn't exist
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{
				"errors": []map[string]any{{"message": "field not found"}},
			})
		}
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test_items")

	got, err := items.MaxDateUpdated(context.Background())
	if err != nil {
		t.Fatalf("error should be nil for missing date_created, got %v", err)
	}

	if !got.IsZero() {
		t.Errorf("expected zero time, got %v", got)
	}
}
