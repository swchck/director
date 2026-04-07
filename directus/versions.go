package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// ContentVersion represents a Directus content version (draft/staging).
type ContentVersion struct {
	ID         string          `json:"id,omitempty"`
	Key        string          `json:"key,omitempty"`
	Name       string          `json:"name,omitempty"`
	Collection string          `json:"collection,omitempty"`
	Item       string          `json:"item,omitempty"`
	Hash       string          `json:"hash,omitempty"`
	Delta      json.RawMessage `json:"delta,omitempty"`
}

func (c *Client) ListContentVersions(ctx context.Context, opts ...QueryOption) ([]ContentVersion, error) {
	return list[ContentVersion](c, ctx, "versions", opts)
}

func (c *Client) GetContentVersion(ctx context.Context, id string) (*ContentVersion, error) {
	return get[ContentVersion](c, ctx, "versions/"+id)
}

func (c *Client) CreateContentVersion(ctx context.Context, v ContentVersion) (*ContentVersion, error) {
	return create[ContentVersion](c, ctx, "versions", v)
}

func (c *Client) UpdateContentVersion(ctx context.Context, id string, v ContentVersion) (*ContentVersion, error) {
	return update[ContentVersion](c, ctx, "versions/"+id, v)
}

func (c *Client) DeleteContentVersion(ctx context.Context, id string) error {
	return c.Delete(ctx, "versions/"+id)
}

// CompareContentVersion compares a version with the main item.
func (c *Client) CompareContentVersion(ctx context.Context, id string) (json.RawMessage, error) {
	raw, err := c.Get(ctx, "versions/"+id+"/compare", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: compare version %s: %w", id, err)
	}

	return raw, nil
}

// PromoteContentVersion promotes a version to become the main version.
func (c *Client) PromoteContentVersion(ctx context.Context, id string) error {
	_, err := c.Post(ctx, "versions/"+id+"/promote", nil)
	if err != nil {
		return fmt.Errorf("directus: promote version %s: %w", id, err)
	}

	return nil
}

func (c *Client) SaveContentVersion(ctx context.Context, id string, data any) error {
	_, err := c.Post(ctx, "versions/"+id+"/save", data)
	if err != nil {
		return fmt.Errorf("directus: save version %s: %w", id, err)
	}

	return nil
}
