package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListFiles(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files" || r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, []directus.File{
			{ID: "f1", Title: "Photo", Type: "image/jpeg"},
			{ID: "f2", Title: "Document", Type: "application/pdf"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	files, err := client.ListFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}

	if files[0].Title != "Photo" {
		t.Errorf("files[0].Title = %q", files[0].Title)
	}
}

func TestListFiles_WithOptions(t *testing.T) {
	var gotQuery string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSONData(w, []directus.File{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListFiles(context.Background(),
		directus.WithLimit(10),
		directus.WithFilter(directus.Field("type", "_eq", "image/png")),
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery == "" {
		t.Error("expected query params")
	}
}

func TestGetFile(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/f1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.File{ID: "f1", Title: "Photo", Type: "image/jpeg", Filesize: 12345})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	f, err := client.GetFile(context.Background(), "f1")
	if err != nil {
		t.Fatal(err)
	}

	if f.ID != "f1" || f.Title != "Photo" {
		t.Errorf("file = %+v", f)
	}

	if f.Filesize != 12345 {
		t.Errorf("Filesize = %d", f.Filesize)
	}
}

func TestUpdateFile(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/files/f1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.File{ID: "f1", Title: "Updated Photo"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	f, err := client.UpdateFile(context.Background(), "f1", directus.File{Title: "Updated Photo"})
	if err != nil {
		t.Fatal(err)
	}

	if f.Title != "Updated Photo" {
		t.Errorf("Title = %q", f.Title)
	}
}

func TestDeleteFile(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/files/f1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteFile(context.Background(), "f1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestImportFile(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/files/import" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.File{ID: "f-imported", Title: "Imported"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	f, err := client.ImportFile(context.Background(), directus.ImportFileInput{
		URL: "https://example.com/image.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}

	if f.ID != "f-imported" {
		t.Errorf("ID = %q", f.ID)
	}

	if gotBody["url"] != "https://example.com/image.jpg" {
		t.Errorf("url = %v", gotBody["url"])
	}
}

func TestAssetURL(t *testing.T) {
	client := directus.NewClient("https://directus.example.com", "token")

	url := client.AssetURL("file-id-123", "")
	if url != "https://directus.example.com/assets/file-id-123" {
		t.Errorf("url = %q", url)
	}

	url = client.AssetURL("file-id-123", "thumbnail")
	if url != "https://directus.example.com/assets/file-id-123?key=thumbnail" {
		t.Errorf("url = %q", url)
	}
}
