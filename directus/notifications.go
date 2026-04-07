package directus

import (
	"context"
	"fmt"
)

// Notification represents a Directus in-app notification.
type Notification struct {
	ID         int    `json:"id,omitempty"`
	Recipient  string `json:"recipient,omitempty"`
	Sender     string `json:"sender,omitempty"`
	Subject    string `json:"subject,omitempty"`
	Message    string `json:"message,omitempty"`
	Collection string `json:"collection,omitempty"`
	Item       string `json:"item,omitempty"`
	Status     string `json:"status,omitempty"` // "inbox" or "archived"
	Timestamp  string `json:"timestamp,omitempty"`
}

func (c *Client) ListNotifications(ctx context.Context, opts ...QueryOption) ([]Notification, error) {
	return list[Notification](c, ctx, "notifications", opts)
}

func (c *Client) GetNotification(ctx context.Context, id int) (*Notification, error) {
	return get[Notification](c, ctx, fmt.Sprintf("notifications/%d", id))
}

func (c *Client) CreateNotification(ctx context.Context, n Notification) (*Notification, error) {
	return create[Notification](c, ctx, "notifications", n)
}

func (c *Client) UpdateNotification(ctx context.Context, id int, n Notification) (*Notification, error) {
	return update[Notification](c, ctx, fmt.Sprintf("notifications/%d", id), n)
}

func (c *Client) DeleteNotification(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("notifications/%d", id))
}
