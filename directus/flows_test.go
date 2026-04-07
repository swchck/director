package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestCreateFlow(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{"id": "f1", "name": "Test Flow"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	flow, err := client.CreateFlow(context.Background(), directus.NewManualFlow("Test Flow"))
	if err != nil {
		t.Fatal(err)
	}

	if flow.ID != "f1" || flow.Name != "Test Flow" {
		t.Errorf("flow = %+v", flow)
	}

	if gotBody["name"] != "Test Flow" {
		t.Errorf("sent name = %v", gotBody["name"])
	}
}

func TestFlowBuilders(t *testing.T) {
	t.Run("manual", func(t *testing.T) {
		f := directus.NewManualFlow("Manual")
		if f.Trigger != directus.TriggerManual || f.Name != "Manual" {
			t.Errorf("manual = %+v", f)
		}
	})

	t.Run("webhook", func(t *testing.T) {
		f := directus.NewWebhookFlow("WH", directus.WebhookFlowOptions{Method: "POST"})
		if f.Trigger != directus.TriggerWebhook {
			t.Errorf("trigger = %s", f.Trigger)
		}

		if f.Options["method"] != "POST" {
			t.Errorf("options = %v", f.Options)
		}
	})

	t.Run("schedule", func(t *testing.T) {
		f := directus.NewScheduleFlow("Hourly", directus.ScheduleFlowOptions{Cron: "0 * * * *"})
		if f.Trigger != directus.TriggerSchedule || f.Options["cron"] != "0 * * * *" {
			t.Errorf("schedule = %+v", f)
		}
	})

	t.Run("hook", func(t *testing.T) {
		f := directus.NewHookFlow("OnCreate", directus.HookFlowOptions{
			Type:        "action",
			Scope:       []string{"items.create"},
			Collections: []string{"products"},
		})

		if f.Trigger != directus.TriggerHook {
			t.Errorf("trigger = %s", f.Trigger)
		}

		if f.Options["type"] != "action" {
			t.Errorf("options = %v", f.Options)
		}
	})
}

func TestOperationBuilders(t *testing.T) {
	op := directus.NewLogOperation("log_it", "Hello")
	if op.Type != directus.OpLog || op.Key != "log_it" {
		t.Errorf("log op = %+v", op)
	}

	op = directus.NewRequestOperation("req", "POST", "https://example.com")
	if op.Type != directus.OpRequest {
		t.Errorf("request op type = %s", op.Type)
	}

	if op.Options["url"] != "https://example.com" {
		t.Errorf("request op options = %v", op.Options)
	}
}

func TestFlow_ParseOperations_FullObjects(t *testing.T) {
	ops := []directus.Operation{{ID: "o1", Key: "step1"}, {ID: "o2", Key: "step2"}}
	data, _ := json.Marshal(ops)

	flow := directus.Flow{Operations: data}
	parsed, err := flow.ParseOperations()
	if err != nil {
		t.Fatal(err)
	}

	if len(parsed) != 2 || parsed[0].Key != "step1" {
		t.Errorf("parsed = %+v", parsed)
	}
}

func TestFlow_ParseOperations_UUIDs(t *testing.T) {
	ids := []string{"uuid-1", "uuid-2"}
	data, _ := json.Marshal(ids)

	flow := directus.Flow{Operations: data}
	parsed, err := flow.ParseOperations()
	if err != nil {
		t.Fatal(err)
	}

	if len(parsed) != 2 || parsed[0].ID != "uuid-1" {
		t.Errorf("parsed = %+v", parsed)
	}
}

func TestFlow_ParseOperations_Empty(t *testing.T) {
	flow := directus.Flow{}
	parsed, err := flow.ParseOperations()
	if err != nil || parsed != nil {
		t.Errorf("empty: parsed=%v, err=%v", parsed, err)
	}
}

func TestListFlows(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		writeJSONData(w, []map[string]any{{"id": "f1", "name": "Flow A"}})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	flows, err := client.ListFlows(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(flows) != 1 || flows[0].Name != "Flow A" {
		t.Errorf("flows = %+v", flows)
	}
}
