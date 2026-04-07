package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListShares(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shares" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Share{
			{ID: "s1", Name: "Public Link", Collection: "products", Item: "1"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	shares, err := client.ListShares(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(shares) != 1 || shares[0].Name != "Public Link" {
		t.Errorf("shares = %+v", shares)
	}
}

func TestGetShare(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shares/s1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Share{ID: "s1", Name: "Public Link"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s, err := client.GetShare(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}

	if s.ID != "s1" || s.Name != "Public Link" {
		t.Errorf("share = %+v", s)
	}
}

func TestCreateShare(t *testing.T) {
	var gotBody directus.Share

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/shares" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Share{ID: "s-new", Name: gotBody.Name, Collection: gotBody.Collection})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s, err := client.CreateShare(context.Background(), directus.Share{
		Name:       "New Share",
		Collection: "products",
		Item:       "5",
	})
	if err != nil {
		t.Fatal(err)
	}

	if s.ID != "s-new" {
		t.Errorf("ID = %q", s.ID)
	}

	if gotBody.Name != "New Share" {
		t.Errorf("Name = %q", gotBody.Name)
	}
}

func TestUpdateShare(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/shares/s1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Share{ID: "s1", Name: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s, err := client.UpdateShare(context.Background(), "s1", directus.Share{Name: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if s.Name != "Updated" {
		t.Errorf("Name = %q", s.Name)
	}
}

func TestDeleteShare(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/shares/s1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteShare(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestShareInfo(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shares/info/s1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, map[string]any{"id": "s1", "collection": "products"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	info, err := client.ShareInfo(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}

	if len(info) == 0 {
		t.Error("expected non-empty info")
	}
}
