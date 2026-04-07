package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListNotifications(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notifications" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Notification{
			{ID: 1, Subject: "Welcome", Status: "inbox"},
			{ID: 2, Subject: "Update", Status: "inbox"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	notifs, err := client.ListNotifications(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(notifs) != 2 {
		t.Fatalf("got %d notifications, want 2", len(notifs))
	}

	if notifs[0].Subject != "Welcome" {
		t.Errorf("notifs[0].Subject = %q", notifs[0].Subject)
	}
}

func TestGetNotification(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notifications/1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Notification{ID: 1, Subject: "Welcome"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	n, err := client.GetNotification(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	if n.ID != 1 || n.Subject != "Welcome" {
		t.Errorf("notification = %+v", n)
	}
}

func TestCreateNotification(t *testing.T) {
	var gotBody directus.Notification

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/notifications" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Notification{ID: 10, Subject: gotBody.Subject, Recipient: gotBody.Recipient})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	n, err := client.CreateNotification(context.Background(), directus.Notification{
		Subject:   "Hello",
		Recipient: "user-1",
		Message:   "Test message",
	})
	if err != nil {
		t.Fatal(err)
	}

	if n.ID != 10 {
		t.Errorf("ID = %d", n.ID)
	}

	if gotBody.Subject != "Hello" {
		t.Errorf("Subject = %q", gotBody.Subject)
	}
}

func TestUpdateNotification(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/notifications/1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Notification{ID: 1, Status: "archived"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	n, err := client.UpdateNotification(context.Background(), 1, directus.Notification{Status: "archived"})
	if err != nil {
		t.Fatal(err)
	}

	if n.Status != "archived" {
		t.Errorf("Status = %q", n.Status)
	}
}

func TestDeleteNotification(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/notifications/1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteNotification(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
}
