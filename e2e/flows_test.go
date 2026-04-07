//go:build e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/swchck/director/directus"
)

func TestE2E_FlowCRUDLifecycle(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a webhook flow.
	flow := directus.NewWebhookFlow("E2E Test Flow", directus.WebhookFlowOptions{
		Method: "POST",
	})

	created, err := dc.CreateFlow(ctx, flow)
	if err != nil {
		t.Fatalf("create flow: %v", err)
	}

	t.Cleanup(func() {
		_ = dc.DeleteFlow(context.Background(), created.ID)
	})

	if created.ID == "" {
		t.Fatal("created flow has empty ID")
	}

	if created.Name != "E2E Test Flow" {
		t.Errorf("name = %q, want 'E2E Test Flow'", created.Name)
	}

	if created.Trigger != directus.TriggerWebhook {
		t.Errorf("trigger = %q, want 'webhook'", created.Trigger)
	}

	t.Logf("created flow: id=%s", created.ID)

	// Get flow by ID.
	got, err := dc.GetFlow(ctx, created.ID)
	if err != nil {
		t.Fatalf("get flow: %v", err)
	}

	if got.Name != "E2E Test Flow" {
		t.Errorf("get: name = %q", got.Name)
	}

	// Update flow.
	updated, err := dc.UpdateFlow(ctx, created.ID, directus.Flow{
		Name:        "E2E Updated Flow",
		Description: "Updated via API",
	})
	if err != nil {
		t.Fatalf("update flow: %v", err)
	}

	if updated.Name != "E2E Updated Flow" {
		t.Errorf("update: name = %q", updated.Name)
	}

	// List flows.
	flows, err := dc.ListFlows(ctx)
	if err != nil {
		t.Fatalf("list flows: %v", err)
	}

	found := false
	for _, f := range flows {
		if f.ID == created.ID {
			found = true
			break
		}
	}

	if !found {
		t.Error("created flow not in list")
	}

	// Delete.
	if err := dc.DeleteFlow(ctx, created.ID); err != nil {
		t.Fatalf("delete flow: %v", err)
	}

	// Verify deleted.
	_, err = dc.GetFlow(ctx, created.ID)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestE2E_FlowWithOperations(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a manual flow.
	flow := directus.NewManualFlow("E2E Flow With Ops")
	createdFlow, err := dc.CreateFlow(ctx, flow)
	if err != nil {
		t.Fatalf("create flow: %v", err)
	}

	t.Cleanup(func() {
		_ = dc.DeleteFlow(context.Background(), createdFlow.ID)
	})

	// Create a log operation.
	logOp := directus.NewLogOperation("log_step", "Hello from e2e test")
	logOp.Flow = createdFlow.ID
	logOp.PositionX = 20
	logOp.PositionY = 1

	createdOp, err := dc.CreateOperation(ctx, logOp)
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}

	if createdOp.ID == "" {
		t.Fatal("operation has empty ID")
	}

	if createdOp.Key != "log_step" {
		t.Errorf("op key = %q, want 'log_step'", createdOp.Key)
	}

	t.Logf("created operation: id=%s, key=%s", createdOp.ID, createdOp.Key)

	// Link flow to its first operation.
	_, err = dc.UpdateFlow(ctx, createdFlow.ID, directus.Flow{
		Operation: &createdOp.ID,
	})
	if err != nil {
		t.Fatalf("link flow to operation: %v", err)
	}

	// Create a second operation chained from the first.
	secondOp := directus.Operation{
		Name: "Second Step",
		Key:  "second_step",
		Type: directus.OpLog,
		Flow: createdFlow.ID,
		Options: map[string]any{
			"message": "Second step reached",
		},
		PositionX: 40,
		PositionY: 1,
	}

	createdSecondOp, err := dc.CreateOperation(ctx, secondOp)
	if err != nil {
		t.Fatalf("create second operation: %v", err)
	}

	// Chain: first → second on success.
	_, err = dc.UpdateOperation(ctx, createdOp.ID, directus.Operation{
		Resolve: &createdSecondOp.ID,
	})
	if err != nil {
		t.Fatalf("chain operations: %v", err)
	}

	// Verify the full flow with operations.
	fullFlow, err := dc.GetFlow(ctx, createdFlow.ID,
		directus.WithFields("*", "operations.*"),
	)
	if err != nil {
		t.Fatalf("get full flow: %v", err)
	}

	if fullFlow.Operation == nil || *fullFlow.Operation == "" {
		t.Error("flow has no linked operation")
	}

	parsedOps, err := fullFlow.ParseOperations()
	if err != nil {
		t.Fatalf("parse operations: %v", err)
	}

	if len(parsedOps) != 2 {
		t.Errorf("flow has %d operations, want 2", len(parsedOps))
	}

	// Verify chain via GetOperation.
	chainedOp, err := dc.GetOperation(ctx, createdOp.ID)
	if err != nil {
		t.Fatalf("get chained operation: %v", err)
	}

	if chainedOp.Resolve == nil || *chainedOp.Resolve != createdSecondOp.ID {
		t.Errorf("first op resolve = %v, want %s", chainedOp.Resolve, createdSecondOp.ID)
	}
}

func TestE2E_HookFlow(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a hook flow that triggers on item creation.
	flow := directus.NewHookFlow("E2E Hook Flow", directus.HookFlowOptions{
		Type:        "action",
		Scope:       []string{"items.create"},
		Collections: []string{"directus_users"}, // safe system collection
	})

	created, err := dc.CreateFlow(ctx, flow)
	if err != nil {
		t.Fatalf("create hook flow: %v", err)
	}

	t.Cleanup(func() {
		_ = dc.DeleteFlow(context.Background(), created.ID)
	})

	if created.Trigger != directus.TriggerHook {
		t.Errorf("trigger = %q, want 'hook'", created.Trigger)
	}

	// Verify options were stored.
	got, err := dc.GetFlow(ctx, created.ID)
	if err != nil {
		t.Fatalf("get flow: %v", err)
	}

	if got.Options == nil {
		t.Fatal("options is nil")
	}

	if got.Options["type"] != "action" {
		t.Errorf("options.type = %v, want 'action'", got.Options["type"])
	}

	scope, ok := got.Options["scope"].([]any)
	if !ok || len(scope) == 0 {
		t.Errorf("options.scope = %v, want [items.create]", got.Options["scope"])
	}
}

func TestE2E_ScheduleFlow(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flow := directus.NewScheduleFlow("E2E Schedule Flow", directus.ScheduleFlowOptions{
		Cron: "0 * * * *",
	})

	created, err := dc.CreateFlow(ctx, flow)
	if err != nil {
		t.Fatalf("create schedule flow: %v", err)
	}

	t.Cleanup(func() {
		_ = dc.DeleteFlow(context.Background(), created.ID)
	})

	if created.Trigger != directus.TriggerSchedule {
		t.Errorf("trigger = %q, want 'schedule'", created.Trigger)
	}

	got, err := dc.GetFlow(ctx, created.ID)
	if err != nil {
		t.Fatalf("get flow: %v", err)
	}

	if got.Options["cron"] != "0 * * * *" {
		t.Errorf("options.cron = %v, want '0 * * * *'", got.Options["cron"])
	}
}
