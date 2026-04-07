package directus_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestClient_BaseURL(t *testing.T) {
	client := directus.NewClient("https://example.com/", "token")
	if client.BaseURL() != "https://example.com" {
		t.Errorf("BaseURL() = %q (trailing slash should be trimmed)", client.BaseURL())
	}
}

func TestClient_Patch(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}

		writeJSONData(w, map[string]any{"updated": true})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	raw, err := client.Patch(context.Background(), "/test", map[string]any{"name": "new"})
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(raw, &result)
	if result["updated"] != true {
		t.Errorf("result = %v", result)
	}
}

func TestClient_ErrorResponse_WithApiErrors(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{
				{"message": "field required", "extensions": map[string]any{"code": "VALIDATION"}},
			},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.Get(context.Background(), "/test", nil)

	var re *directus.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("expected ResponseError, got %T", err)
	}

	if len(re.Errors) != 1 || re.Errors[0].Message != "field required" {
		t.Errorf("errors = %+v", re.Errors)
	}
}

func TestClient_EmptyBody_NoContent(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.Delete(context.Background(), "/test")
	if err != nil {
		t.Fatal(err)
	}
}

func TestClient_Get_WithQueryParams(t *testing.T) {
	var gotQuery string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSONData(w, []any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	query := make(map[string][]string)
	query["limit"] = []string{"10"}
	query["sort"] = []string{"-name"}

	_, err := client.Get(context.Background(), "/items/test", query)
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery == "" {
		t.Error("expected query params")
	}
}
