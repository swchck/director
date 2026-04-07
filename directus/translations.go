package directus

import "context"

// Translation represents a Directus custom UI translation string.
type Translation struct {
	ID       string `json:"id,omitempty"`
	Key      string `json:"key,omitempty"`
	Language string `json:"language,omitempty"`
	Value    string `json:"value,omitempty"`
}

func (c *Client) ListTranslations(ctx context.Context, opts ...QueryOption) ([]Translation, error) {
	return list[Translation](c, ctx, "translations", opts)
}

func (c *Client) GetTranslation(ctx context.Context, id string) (*Translation, error) {
	return get[Translation](c, ctx, "translations/"+id)
}

func (c *Client) CreateTranslation(ctx context.Context, t Translation) (*Translation, error) {
	return create[Translation](c, ctx, "translations", t)
}

func (c *Client) UpdateTranslation(ctx context.Context, id string, t Translation) (*Translation, error) {
	return update[Translation](c, ctx, "translations/"+id, t)
}

func (c *Client) DeleteTranslation(ctx context.Context, id string) error {
	return c.Delete(ctx, "translations/"+id)
}
