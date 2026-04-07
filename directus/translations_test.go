package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListTranslations(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/translations" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Translation{
			{ID: "t1", Key: "welcome", Language: "en-US", Value: "Welcome"},
			{ID: "t2", Key: "welcome", Language: "de-DE", Value: "Willkommen"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	translations, err := client.ListTranslations(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(translations) != 2 {
		t.Fatalf("got %d translations, want 2", len(translations))
	}

	if translations[0].Value != "Welcome" {
		t.Errorf("translations[0].Value = %q", translations[0].Value)
	}
}

func TestListTranslations_WithOptions(t *testing.T) {
	var gotQuery string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSONData(w, []directus.Translation{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListTranslations(context.Background(),
		directus.WithFilter(directus.Field("language", "_eq", "en-US")),
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery == "" {
		t.Error("expected query params")
	}
}

func TestGetTranslation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/translations/t1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Translation{ID: "t1", Key: "welcome", Language: "en-US", Value: "Welcome"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	tr, err := client.GetTranslation(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}

	if tr.Key != "welcome" || tr.Value != "Welcome" {
		t.Errorf("translation = %+v", tr)
	}
}

func TestCreateTranslation(t *testing.T) {
	var gotBody directus.Translation

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/translations" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Translation{ID: "t-new", Key: gotBody.Key, Language: gotBody.Language, Value: gotBody.Value})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	tr, err := client.CreateTranslation(context.Background(), directus.Translation{
		Key:      "goodbye",
		Language: "en-US",
		Value:    "Goodbye",
	})
	if err != nil {
		t.Fatal(err)
	}

	if tr.ID != "t-new" {
		t.Errorf("ID = %q", tr.ID)
	}

	if gotBody.Key != "goodbye" {
		t.Errorf("Key = %q", gotBody.Key)
	}
}

func TestUpdateTranslation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/translations/t1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Translation{ID: "t1", Value: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	tr, err := client.UpdateTranslation(context.Background(), "t1", directus.Translation{Value: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if tr.Value != "Updated" {
		t.Errorf("Value = %q", tr.Value)
	}
}

func TestDeleteTranslation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/translations/t1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteTranslation(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
}
