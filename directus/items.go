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

// List fetches items from the collection with optional filtering, sorting, and pagination.
// Supports relational fields via WithFields (dot notation) and WithDeep for nested queries.
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
// Use WithFields with dot notation to include relational data (e.g. "author.*", "tags.*").
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
// Relational fields can be set by passing nested objects or IDs in the struct.
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
// Relational fields can be updated by passing nested objects or IDs in the struct.
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

// MaxDateUpdated fetches the most recent modification timestamp from the collection.
// It tries date_updated first (sorted desc), falling back to date_created (sorted desc)
// if no items have been updated yet.
// This is used for lightweight version detection without fetching all items.
func (i *Items[T]) MaxDateUpdated(ctx context.Context) (time.Time, error) {
	// First try: max(date_updated).
	t, err := i.fetchMaxTimestamp(ctx, "date_updated")
	if err == nil && !t.IsZero() {
		return t, nil
	}
	// Field might not exist (403/400) or have no values — fall through.

	// Fallback: max(date_created) — for items that were created but never updated,
	// or when date_updated field doesn't exist in the schema.
	t, err = i.fetchMaxTimestamp(ctx, "date_created")
	if err != nil {
		// date_created field might not exist either — not an error, just return zero.
		return time.Time{}, nil
	}

	return t, nil
}

func (i *Items[T]) fetchMaxTimestamp(ctx context.Context, field string) (time.Time, error) {
	query, err := buildQuery([]QueryOption{
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
