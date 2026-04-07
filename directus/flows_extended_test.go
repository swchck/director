package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestGetFlow(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flows/f1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Flow{ID: "f1", Name: "My Flow", Status: directus.FlowStatusActive})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	flow, err := client.GetFlow(context.Background(), "f1")
	if err != nil {
		t.Fatal(err)
	}

	if flow.ID != "f1" || flow.Name != "My Flow" {
		t.Errorf("flow = %+v", flow)
	}

	if flow.Status != directus.FlowStatusActive {
		t.Errorf("Status = %q", flow.Status)
	}
}

func TestGetFlow_WithQueryOptions(t *testing.T) {
	var gotFields string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotFields = r.URL.Query().Get("fields")
		writeJSONData(w, directus.Flow{ID: "f1"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetFlow(context.Background(), "f1", directus.WithFields("*", "operations.*"))
	if err != nil {
		t.Fatal(err)
	}

	if gotFields != "*,operations.*" {
		t.Errorf("fields = %q", gotFields)
	}
}

func TestUpdateFlow(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/flows/f1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Flow{ID: "f1", Name: "Updated", Status: directus.FlowStatusInactive})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	flow, err := client.UpdateFlow(context.Background(), "f1", directus.Flow{Status: directus.FlowStatusInactive})
	if err != nil {
		t.Fatal(err)
	}

	if flow.Status != directus.FlowStatusInactive {
		t.Errorf("Status = %q", flow.Status)
	}
}

func TestDeleteFlow(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/flows/f1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteFlow(context.Background(), "f1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestTriggerWebhookFlow(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/flows/trigger/f1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{"result": "ok"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	raw, err := client.TriggerWebhookFlow(context.Background(), "f1", map[string]any{"key": "value"})
	if err != nil {
		t.Fatal(err)
	}

	if len(raw) == 0 {
		t.Error("expected non-empty response")
	}

	if gotBody["key"] != "value" {
		t.Errorf("body = %v", gotBody)
	}
}

func TestListOperations(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/operations" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Operation{
			{ID: "o1", Name: "Step 1", Type: directus.OpLog},
			{ID: "o2", Name: "Step 2", Type: directus.OpRequest},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	ops, err := client.ListOperations(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2", len(ops))
	}

	if ops[0].Type != directus.OpLog {
		t.Errorf("ops[0].Type = %q", ops[0].Type)
	}
}

func TestGetOperation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/operations/o1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Operation{ID: "o1", Name: "Log Step", Key: "log_step"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	op, err := client.GetOperation(context.Background(), "o1")
	if err != nil {
		t.Fatal(err)
	}

	if op.ID != "o1" || op.Key != "log_step" {
		t.Errorf("operation = %+v", op)
	}
}

func TestCreateOperation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/operations" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Operation{ID: "o-new", Name: "Log: test", Key: "test", Type: directus.OpLog})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	op := directus.NewLogOperation("test", "Hello World")
	op.Flow = "f1"

	created, err := client.CreateOperation(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}

	if created.ID != "o-new" {
		t.Errorf("ID = %q", created.ID)
	}
}

func TestUpdateOperation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/operations/o1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Operation{ID: "o1", Name: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	op, err := client.UpdateOperation(context.Background(), "o1", directus.Operation{Name: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if op.Name != "Updated" {
		t.Errorf("Name = %q", op.Name)
	}
}

func TestDeleteOperation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/operations/o1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteOperation(context.Background(), "o1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestNewCreateItemOperation(t *testing.T) {
	op := directus.NewCreateItemOperation("create_product", "products", map[string]any{
		"name":  "Test",
		"price": 9.99,
	})

	if op.Type != directus.OpCreate {
		t.Errorf("Type = %q", op.Type)
	}

	if op.Key != "create_product" {
		t.Errorf("Key = %q", op.Key)
	}

	if op.Options["collection"] != "products" {
		t.Errorf("collection = %v", op.Options["collection"])
	}
}

func TestNewConditionOperation(t *testing.T) {
	op := directus.NewConditionOperation("check_status", map[string]any{
		"status": map[string]any{"_eq": "active"},
	})

	if op.Type != directus.OpCondition {
		t.Errorf("Type = %q", op.Type)
	}

	if op.Key != "check_status" {
		t.Errorf("Key = %q", op.Key)
	}
}

func TestWebhookFlow_AsyncOption(t *testing.T) {
	f := directus.NewWebhookFlow("Async WH", directus.WebhookFlowOptions{
		Method: "POST",
		Async:  true,
	})

	if f.Options["async"] != true {
		t.Errorf("async = %v", f.Options["async"])
	}
}

func TestHookFlow_NoCollections(t *testing.T) {
	f := directus.NewHookFlow("Global Hook", directus.HookFlowOptions{
		Type:  "action",
		Scope: []string{"items.create"},
	})

	if _, ok := f.Options["collections"]; ok {
		t.Error("collections should not be set when empty")
	}
}

func TestFlow_ParseOperations_InvalidJSON(t *testing.T) {
	flow := directus.Flow{Operations: json.RawMessage(`not valid json`)}
	_, err := flow.ParseOperations()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
