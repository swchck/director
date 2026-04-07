package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// Preset represents a Directus layout/filter preset (bookmark).
type Preset struct {
	ID            int             `json:"id,omitempty"`
	Bookmark      string          `json:"bookmark,omitempty"`
	User          string          `json:"user,omitempty"`
	Role          string          `json:"role,omitempty"`
	Collection    string          `json:"collection,omitempty"`
	Search        string          `json:"search,omitempty"`
	Filters       json.RawMessage `json:"filters,omitempty"`
	Layout        string          `json:"layout,omitempty"`
	LayoutQuery   json.RawMessage `json:"layout_query,omitempty"`
	LayoutOptions json.RawMessage `json:"layout_options,omitempty"`
}

func (c *Client) ListPresets(ctx context.Context, opts ...QueryOption) ([]Preset, error) {
	return list[Preset](c, ctx, "presets", opts)
}

func (c *Client) GetPreset(ctx context.Context, id int) (*Preset, error) {
	return get[Preset](c, ctx, fmt.Sprintf("presets/%d", id))
}

func (c *Client) CreatePreset(ctx context.Context, p Preset) (*Preset, error) {
	return create[Preset](c, ctx, "presets", p)
}

func (c *Client) UpdatePreset(ctx context.Context, id int, p Preset) (*Preset, error) {
	return update[Preset](c, ctx, fmt.Sprintf("presets/%d", id), p)
}

func (c *Client) DeletePreset(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("presets/%d", id))
}
