package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestServerHealth(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server/health" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.ServerHealth{Status: "ok"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	health, err := client.ServerHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if health.Status != "ok" {
		t.Errorf("Status = %q", health.Status)
	}
}

func TestServerInfo(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server/info" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, map[string]any{
			"project": map[string]any{"project_name": "Test"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	info, err := client.ServerInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(info) == 0 {
		t.Error("expected non-empty info")
	}
}

func TestServerPing(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server/ping" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, "pong")
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.ServerPing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func TestServerSpecsOAS(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server/specs/oas" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, map[string]any{"openapi": "3.0.0"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	spec, err := client.ServerSpecsOAS(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(spec) == 0 {
		t.Error("expected non-empty spec")
	}
}

func TestServerSpecsGraphQL(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server/specs/graphql" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, "type Query { }")
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	sdl, err := client.ServerSpecsGraphQL(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(sdl) == 0 {
		t.Error("expected non-empty SDL")
	}
}

func TestGetSettings(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settings" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Settings{
			ID:          1,
			ProjectName: "Test Project",
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s, err := client.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if s.ProjectName != "Test Project" {
		t.Errorf("ProjectName = %q", s.ProjectName)
	}
}

func TestUpdateSettings(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/settings" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Settings{ID: 1, ProjectName: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s, err := client.UpdateSettings(context.Background(), directus.Settings{ProjectName: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if s.ProjectName != "Updated" {
		t.Errorf("ProjectName = %q", s.ProjectName)
	}
}

func TestHashGenerate(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/utils/hash/generate" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, "$argon2id$v=19$...")
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	hash, err := client.HashGenerate(context.Background(), "password123")
	if err != nil {
		t.Fatal(err)
	}

	if hash == "" {
		t.Error("expected non-empty hash")
	}
}

func TestHashVerify(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/utils/hash/verify" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, true)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	ok, err := client.HashVerify(context.Background(), "password123", "$argon2id$...")
	if err != nil {
		t.Fatal(err)
	}

	if !ok {
		t.Error("expected true")
	}
}

func TestRandomString(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}

		writeJSONData(w, "abcdef1234567890")
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s, err := client.RandomString(context.Background(), 16)
	if err != nil {
		t.Fatal(err)
	}

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func TestClearCache(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/utils/cache/clear" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.ClearCache(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func TestSortItems(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/utils/sort/products" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.SortItems(context.Background(), "products", 5, 2)
	if err != nil {
		t.Fatal(err)
	}

	if gotBody["item"] != float64(5) || gotBody["to"] != float64(2) {
		t.Errorf("body = %v", gotBody)
	}
}

func TestSchemaSnapshot(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/schema/snapshot" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, map[string]any{"collections": []any{}})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	snap, err := client.SchemaSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(snap) == 0 {
		t.Error("expected non-empty snapshot")
	}
}

func TestSchemaDiff(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}

		writeJSONData(w, map[string]any{"diff": []any{}})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	diff, err := client.SchemaDiff(context.Background(), json.RawMessage(`{"collections":[]}`), false)
	if err != nil {
		t.Fatal(err)
	}

	if len(diff) == 0 {
		t.Error("expected non-empty diff")
	}
}

func TestSchemaDiff_Force(t *testing.T) {
	var gotPath string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		writeJSONData(w, map[string]any{"diff": []any{}})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.SchemaDiff(context.Background(), json.RawMessage(`{}`), true)
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/schema/diff?force=true" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestSchemaApply(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/schema/apply" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.SchemaApply(context.Background(), json.RawMessage(`{"diff":[]}`))
	if err != nil {
		t.Fatal(err)
	}
}
