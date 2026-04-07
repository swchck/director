package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// Extension represents a Directus extension.
type Extension struct {
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Enabled bool            `json:"enabled,omitempty"`
	Bundle  *string         `json:"bundle,omitempty"`
	Schema  json.RawMessage `json:"schema,omitempty"`
	Meta    json.RawMessage `json:"meta,omitempty"`
}

func (c *Client) ListExtensions(ctx context.Context) ([]Extension, error) {
	return list[Extension](c, ctx, "extensions", nil)
}

// UpdateExtension enables/disables an extension.
func (c *Client) UpdateExtension(ctx context.Context, name string, ext Extension) (*Extension, error) {
	return update[Extension](c, ctx, "extensions/"+name, ext)
}

// Metrics returns Directus Prometheus-format metrics (if enabled).
func (c *Client) Metrics(ctx context.Context) (string, error) {
	raw, err := c.Get(ctx, "server/metrics", nil)
	if err != nil {
		return "", fmt.Errorf("directus: metrics: %w", err)
	}

	return string(raw), nil
}
