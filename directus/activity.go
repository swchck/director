package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// Activity represents a Directus activity log entry.
type Activity struct {
	ID         int             `json:"id,omitempty"`
	Action     string          `json:"action,omitempty"`
	User       string          `json:"user,omitempty"`
	Timestamp  string          `json:"timestamp,omitempty"`
	IP         string          `json:"ip,omitempty"`
	UserAgent  string          `json:"user_agent,omitempty"`
	Collection string          `json:"collection,omitempty"`
	Item       string          `json:"item,omitempty"`
	Comment    string          `json:"comment,omitempty"`
	Origin     string          `json:"origin,omitempty"`
	Revisions  json.RawMessage `json:"revisions,omitempty"`
}

func (c *Client) ListActivity(ctx context.Context, opts ...QueryOption) ([]Activity, error) {
	return list[Activity](c, ctx, "activity", opts)
}

func (c *Client) GetActivity(ctx context.Context, id int) (*Activity, error) {
	return get[Activity](c, ctx, fmt.Sprintf("activity/%d", id))
}
