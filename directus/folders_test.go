package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestCreateFolder(t *testing.T) {
	var gotBody directus.Folder

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folders" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{"id": "folder-1", "name": "Photos"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	folder, err := client.CreateFolder(context.Background(), directus.Folder{Name: "Photos"})
	if err != nil {
		t.Fatal(err)
	}

	if folder.ID != "folder-1" {
		t.Errorf("folder = %+v", folder)
	}

	if gotBody.Name != "Photos" {
		t.Errorf("sent = %+v", gotBody)
	}
}

func TestCreateFolder_Nested(t *testing.T) {
	var gotBody directus.Folder

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{"id": "folder-2", "name": "Vacation"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	parentID := "folder-1"
	_, err := client.CreateFolder(context.Background(), directus.Folder{Name: "Vacation", Parent: &parentID})
	if err != nil {
		t.Fatal(err)
	}

	if gotBody.Parent == nil || *gotBody.Parent != "folder-1" {
		t.Error("parent not set")
	}
}

func TestListFolders(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		writeJSONData(w, []directus.Folder{
			{ID: "f1", Name: "Photos"},
			{ID: "f2", Name: "Docs"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	folders, err := client.ListFolders(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(folders) != 2 {
		t.Errorf("got %d folders", len(folders))
	}
}

func TestDeleteFolder(t *testing.T) {
	var gotPath string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteFolder(context.Background(), "folder-1")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/folders/folder-1" {
		t.Errorf("path = %s", gotPath)
	}
}
