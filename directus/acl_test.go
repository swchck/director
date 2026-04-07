package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListRoles(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/roles" || r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, []directus.Role{{ID: "r1", Name: "Admin"}})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	roles, err := client.ListRoles(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(roles) != 1 || roles[0].Name != "Admin" {
		t.Errorf("roles = %+v", roles)
	}
}

func TestListPolicies(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		writeJSONData(w, []directus.Policy{{ID: "p1", Name: "Admin", AdminAccess: true}})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	policies, err := client.ListPolicies(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(policies) != 1 || !policies[0].AdminAccess {
		t.Errorf("policies = %+v", policies)
	}
}

func TestGrantAdminAccess_UpdatesPolicy(t *testing.T) {
	var patchCalled bool

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/policies":
			writeJSONData(w, []directus.Policy{
				{ID: "p1", Name: "Administrator", AdminAccess: false},
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/policies/p1":
			patchCalled = true
			var body directus.Policy
			json.NewDecoder(r.Body).Decode(&body)

			if !body.AdminAccess {
				t.Error("expected AdminAccess=true in patch")
			}

			writeJSONData(w, body)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.GrantAdminAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !patchCalled {
		t.Error("PATCH was not called")
	}
}

func TestGrantFullAccess_Creates4Permissions(t *testing.T) {
	var created int

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/permissions" {
			created++
			writeJSONData(w, map[string]any{"id": created})
		}
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.GrantFullAccess(context.Background(), "policy-1", "products")
	if err != nil {
		t.Fatal(err)
	}

	if created != 4 {
		t.Errorf("created %d permissions, want 4 (CRUD)", created)
	}
}

func TestGetCurrentUser(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.User{ID: "u1", Email: "admin@test.com"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	user, err := client.GetCurrentUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if user.Email != "admin@test.com" {
		t.Errorf("user = %+v", user)
	}
}
