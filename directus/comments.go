package directus

import "context"

// Comment represents a Directus item comment.
type Comment struct {
	ID          string `json:"id,omitempty"`
	Collection  string `json:"collection,omitempty"`
	Item        string `json:"item,omitempty"`
	Comment     string `json:"comment,omitempty"`
	DateCreated string `json:"date_created,omitempty"`
	DateUpdated string `json:"date_updated,omitempty"`
	UserCreated string `json:"user_created,omitempty"`
	UserUpdated string `json:"user_updated,omitempty"`
}

func (c *Client) ListComments(ctx context.Context, opts ...QueryOption) ([]Comment, error) {
	return list[Comment](c, ctx, "comments", opts)
}

func (c *Client) GetComment(ctx context.Context, id string) (*Comment, error) {
	return get[Comment](c, ctx, "comments/"+id)
}

func (c *Client) CreateComment(ctx context.Context, comment Comment) (*Comment, error) {
	return create[Comment](c, ctx, "comments", comment)
}

func (c *Client) UpdateComment(ctx context.Context, id string, comment Comment) (*Comment, error) {
	return update[Comment](c, ctx, "comments/"+id, comment)
}

func (c *Client) DeleteComment(ctx context.Context, id string) error {
	return c.Delete(ctx, "comments/"+id)
}
