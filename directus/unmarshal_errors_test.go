package directus_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListRoles_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// data is a string, not an array of objects
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListRoles(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestGetRole_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetRole(context.Background(), "r1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestCreateRole_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.CreateRole(context.Background(), directus.Role{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdateRole_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdateRole(context.Background(), "r1", directus.Role{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestListPolicies_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListPolicies(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestGetPolicy_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetPolicy(context.Background(), "p1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestCreatePolicy_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.CreatePolicy(context.Background(), directus.Policy{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdatePolicy_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdatePolicy(context.Background(), "p1", directus.Policy{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestListPermissions_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListPermissions(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestCreatePermission_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.CreatePermission(context.Background(), directus.Permission{Collection: "x", Action: directus.ActionRead})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdatePermission_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdatePermission(context.Background(), 1, directus.Permission{})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestGetCurrentUser_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetCurrentUser(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdateUser_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdateUser(context.Background(), "u1", directus.User{})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestListUsers_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListUsers(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestListActivity_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListActivity(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestGetActivity_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetActivity(context.Background(), 1)
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestLogin_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.Login(context.Background(), "a@b.com", "pass")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestRefreshToken_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.RefreshToken(context.Background(), "refresh")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestListFlows_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListFlows(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestGetFlow_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetFlow(context.Background(), "f1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestCreateFlow_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.CreateFlow(context.Background(), directus.Flow{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdateFlow_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdateFlow(context.Background(), "f1", directus.Flow{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestListOperations_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListOperations(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestGetOperation_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetOperation(context.Background(), "o1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestCreateOperation_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.CreateOperation(context.Background(), directus.Operation{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdateOperation_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdateOperation(context.Background(), "o1", directus.Operation{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestListFiles_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListFiles(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestGetFile_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetFile(context.Background(), "f1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdateFile_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdateFile(context.Background(), "f1", directus.File{Title: "x"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestImportFile_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ImportFile(context.Background(), directus.ImportFileInput{URL: "http://x"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestListFolders_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.ListFolders(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestGetFolder_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetFolder(context.Background(), "f1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestCreateFolder_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.CreateFolder(context.Background(), directus.Folder{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdateFolder_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdateFolder(context.Background(), "f1", directus.Folder{Name: "test"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestServerHealth_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// ServerHealth has fallback behavior - return something that triggers
		// the unmarshal error but with data that's not a valid ServerHealth
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	// ServerHealth has a fallback: returns {Status: "ok"} on unmarshal error
	health, err := client.ServerHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if health.Status != "ok" {
		t.Errorf("Status = %q", health.Status)
	}
}

func TestGetSettings_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetSettings(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestUpdateSettings_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.UpdateSettings(context.Background(), directus.Settings{ProjectName: "x"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestItems_List_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test")
	_, err := items.List(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestItems_Get_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test")
	_, err := items.Get(context.Background(), "1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestItems_Create_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test")
	_, err := items.Create(context.Background(), &testItem{Name: "x"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestItems_Update_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[testItem](client, "test")
	_, err := items.Update(context.Background(), "1", &testItem{Name: "x"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestSingleton_Get_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")
	_, err := s.Get(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestSingleton_Update_UnmarshalError(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	s := directus.NewSingleton[testSettings](client, "settings")
	_, err := s.Update(context.Background(), &testSettings{})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}
