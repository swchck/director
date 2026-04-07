package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// Revision represents a Directus content revision.
type Revision struct {
	ID         int             `json:"id,omitempty"`
	Activity   int             `json:"activity,omitempty"`
	Collection string          `json:"collection,omitempty"`
	Item       string          `json:"item,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Delta      json.RawMessage `json:"delta,omitempty"`
	Parent     *int            `json:"parent,omitempty"`
	Version    *string         `json:"version,omitempty"`
}

func (c *Client) ListRevisions(ctx context.Context, opts ...QueryOption) ([]Revision, error) {
	return list[Revision](c, ctx, "revisions", opts)
}

func (c *Client) GetRevision(ctx context.Context, id int) (*Revision, error) {
	return get[Revision](c, ctx, fmt.Sprintf("revisions/%d", id))
}
