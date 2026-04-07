package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// Dashboard represents a Directus Insights dashboard.
type Dashboard struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Icon        string          `json:"icon,omitempty"`
	Note        string          `json:"note,omitempty"`
	Color       string          `json:"color,omitempty"`
	DateCreated string          `json:"date_created,omitempty"`
	UserCreated string          `json:"user_created,omitempty"`
	Panels      json.RawMessage `json:"panels,omitempty"`
}

func (c *Client) ListDashboards(ctx context.Context, opts ...QueryOption) ([]Dashboard, error) {
	return list[Dashboard](c, ctx, "dashboards", opts)
}

func (c *Client) GetDashboard(ctx context.Context, id string) (*Dashboard, error) {
	return get[Dashboard](c, ctx, "dashboards/"+id)
}

func (c *Client) CreateDashboard(ctx context.Context, d Dashboard) (*Dashboard, error) {
	return create[Dashboard](c, ctx, "dashboards", d)
}

func (c *Client) UpdateDashboard(ctx context.Context, id string, d Dashboard) (*Dashboard, error) {
	return update[Dashboard](c, ctx, "dashboards/"+id, d)
}

func (c *Client) DeleteDashboard(ctx context.Context, id string) error {
	return c.Delete(ctx, "dashboards/"+id)
}

// Panel represents a panel within a Directus dashboard.
type Panel struct {
	ID          string         `json:"id,omitempty"`
	Dashboard   string         `json:"dashboard,omitempty"`
	Name        string         `json:"name,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	Color       string         `json:"color,omitempty"`
	ShowHeader  bool           `json:"show_header,omitempty"`
	Note        string         `json:"note,omitempty"`
	Type        string         `json:"type,omitempty"`
	PositionX   int            `json:"position_x,omitempty"`
	PositionY   int            `json:"position_y,omitempty"`
	Width       int            `json:"width,omitempty"`
	Height      int            `json:"height,omitempty"`
	Options     map[string]any `json:"options,omitempty"`
	DateCreated string         `json:"date_created,omitempty"`
	UserCreated string         `json:"user_created,omitempty"`
}

func (c *Client) ListPanels(ctx context.Context, opts ...QueryOption) ([]Panel, error) {
	return list[Panel](c, ctx, "panels", opts)
}

func (c *Client) GetPanel(ctx context.Context, id string) (*Panel, error) {
	return get[Panel](c, ctx, "panels/"+id)
}

func (c *Client) CreatePanel(ctx context.Context, p Panel) (*Panel, error) {
	return create[Panel](c, ctx, "panels", p)
}

func (c *Client) UpdatePanel(ctx context.Context, id string, p Panel) (*Panel, error) {
	return update[Panel](c, ctx, "panels/"+id, p)
}

func (c *Client) DeletePanel(ctx context.Context, id string) error {
	return c.Delete(ctx, "panels/"+id)
}

func list[T any](c *Client, ctx context.Context, path string, opts []QueryOption) ([]T, error) {
	query, err := buildQuery(opts)
	if err != nil {
		return nil, err
	}

	raw, err := c.Get(ctx, path, query)
	if err != nil {
		return nil, fmt.Errorf("directus: list %s: %w", path, err)
	}

	var items []T
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("directus: unmarshal %s: %w", path, err)
	}

	return items, nil
}

func get[T any](c *Client, ctx context.Context, path string) (*T, error) {
	raw, err := c.Get(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("directus: get %s: %w", path, err)
	}

	var item T
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("directus: unmarshal %s: %w", path, err)
	}

	return &item, nil
}

func create[T any](c *Client, ctx context.Context, path string, item T) (*T, error) {
	raw, err := c.Post(ctx, path, item)
	if err != nil {
		return nil, fmt.Errorf("directus: create %s: %w", path, err)
	}

	var created T
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("directus: unmarshal %s: %w", path, err)
	}

	return &created, nil
}

func update[T any](c *Client, ctx context.Context, path string, item T) (*T, error) {
	raw, err := c.Patch(ctx, path, item)
	if err != nil {
		return nil, fmt.Errorf("directus: update %s: %w", path, err)
	}

	var updated T
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal %s: %w", path, err)
	}

	return &updated, nil
}
