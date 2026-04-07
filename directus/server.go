package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// ServerHealth represents the Directus health check response.
type ServerHealth struct {
	Status string `json:"status"`
}

func (c *Client) ServerHealth(ctx context.Context) (*ServerHealth, error) {
	raw, err := c.Get(ctx, "server/health", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: server health: %w", err)
	}

	var health ServerHealth
	if err := json.Unmarshal(raw, &health); err != nil {
		// Health endpoint may return {"status":"ok"} without data wrapper.
		return &ServerHealth{Status: "ok"}, nil
	}

	return &health, nil
}

func (c *Client) ServerInfo(ctx context.Context) (json.RawMessage, error) {
	raw, err := c.Get(ctx, "server/info", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: server info: %w", err)
	}

	return raw, nil
}

// ServerPing checks connectivity (returns "pong").
func (c *Client) ServerPing(ctx context.Context) error {
	_, err := c.Get(ctx, "server/ping", nil)
	return err
}

// ServerSpecsOAS returns the OpenAPI specification.
func (c *Client) ServerSpecsOAS(ctx context.Context) (json.RawMessage, error) {
	raw, err := c.Get(ctx, "server/specs/oas", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: server specs oas: %w", err)
	}

	return raw, nil
}

// ServerSpecsGraphQL returns the GraphQL SDL.
func (c *Client) ServerSpecsGraphQL(ctx context.Context) (json.RawMessage, error) {
	raw, err := c.Get(ctx, "server/specs/graphql", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: server specs graphql: %w", err)
	}

	return raw, nil
}

// Settings represents Directus project settings.
type Settings struct {
	ID                    int             `json:"id,omitempty"`
	ProjectName           string          `json:"project_name,omitempty"`
	ProjectURL            string          `json:"project_url,omitempty"`
	ProjectColor          string          `json:"project_color,omitempty"`
	ProjectLogo           *string         `json:"project_logo,omitempty"`
	DefaultLanguage       string          `json:"default_language,omitempty"`
	DefaultAppearance     string          `json:"default_appearance,omitempty"`
	PublicForeground      *string         `json:"public_foreground,omitempty"`
	PublicBackground      *string         `json:"public_background,omitempty"`
	PublicNote            *string         `json:"public_note,omitempty"`
	AuthLoginAttempts     int             `json:"auth_login_attempts,omitempty"`
	StorageAssetTransform string          `json:"storage_asset_transform,omitempty"`
	StorageAssetPresets   json.RawMessage `json:"storage_asset_presets,omitempty"`
	CustomCSS             *string         `json:"custom_css,omitempty"`
	ModuleBar             json.RawMessage `json:"module_bar,omitempty"`
}

func (c *Client) GetSettings(ctx context.Context) (*Settings, error) {
	raw, err := c.Get(ctx, "settings", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: get settings: %w", err)
	}

	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("directus: unmarshal settings: %w", err)
	}

	return &s, nil
}

func (c *Client) UpdateSettings(ctx context.Context, s Settings) (*Settings, error) {
	raw, err := c.Patch(ctx, "settings", s)
	if err != nil {
		return nil, fmt.Errorf("directus: update settings: %w", err)
	}

	var updated Settings
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal settings: %w", err)
	}

	return &updated, nil
}

func (c *Client) HashGenerate(ctx context.Context, value string) (string, error) {
	raw, err := c.Post(ctx, "utils/hash/generate", map[string]string{"string": value})
	if err != nil {
		return "", fmt.Errorf("directus: hash generate: %w", err)
	}

	var result string
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("directus: unmarshal hash: %w", err)
	}

	return result, nil
}

func (c *Client) HashVerify(ctx context.Context, value, hash string) (bool, error) {
	raw, err := c.Post(ctx, "utils/hash/verify", map[string]string{"string": value, "hash": hash})
	if err != nil {
		return false, fmt.Errorf("directus: hash verify: %w", err)
	}

	var result bool
	if err := json.Unmarshal(raw, &result); err != nil {
		return false, fmt.Errorf("directus: unmarshal hash verify: %w", err)
	}

	return result, nil
}

func (c *Client) RandomString(ctx context.Context, length int) (string, error) {
	raw, err := c.Get(ctx, fmt.Sprintf("utils/random/string?length=%d", length), nil)
	if err != nil {
		return "", fmt.Errorf("directus: random string: %w", err)
	}

	var result string
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("directus: unmarshal random string: %w", err)
	}

	return result, nil
}

func (c *Client) ClearCache(ctx context.Context) error {
	_, err := c.Post(ctx, "utils/cache/clear", nil)
	if err != nil {
		return fmt.Errorf("directus: clear cache: %w", err)
	}

	return nil
}

func (c *Client) SortItems(ctx context.Context, collection string, item, to int) error {
	_, err := c.Post(ctx, "utils/sort/"+collection, map[string]int{"item": item, "to": to})
	if err != nil {
		return fmt.Errorf("directus: sort items %s: %w", collection, err)
	}

	return nil
}

func (c *Client) SchemaSnapshot(ctx context.Context) (json.RawMessage, error) {
	raw, err := c.Get(ctx, "schema/snapshot", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: schema snapshot: %w", err)
	}

	return raw, nil
}

// SchemaDiff computes the diff between a snapshot and the current schema.
func (c *Client) SchemaDiff(ctx context.Context, snapshot json.RawMessage, force bool) (json.RawMessage, error) {
	path := "schema/diff"
	if force {
		path += "?force=true"
	}

	raw, err := c.Post(ctx, path, snapshot)
	if err != nil {
		return nil, fmt.Errorf("directus: schema diff: %w", err)
	}

	return raw, nil
}

func (c *Client) SchemaApply(ctx context.Context, diff json.RawMessage) error {
	_, err := c.Post(ctx, "schema/apply", diff)
	if err != nil {
		return fmt.Errorf("directus: schema apply: %w", err)
	}

	return nil
}
