package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestGetFolder(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/folders/f1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Folder{ID: "f1", Name: "Photos"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	folder, err := client.GetFolder(context.Background(), "f1")
	if err != nil {
		t.Fatal(err)
	}

	if folder.ID != "f1" || folder.Name != "Photos" {
		t.Errorf("folder = %+v", folder)
	}
}

func TestUpdateFolder(t *testing.T) {
	var gotBody directus.Folder

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/folders/f1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Folder{ID: "f1", Name: "Renamed"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	folder, err := client.UpdateFolder(context.Background(), "f1", directus.Folder{Name: "Renamed"})
	if err != nil {
		t.Fatal(err)
	}

	if folder.Name != "Renamed" {
		t.Errorf("Name = %q", folder.Name)
	}

	if gotBody.Name != "Renamed" {
		t.Errorf("sent Name = %q", gotBody.Name)
	}
}

func TestGetFolder_NotFound(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "not found"}},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetFolder(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListFolders_WithOptions(t *testing.T) {
	var gotQuery string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSONData(w, []directus.Folder{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListFolders(context.Background(),
		directus.WithSort("name"),
		directus.WithLimit(10),
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery == "" {
		t.Error("expected query params")
	}
}
