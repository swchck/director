package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// Share represents a Directus shared item link.
type Share struct {
	ID          string  `json:"id,omitempty"`
	Name        string  `json:"name,omitempty"`
	Collection  string  `json:"collection,omitempty"`
	Item        string  `json:"item,omitempty"`
	Role        string  `json:"role,omitempty"`
	Password    *string `json:"password,omitempty"`
	UserCreated string  `json:"user_created,omitempty"`
	DateCreated string  `json:"date_created,omitempty"`
	DateStart   *string `json:"date_start,omitempty"`
	DateEnd     *string `json:"date_end,omitempty"`
	TimesUsed   int     `json:"times_used,omitempty"`
	MaxUses     *int    `json:"max_uses,omitempty"`
}

func (c *Client) ListShares(ctx context.Context, opts ...QueryOption) ([]Share, error) {
	return list[Share](c, ctx, "shares", opts)
}

func (c *Client) GetShare(ctx context.Context, id string) (*Share, error) {
	return get[Share](c, ctx, "shares/"+id)
}

func (c *Client) CreateShare(ctx context.Context, s Share) (*Share, error) {
	return create[Share](c, ctx, "shares", s)
}

func (c *Client) UpdateShare(ctx context.Context, id string, s Share) (*Share, error) {
	return update[Share](c, ctx, "shares/"+id, s)
}

func (c *Client) DeleteShare(ctx context.Context, id string) error {
	return c.Delete(ctx, "shares/"+id)
}

// ShareInfo returns public information about a share (unauthenticated).
func (c *Client) ShareInfo(ctx context.Context, id string) (json.RawMessage, error) {
	raw, err := c.Get(ctx, "shares/info/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("directus: share info %s: %w", id, err)
	}

	return raw, nil
}
