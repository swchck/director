package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListComments(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/comments" || r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, []directus.Comment{
			{ID: "c1", Comment: "First comment", Collection: "products", Item: "1"},
			{ID: "c2", Comment: "Second comment", Collection: "products", Item: "2"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	comments, err := client.ListComments(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments))
	}

	if comments[0].Comment != "First comment" {
		t.Errorf("comments[0].Comment = %q", comments[0].Comment)
	}
}

func TestGetComment(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/comments/c1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Comment{ID: "c1", Comment: "Test"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	c, err := client.GetComment(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}

	if c.ID != "c1" || c.Comment != "Test" {
		t.Errorf("comment = %+v", c)
	}
}

func TestCreateComment(t *testing.T) {
	var gotBody directus.Comment

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/comments" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Comment{ID: "c-new", Comment: gotBody.Comment, Collection: gotBody.Collection})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	created, err := client.CreateComment(context.Background(), directus.Comment{
		Comment:    "New comment",
		Collection: "products",
		Item:       "5",
	})
	if err != nil {
		t.Fatal(err)
	}

	if created.ID != "c-new" {
		t.Errorf("created.ID = %q", created.ID)
	}

	if gotBody.Comment != "New comment" {
		t.Errorf("sent Comment = %q", gotBody.Comment)
	}
}

func TestUpdateComment(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/comments/c1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Comment{ID: "c1", Comment: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	updated, err := client.UpdateComment(context.Background(), "c1", directus.Comment{Comment: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if updated.Comment != "Updated" {
		t.Errorf("Comment = %q", updated.Comment)
	}
}

func TestDeleteComment(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/comments/c1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteComment(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
}
