package directus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Items provides typed CRUD operations for a Directus collection.
type Items[T any] struct {
	client     *Client
	collection string
}

// NewItems creates a new Items wrapper for the given collection.
func NewItems[T any](client *Client, collection string) *Items[T] {
	return &Items[T]{
		client:     client,
		collection: collection,
	}
}

// Collection returns the collection name.
func (i *Items[T]) Collection() string {
	return i.collection
}

// Client returns the underlying Directus client for ad-hoc REST calls.
func (i *Items[T]) Client() *Client {
	return i.client
}

// List fetches items from the collection, shaped by the given QueryOptions.
func (i *Items[T]) List(ctx context.Context, opts ...QueryOption) ([]T, error) {
	query, err := buildQuery(opts)
	if err != nil {
		return nil, err
	}

	raw, err := i.client.Get(ctx, i.path(), query)
	if err != nil {
		return nil, fmt.Errorf("directus: list %s: %w", i.collection, err)
	}

	var items []T
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("directus: unmarshal %s list: %w", i.collection, err)
	}

	return items, nil
}

// Get fetches a single item by ID.
func (i *Items[T]) Get(ctx context.Context, id string, opts ...QueryOption) (*T, error) {
	query, err := buildQuery(opts)
	if err != nil {
		return nil, err
	}

	raw, err := i.client.Get(ctx, i.path()+"/"+id, query)
	if err != nil {
		return nil, fmt.Errorf("directus: get %s/%s: %w", i.collection, id, err)
	}

	var item T
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("directus: unmarshal %s/%s: %w", i.collection, id, err)
	}

	return &item, nil
}

// Create creates a new item in the collection.
func (i *Items[T]) Create(ctx context.Context, item *T) (*T, error) {
	raw, err := i.client.Post(ctx, i.path(), item)
	if err != nil {
		return nil, fmt.Errorf("directus: create %s: %w", i.collection, err)
	}

	var created T
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("directus: unmarshal created %s: %w", i.collection, err)
	}

	return &created, nil
}

// Update updates an existing item by ID.
func (i *Items[T]) Update(ctx context.Context, id string, item *T) (*T, error) {
	raw, err := i.client.Patch(ctx, i.path()+"/"+id, item)
	if err != nil {
		return nil, fmt.Errorf("directus: update %s/%s: %w", i.collection, id, err)
	}

	var updated T
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal updated %s/%s: %w", i.collection, id, err)
	}

	return &updated, nil
}

// Delete removes an item by ID.
func (i *Items[T]) Delete(ctx context.Context, id string) error {
	if err := i.client.Delete(ctx, i.path()+"/"+id); err != nil {
		return fmt.Errorf("directus: delete %s/%s: %w", i.collection, id, err)
	}

	return nil
}

// MaxDateUpdated returns max(date_updated), else max(date_created), else the zero
// time, fetching one field of one row. Deleting the newest item makes it decrease.
func (i *Items[T]) MaxDateUpdated(ctx context.Context) (time.Time, error) {
	t, err := i.fetchMaxTimestamp(ctx, "date_updated")
	if err == nil && !t.IsZero() {
		return t, nil
	}

	// date_updated may be absent from the schema (400/403) or unset on every item.
	t, err = i.fetchMaxTimestamp(ctx, "date_created")
	if err != nil {
		// Neither field exists — a collection without timestamps is not an error.
		return time.Time{}, nil
	}

	return t, nil
}

func (i *Items[T]) fetchMaxTimestamp(ctx context.Context, field string) (time.Time, error) {
	query, err := buildQuery([]QueryOption{
		WithFilter(Field(field, "_nnull", true)),
		WithSort("-" + field),
		WithLimit(1),
		WithFields(field),
	})
	if err != nil {
		return time.Time{}, err
	}

	raw, err := i.client.Get(ctx, i.path(), query)
	if err != nil {
		return time.Time{}, fmt.Errorf("directus: fetch max %s %s: %w", field, i.collection, err)
	}

	var records []map[string]*time.Time
	if err := json.Unmarshal(raw, &records); err != nil {
		return time.Time{}, fmt.Errorf("directus: unmarshal %s %s: %w", field, i.collection, err)
	}

	if len(records) == 0 {
		return time.Time{}, nil
	}

	if t := records[0][field]; t != nil && !t.IsZero() {
		return *t, nil
	}

	return time.Time{}, nil
}

func (i *Items[T]) path() string {
	return "items/" + i.collection
}
