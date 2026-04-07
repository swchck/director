package directus_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/swchck/director/directus"
)

func TestNewClient_SetsAuthHeader(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := directus.NewClient(srv.URL, "test-token-123")
	_ = client.Delete(context.Background(), "/test")

	if gotAuth != "Bearer test-token-123" {
		t.Errorf("got auth header %q, want %q", gotAuth, "Bearer test-token-123")
	}
}

func TestClient_Get_UnwrapsData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("got method %s, want GET", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": 1, "name": "test"},
			},
		})
	}))
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	raw, err := client.Get(context.Background(), "/items/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(items) != 1 || items[0]["name"] != "test" {
		t.Errorf("got %v, want [{id:1 name:test}]", items)
	}
}

func TestClient_Post_SendsJSON(t *testing.T) {
	var gotBody []byte
	var gotContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"id": 1, "name": "created"},
		})
	}))
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.Post(context.Background(), "/items/test", map[string]string{"name": "new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotContentType != "application/json" {
		t.Errorf("content type = %q, want application/json", gotContentType)
	}

	var body map[string]string
	json.Unmarshal(gotBody, &body)
	if body["name"] != "new" {
		t.Errorf("body = %v, want {name: new}", body)
	}
}

func TestClient_ErrorResponse_MapsToSentinel(t *testing.T) {
	tests := []struct {
		status int
		target error
	}{
		{400, directus.ErrBadRequest},
		{401, directus.ErrUnauthorized},
		{403, directus.ErrForbidden},
		{404, directus.ErrNotFound},
		{409, directus.ErrConflict},
		{500, directus.ErrInternal},
	}

	for _, tt := range tests {
		t.Run(tt.target.Error(), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				json.NewEncoder(w).Encode(map[string]any{
					"errors": []map[string]any{
						{"message": "test error"},
					},
				})
			}))
			defer srv.Close()

			client := directus.NewClient(srv.URL, "token")
			_, err := client.Get(context.Background(), "/test", nil)

			if err == nil {
				t.Fatal("expected error, got nil")
			}

			var re *directus.ResponseError
			if !errors.As(err, &re) {
				t.Fatalf("expected ResponseError, got %T", err)
			}

			if !errors.Is(err, tt.target) {
				t.Errorf("errors.Is(%v, %v) = false", err, tt.target)
			}

			if re.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", re.StatusCode, tt.status)
			}
		})
	}
}

func TestClient_Delete_NoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("got method %s, want DELETE", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.Delete(context.Background(), "/items/test/1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
