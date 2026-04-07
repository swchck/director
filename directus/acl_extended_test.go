package directus_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestGetRole(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/roles/r1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Role{ID: "r1", Name: "Editor"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	role, err := client.GetRole(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}

	if role.ID != "r1" || role.Name != "Editor" {
		t.Errorf("role = %+v", role)
	}
}

func TestCreateRole(t *testing.T) {
	var gotBody directus.Role

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/roles" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Role{ID: "r-new", Name: gotBody.Name})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	role, err := client.CreateRole(context.Background(), directus.Role{Name: "New Role", Icon: "shield"})
	if err != nil {
		t.Fatal(err)
	}

	if role.ID != "r-new" {
		t.Errorf("ID = %q", role.ID)
	}

	if gotBody.Name != "New Role" {
		t.Errorf("Name = %q", gotBody.Name)
	}
}

func TestUpdateRole(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/roles/r1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Role{ID: "r1", Name: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	role, err := client.UpdateRole(context.Background(), "r1", directus.Role{Name: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if role.Name != "Updated" {
		t.Errorf("Name = %q", role.Name)
	}
}

func TestDeleteRole(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/roles/r1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteRole(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetPolicy(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies/p1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Policy{ID: "p1", Name: "Admin", AdminAccess: true})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	policy, err := client.GetPolicy(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}

	if policy.ID != "p1" || !policy.AdminAccess {
		t.Errorf("policy = %+v", policy)
	}
}

func TestCreatePolicy(t *testing.T) {
	var gotBody directus.Policy

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/policies" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Policy{ID: "p-new", Name: gotBody.Name, AdminAccess: gotBody.AdminAccess})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	policy, err := client.CreatePolicy(context.Background(), directus.Policy{
		Name:        "Editor Policy",
		AdminAccess: false,
		AppAccess:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if policy.ID != "p-new" {
		t.Errorf("ID = %q", policy.ID)
	}
}

func TestUpdatePolicy(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/policies/p1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Policy{ID: "p1", Name: "Updated", AdminAccess: true})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	policy, err := client.UpdatePolicy(context.Background(), "p1", directus.Policy{Name: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if policy.Name != "Updated" {
		t.Errorf("Name = %q", policy.Name)
	}
}

func TestDeletePolicy(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/policies/p1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeletePolicy(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestGrantAdminAccess_NoUpdate_WhenAlreadyAdmin(t *testing.T) {
	var patchCalled bool

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/policies":
			writeJSONData(w, []directus.Policy{
				{ID: "p1", Name: "Administrator", AdminAccess: true},
			})
		case r.Method == http.MethodPatch:
			patchCalled = true
			writeJSONData(w, map[string]any{})
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

	if patchCalled {
		t.Error("PATCH should not be called when already admin")
	}
}

func TestGrantAdminAccess_NoAdministratorPolicy(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		writeJSONData(w, []directus.Policy{
			{ID: "p1", Name: "Editor", AdminAccess: false},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.GrantAdminAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func TestListPermissions(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/permissions" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Permission{
			{ID: 1, Collection: "products", Action: directus.ActionRead, Policy: "p1"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	perms, err := client.ListPermissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(perms) != 1 || perms[0].Action != directus.ActionRead {
		t.Errorf("permissions = %+v", perms)
	}
}

func TestCreatePermission(t *testing.T) {
	var gotBody directus.Permission

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/permissions" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Permission{ID: 10, Collection: gotBody.Collection, Action: gotBody.Action})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	perm, err := client.CreatePermission(context.Background(), directus.Permission{
		Collection: "products",
		Action:     directus.ActionCreate,
		Policy:     "p1",
		Fields:     []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if perm.ID != 10 {
		t.Errorf("ID = %d", perm.ID)
	}

	if gotBody.Action != directus.ActionCreate {
		t.Errorf("Action = %q", gotBody.Action)
	}
}

func TestUpdatePermission(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/permissions/1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Permission{ID: 1, Collection: "products", Action: directus.ActionRead})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	perm, err := client.UpdatePermission(context.Background(), 1, directus.Permission{Fields: []string{"id", "name"}})
	if err != nil {
		t.Fatal(err)
	}

	if perm.ID != 1 {
		t.Errorf("ID = %d", perm.ID)
	}
}

func TestDeletePermission(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/permissions/1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeletePermission(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGrantFullAccess_Error(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "forbidden"}},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.GrantFullAccess(context.Background(), "policy-1", "products")
	if err == nil {
		t.Fatal("expected error")
	}

	if !errors.Is(err, directus.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestUpdateUser(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/users/u1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.User{ID: "u1", FirstName: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	user, err := client.UpdateUser(context.Background(), "u1", directus.User{FirstName: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if user.FirstName != "Updated" {
		t.Errorf("FirstName = %q", user.FirstName)
	}
}

func TestListUsers(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.User{
			{ID: "u1", Email: "admin@test.com"},
			{ID: "u2", Email: "editor@test.com"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	users, err := client.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}

	if users[0].Email != "admin@test.com" {
		t.Errorf("users[0].Email = %q", users[0].Email)
	}
}
