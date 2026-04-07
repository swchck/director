package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/swchck/director/directus"
)

func errorServer() *httptest.Server {
	return newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "server error"}},
		})
	})
}

// Every Client method must propagate server errors.
func TestClient_ErrorPropagation(t *testing.T) {
	srv := errorServer()
	defer srv.Close()

	c := directus.NewClient(srv.URL, "token")
	ctx := context.Background()
	items := directus.NewItems[testItem](c, "test")

	calls := map[string]func() error{
		// Comments
		"ListComments":  func() error { _, e := c.ListComments(ctx); return e },
		"GetComment":    func() error { _, e := c.GetComment(ctx, "c1"); return e },
		"CreateComment": func() error { _, e := c.CreateComment(ctx, directus.Comment{Comment: "t"}); return e },
		"UpdateComment": func() error { _, e := c.UpdateComment(ctx, "c1", directus.Comment{Comment: "t"}); return e },
		"DeleteComment": func() error { return c.DeleteComment(ctx, "c1") },

		// Dashboards & Panels
		"DeleteDashboard": func() error { return c.DeleteDashboard(ctx, "d1") },
		"DeletePanel":     func() error { return c.DeletePanel(ctx, "p1") },

		// Files & Folders
		"DeleteFile":   func() error { return c.DeleteFile(ctx, "f1") },
		"DeleteFolder": func() error { return c.DeleteFolder(ctx, "f1") },

		// Notifications, Presets, Shares, Translations
		"DeleteNotification": func() error { return c.DeleteNotification(ctx, 1) },
		"DeletePreset":       func() error { return c.DeletePreset(ctx, 1) },
		"DeleteShare":        func() error { return c.DeleteShare(ctx, "s1") },
		"DeleteTranslation":  func() error { return c.DeleteTranslation(ctx, "t1") },

		// Content Versions
		"DeleteContentVersion":  func() error { return c.DeleteContentVersion(ctx, "v1") },
		"CompareContentVersion": func() error { _, e := c.CompareContentVersion(ctx, "v1"); return e },
		"PromoteContentVersion": func() error { return c.PromoteContentVersion(ctx, "v1") },
		"SaveContentVersion":    func() error { return c.SaveContentVersion(ctx, "v1", map[string]any{}) },

		// Flows & Operations
		"DeleteFlow":         func() error { return c.DeleteFlow(ctx, "f1") },
		"DeleteOperation":    func() error { return c.DeleteOperation(ctx, "o1") },
		"TriggerWebhookFlow": func() error { _, e := c.TriggerWebhookFlow(ctx, "f1", nil); return e },

		// Schema
		"DeleteCollection":       func() error { return c.DeleteCollection(ctx, "test") },
		"DeleteField":            func() error { return c.DeleteField(ctx, "products", "name") },
		"DeleteRelation":         func() error { return c.DeleteRelation(ctx, "products", "category_id") },
		"CreateCollection":       func() error { return c.CreateCollection(ctx, directus.CreateCollectionInput{Collection: "t"}) },
		"CreateCollectionFolder": func() error { return c.CreateCollectionFolder(ctx, "t", nil) },
		"CreateField":            func() error { return c.CreateField(ctx, "products", directus.StringField("name")) },
		"UpdateField":            func() error { return c.UpdateField(ctx, "products", "name", directus.FieldInput{}) },
		"CreateRelation":         func() error { return c.CreateRelation(ctx, directus.M2O("x", "y", "z")) },
		"GetRelations":           func() error { _, e := c.GetRelations(ctx, "products"); return e },
		"MoveCollectionToFolder": func() error { return c.MoveCollectionToFolder(ctx, "products", "folder") },
		"SchemaSnapshot":         func() error { _, e := c.SchemaSnapshot(ctx); return e },
		"SchemaDiff":             func() error { _, e := c.SchemaDiff(ctx, json.RawMessage(`{}`), false); return e },
		"SchemaApply":            func() error { return c.SchemaApply(ctx, json.RawMessage(`{}`)) },

		// ACL
		"DeleteRole":       func() error { return c.DeleteRole(ctx, "r1") },
		"DeletePolicy":     func() error { return c.DeletePolicy(ctx, "p1") },
		"DeletePermission": func() error { return c.DeletePermission(ctx, 1) },

		// Server
		"ServerInfo":         func() error { _, e := c.ServerInfo(ctx); return e },
		"ServerHealth":       func() error { _, e := c.ServerHealth(ctx); return e },
		"ServerSpecsOAS":     func() error { _, e := c.ServerSpecsOAS(ctx); return e },
		"ServerSpecsGraphQL": func() error { _, e := c.ServerSpecsGraphQL(ctx); return e },
		"ClearCache":         func() error { return c.ClearCache(ctx) },
		"Metrics":            func() error { _, e := c.Metrics(ctx); return e },
		"HashGenerate":       func() error { _, e := c.HashGenerate(ctx, "test"); return e },
		"HashVerify":         func() error { _, e := c.HashVerify(ctx, "test", "hash"); return e },
		"RandomString":       func() error { _, e := c.RandomString(ctx, 16); return e },
		"SortItems":          func() error { return c.SortItems(ctx, "products", 1, 2) },

		// Shares
		"ShareInfo": func() error { _, e := c.ShareInfo(ctx, "s1"); return e },

		// Auth
		"Logout":               func() error { return c.Logout(ctx, "refresh") },
		"RequestPasswordReset": func() error { return c.RequestPasswordReset(ctx, "test@test.com") },
		"ResetPassword":        func() error { return c.ResetPassword(ctx, "token", "pass") },

		// Items
		"Items.Delete": func() error { return items.Delete(ctx, "1") },
	}

	for name, fn := range calls {
		t.Run(name, func(t *testing.T) {
			if err := fn(); err == nil {
				t.Fatal("expected error from server returning 500")
			}
		})
	}
}

// Generic CRUD helpers must return errors on malformed JSON.
func TestGenericCRUD_UnmarshalErrors(t *testing.T) {
	badList := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an array"}`))
	})
	defer badList.Close()

	badObj := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "not an object"}`))
	})
	defer badObj.Close()

	ctx := context.Background()

	t.Run("list", func(t *testing.T) {
		c := directus.NewClient(badList.URL, "token")
		if _, err := c.ListComments(ctx); err == nil {
			t.Fatal("expected unmarshal error")
		}
	})

	t.Run("get", func(t *testing.T) {
		c := directus.NewClient(badObj.URL, "token")
		if _, err := c.GetComment(ctx, "c1"); err == nil {
			t.Fatal("expected unmarshal error")
		}
	})

	t.Run("create", func(t *testing.T) {
		c := directus.NewClient(badObj.URL, "token")
		if _, err := c.CreateComment(ctx, directus.Comment{Comment: "t"}); err == nil {
			t.Fatal("expected unmarshal error")
		}
	})

	t.Run("update", func(t *testing.T) {
		c := directus.NewClient(badObj.URL, "token")
		if _, err := c.UpdateComment(ctx, "c1", directus.Comment{Comment: "t"}); err == nil {
			t.Fatal("expected unmarshal error")
		}
	})
}
