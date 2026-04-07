package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListPresets(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/presets" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Preset{
			{ID: 1, Bookmark: "My Preset", Collection: "products"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	presets, err := client.ListPresets(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(presets) != 1 || presets[0].Bookmark != "My Preset" {
		t.Errorf("presets = %+v", presets)
	}
}

func TestGetPreset(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/presets/1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Preset{ID: 1, Bookmark: "My Preset"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	p, err := client.GetPreset(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	if p.ID != 1 || p.Bookmark != "My Preset" {
		t.Errorf("preset = %+v", p)
	}
}

func TestCreatePreset(t *testing.T) {
	var gotBody directus.Preset

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/presets" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Preset{ID: 5, Bookmark: gotBody.Bookmark, Collection: gotBody.Collection})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	p, err := client.CreatePreset(context.Background(), directus.Preset{
		Bookmark:   "New Preset",
		Collection: "products",
		Layout:     "tabular",
	})
	if err != nil {
		t.Fatal(err)
	}

	if p.ID != 5 {
		t.Errorf("ID = %d", p.ID)
	}

	if gotBody.Collection != "products" {
		t.Errorf("Collection = %q", gotBody.Collection)
	}
}

func TestUpdatePreset(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/presets/1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Preset{ID: 1, Bookmark: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	p, err := client.UpdatePreset(context.Background(), 1, directus.Preset{Bookmark: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if p.Bookmark != "Updated" {
		t.Errorf("Bookmark = %q", p.Bookmark)
	}
}

func TestDeletePreset(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/presets/1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeletePreset(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
}
