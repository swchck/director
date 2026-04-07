package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// TriggerType defines how a flow is activated.
type TriggerType string

const (
	TriggerHook      TriggerType = "hook"
	TriggerWebhook   TriggerType = "webhook"
	TriggerOperation TriggerType = "operation"
	TriggerSchedule  TriggerType = "schedule"
	TriggerManual    TriggerType = "manual"
)

// FlowStatus defines the flow's state.
type FlowStatus string

const (
	FlowStatusActive   FlowStatus = "active"
	FlowStatusInactive FlowStatus = "inactive"
)

// FlowAccountability defines the permission context for flow execution.
type FlowAccountability string

const (
	// AccountabilityPublic runs with public permissions.
	AccountabilityPublic FlowAccountability = "$public"
	// AccountabilityTrigger runs with the permissions of the user who triggered the flow.
	AccountabilityTrigger FlowAccountability = "$trigger"
	// AccountabilityFull runs with full admin permissions.
	AccountabilityFull FlowAccountability = "$full"
)

// Flow represents a Directus automation flow.
type Flow struct {
	ID             string             `json:"id,omitempty"`
	Name           string             `json:"name,omitempty"`
	Icon           string             `json:"icon,omitempty"`
	Color          string             `json:"color,omitempty"`
	Description    string             `json:"description,omitempty"`
	Status         FlowStatus         `json:"status,omitempty"`
	Trigger        TriggerType        `json:"trigger,omitempty"`
	Accountability FlowAccountability `json:"accountability,omitempty"`
	Options        map[string]any     `json:"options,omitempty"`
	// Operation is the UUID of the first operation in the chain.
	Operation *string `json:"operation,omitempty"`
	// Operations contains the flow's operations.
	// When fetched with WithFields("*", "operations.*"), these are full Operation objects.
	// In some responses (e.g. PATCH), they may be just UUID strings.
	Operations json.RawMessage `json:"operations,omitempty"`

	DateCreated string `json:"date_created,omitempty"`
	UserCreated string `json:"user_created,omitempty"`
}

// ParseOperations parses the Operations field into typed Operation objects.
// This is needed because the operations field can contain full objects
// (when using WithFields("*", "operations.*")) or just UUID strings.
func (f *Flow) ParseOperations() ([]Operation, error) {
	if len(f.Operations) == 0 {
		return nil, nil
	}

	var ops []Operation
	if err := json.Unmarshal(f.Operations, &ops); err != nil {
		// Try as string array (UUID list).
		var ids []string
		if err2 := json.Unmarshal(f.Operations, &ids); err2 != nil {
			return nil, fmt.Errorf("directus: parse operations: %w", err)
		}

		ops = make([]Operation, len(ids))
		for i, id := range ids {
			ops[i] = Operation{ID: id}
		}
	}

	return ops, nil
}

// ListFlows returns all flows.
func (c *Client) ListFlows(ctx context.Context, opts ...QueryOption) ([]Flow, error) {
	query, err := buildQuery(opts)
	if err != nil {
		return nil, err
	}

	raw, err := c.Get(ctx, "flows", query)
	if err != nil {
		return nil, fmt.Errorf("directus: list flows: %w", err)
	}

	var flows []Flow
	if err := json.Unmarshal(raw, &flows); err != nil {
		return nil, fmt.Errorf("directus: unmarshal flows: %w", err)
	}

	return flows, nil
}

// GetFlow returns a flow by ID.
func (c *Client) GetFlow(ctx context.Context, id string, opts ...QueryOption) (*Flow, error) {
	query, err := buildQuery(opts)
	if err != nil {
		return nil, err
	}

	raw, err := c.Get(ctx, "flows/"+id, query)
	if err != nil {
		return nil, fmt.Errorf("directus: get flow %s: %w", id, err)
	}

	var flow Flow
	if err := json.Unmarshal(raw, &flow); err != nil {
		return nil, fmt.Errorf("directus: unmarshal flow: %w", err)
	}

	return &flow, nil
}

// CreateFlow creates a new flow.
func (c *Client) CreateFlow(ctx context.Context, flow Flow) (*Flow, error) {
	raw, err := c.Post(ctx, "flows", flow)
	if err != nil {
		return nil, fmt.Errorf("directus: create flow: %w", err)
	}

	var created Flow
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("directus: unmarshal created flow: %w", err)
	}

	return &created, nil
}

// UpdateFlow updates an existing flow.
func (c *Client) UpdateFlow(ctx context.Context, id string, flow Flow) (*Flow, error) {
	raw, err := c.Patch(ctx, "flows/"+id, flow)
	if err != nil {
		return nil, fmt.Errorf("directus: update flow %s: %w", id, err)
	}

	var updated Flow
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal updated flow: %w", err)
	}

	return &updated, nil
}

// DeleteFlow removes a flow and all its operations.
func (c *Client) DeleteFlow(ctx context.Context, id string) error {
	if err := c.Delete(ctx, "flows/"+id); err != nil {
		return fmt.Errorf("directus: delete flow %s: %w", id, err)
	}

	return nil
}

// TriggerWebhookFlow triggers a flow via its webhook endpoint.
func (c *Client) TriggerWebhookFlow(ctx context.Context, flowID string, payload any) (json.RawMessage, error) {
	raw, err := c.Post(ctx, "flows/trigger/"+flowID, payload)
	if err != nil {
		return nil, fmt.Errorf("directus: trigger flow %s: %w", flowID, err)
	}

	return raw, nil
}

// OperationType defines the kind of operation.
type OperationType string

const (
	OpLog          OperationType = "log"
	OpMail         OperationType = "mail"
	OpNotification OperationType = "notification"
	OpCreate       OperationType = "item-create"
	OpRead         OperationType = "item-read"
	OpUpdate       OperationType = "item-update"
	OpDelete       OperationType = "item-delete"
	OpRequest      OperationType = "request"
	OpSleep        OperationType = "sleep"
	OpTransform    OperationType = "transform"
	OpTrigger      OperationType = "trigger"
	OpCondition    OperationType = "condition"
	OpExec         OperationType = "exec"
)

// Operation represents a step within a Directus flow.
type Operation struct {
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Key       string         `json:"key,omitempty"`
	Type      OperationType  `json:"type,omitempty"`
	PositionX int            `json:"position_x,omitempty"`
	PositionY int            `json:"position_y,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
	// Flow is the parent flow UUID.
	Flow string `json:"flow,omitempty"`
	// Resolve is the next operation UUID on success.
	Resolve *string `json:"resolve,omitempty"`
	// Reject is the next operation UUID on failure.
	Reject *string `json:"reject,omitempty"`

	DateCreated string `json:"date_created,omitempty"`
	UserCreated string `json:"user_created,omitempty"`
}

// ListOperations returns all operations, optionally filtered.
func (c *Client) ListOperations(ctx context.Context, opts ...QueryOption) ([]Operation, error) {
	query, err := buildQuery(opts)
	if err != nil {
		return nil, err
	}

	raw, err := c.Get(ctx, "operations", query)
	if err != nil {
		return nil, fmt.Errorf("directus: list operations: %w", err)
	}

	var ops []Operation
	if err := json.Unmarshal(raw, &ops); err != nil {
		return nil, fmt.Errorf("directus: unmarshal operations: %w", err)
	}

	return ops, nil
}

// GetOperation returns an operation by ID.
func (c *Client) GetOperation(ctx context.Context, id string) (*Operation, error) {
	raw, err := c.Get(ctx, "operations/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("directus: get operation %s: %w", id, err)
	}

	var op Operation
	if err := json.Unmarshal(raw, &op); err != nil {
		return nil, fmt.Errorf("directus: unmarshal operation: %w", err)
	}

	return &op, nil
}

// CreateOperation creates a new operation within a flow.
func (c *Client) CreateOperation(ctx context.Context, op Operation) (*Operation, error) {
	raw, err := c.Post(ctx, "operations", op)
	if err != nil {
		return nil, fmt.Errorf("directus: create operation: %w", err)
	}

	var created Operation
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("directus: unmarshal created operation: %w", err)
	}

	return &created, nil
}

// UpdateOperation updates an existing operation.
func (c *Client) UpdateOperation(ctx context.Context, id string, op Operation) (*Operation, error) {
	raw, err := c.Patch(ctx, "operations/"+id, op)
	if err != nil {
		return nil, fmt.Errorf("directus: update operation %s: %w", id, err)
	}

	var updated Operation
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal updated operation: %w", err)
	}

	return &updated, nil
}

// DeleteOperation removes an operation.
func (c *Client) DeleteOperation(ctx context.Context, id string) error {
	if err := c.Delete(ctx, "operations/"+id); err != nil {
		return fmt.Errorf("directus: delete operation %s: %w", id, err)
	}

	return nil
}

// HookFlowOptions configures a hook trigger (fires on database events).
type HookFlowOptions struct {
	// Type is "filter" (blocking, can modify) or "action" (non-blocking, after commit).
	Type string `json:"type"`
	// Scope is the event name, e.g. "items.create", "items.update", "items.delete".
	Scope []string `json:"scope"`
	// Collections restricts the hook to specific collections. Empty = all.
	Collections []string `json:"collections,omitempty"`
}

// ScheduleFlowOptions configures a schedule trigger (cron-based).
type ScheduleFlowOptions struct {
	// Cron is a cron expression, e.g. "0 * * * *" for hourly.
	Cron string `json:"cron"`
}

// WebhookFlowOptions configures a webhook trigger.
type WebhookFlowOptions struct {
	// Method is the allowed HTTP method: "GET" or "POST".
	Method string `json:"method,omitempty"`
	// Async determines if the webhook returns immediately or waits for completion.
	Async bool `json:"async,omitempty"`
}

// NewHookFlow creates a flow triggered by database events.
//
// Example — trigger on item creation in "products":
//
//	directus.NewHookFlow("On Product Create", directus.HookFlowOptions{
//	    Type:        "action",
//	    Scope:       []string{"items.create"},
//	    Collections: []string{"products"},
//	})
func NewHookFlow(name string, opts HookFlowOptions) Flow {
	optsMap := map[string]any{
		"type":  opts.Type,
		"scope": opts.Scope,
	}

	if len(opts.Collections) > 0 {
		optsMap["collections"] = opts.Collections
	}

	return Flow{
		Name:           name,
		Status:         FlowStatusActive,
		Trigger:        TriggerHook,
		Accountability: AccountabilityFull,
		Options:        optsMap,
	}
}

// NewWebhookFlow creates a flow triggered by an HTTP request.
//
// Example:
//
//	flow := directus.NewWebhookFlow("External Trigger", directus.WebhookFlowOptions{
//	    Method: "POST",
//	})
func NewWebhookFlow(name string, opts WebhookFlowOptions) Flow {
	optsMap := map[string]any{}

	if opts.Method != "" {
		optsMap["method"] = opts.Method
	}

	if opts.Async {
		optsMap["async"] = true
	}

	return Flow{
		Name:           name,
		Status:         FlowStatusActive,
		Trigger:        TriggerWebhook,
		Accountability: AccountabilityFull,
		Options:        optsMap,
	}
}

// NewScheduleFlow creates a flow triggered on a cron schedule.
//
// Example — run every hour:
//
//	directus.NewScheduleFlow("Hourly Cleanup", directus.ScheduleFlowOptions{
//	    Cron: "0 * * * *",
//	})
func NewScheduleFlow(name string, opts ScheduleFlowOptions) Flow {
	return Flow{
		Name:           name,
		Status:         FlowStatusActive,
		Trigger:        TriggerSchedule,
		Accountability: AccountabilityFull,
		Options:        map[string]any{"cron": opts.Cron},
	}
}

// NewManualFlow creates a flow triggered manually from the Directus UI.
func NewManualFlow(name string) Flow {
	return Flow{
		Name:           name,
		Status:         FlowStatusActive,
		Trigger:        TriggerManual,
		Accountability: AccountabilityTrigger,
	}
}

// NewLogOperation creates a log operation.
func NewLogOperation(key, message string) Operation {
	return Operation{
		Name: "Log: " + key,
		Key:  key,
		Type: OpLog,
		Options: map[string]any{
			"message": message,
		},
	}
}

// NewRequestOperation creates an HTTP request operation.
func NewRequestOperation(key, method, url string) Operation {
	return Operation{
		Name: "Request: " + key,
		Key:  key,
		Type: OpRequest,
		Options: map[string]any{
			"method": method,
			"url":    url,
		},
	}
}

// NewCreateItemOperation creates an operation that inserts an item into a collection.
func NewCreateItemOperation(key, collection string, payload map[string]any) Operation {
	return Operation{
		Name: "Create: " + key,
		Key:  key,
		Type: OpCreate,
		Options: map[string]any{
			"collection": collection,
			"payload":    payload,
		},
	}
}

// NewConditionOperation creates a conditional branch operation.
func NewConditionOperation(key string, filter map[string]any) Operation {
	return Operation{
		Name: "Condition: " + key,
		Key:  key,
		Type: OpCondition,
		Options: map[string]any{
			"filter": filter,
		},
	}
}
