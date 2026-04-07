package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListContentVersions(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/versions" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.ContentVersion{
			{ID: "v1", Name: "Draft", Collection: "products", Item: "1"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	versions, err := client.ListContentVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(versions) != 1 || versions[0].Name != "Draft" {
		t.Errorf("versions = %+v", versions)
	}
}

func TestGetContentVersion(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/versions/v1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.ContentVersion{ID: "v1", Name: "Draft", Key: "my-draft"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	v, err := client.GetContentVersion(context.Background(), "v1")
	if err != nil {
		t.Fatal(err)
	}

	if v.ID != "v1" || v.Key != "my-draft" {
		t.Errorf("version = %+v", v)
	}
}

func TestCreateContentVersion(t *testing.T) {
	var gotBody directus.ContentVersion

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/versions" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.ContentVersion{ID: "v-new", Name: gotBody.Name, Key: gotBody.Key})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	v, err := client.CreateContentVersion(context.Background(), directus.ContentVersion{
		Name:       "Staging",
		Key:        "staging",
		Collection: "products",
		Item:       "5",
	})
	if err != nil {
		t.Fatal(err)
	}

	if v.ID != "v-new" {
		t.Errorf("ID = %q", v.ID)
	}

	if gotBody.Key != "staging" {
		t.Errorf("Key = %q", gotBody.Key)
	}
}

func TestUpdateContentVersion(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/versions/v1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.ContentVersion{ID: "v1", Name: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	v, err := client.UpdateContentVersion(context.Background(), "v1", directus.ContentVersion{Name: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if v.Name != "Updated" {
		t.Errorf("Name = %q", v.Name)
	}
}

func TestDeleteContentVersion(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/versions/v1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteContentVersion(context.Background(), "v1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompareContentVersion(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/versions/v1/compare" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, map[string]any{"current": map[string]any{"name": "A"}, "version": map[string]any{"name": "B"}})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	raw, err := client.CompareContentVersion(context.Background(), "v1")
	if err != nil {
		t.Fatal(err)
	}

	if len(raw) == 0 {
		t.Error("expected non-empty comparison")
	}
}

func TestPromoteContentVersion(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/versions/v1/promote" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.PromoteContentVersion(context.Background(), "v1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSaveContentVersion(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/versions/v1/save" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.SaveContentVersion(context.Background(), "v1", map[string]any{"name": "Updated Name"})
	if err != nil {
		t.Fatal(err)
	}

	if gotBody["name"] != "Updated Name" {
		t.Errorf("body = %v", gotBody)
	}
}
