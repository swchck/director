package directus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Singleton provides typed read/update operations for a Directus singleton collection.
// Singleton collections return a single object (not an array) from the items endpoint.
//
// Supports relational fields: use WithFields with dot notation (e.g. "translations.*")
// and WithDeep for filtering nested relations.
type Singleton[T any] struct {
	client     *Client
	collection string
}

// NewSingleton creates a new Singleton wrapper for the given collection.
func NewSingleton[T any](client *Client, collection string) *Singleton[T] {
	return &Singleton[T]{
		client:     client,
		collection: collection,
	}
}

// Collection returns the collection name.
func (s *Singleton[T]) Collection() string {
	return s.collection
}

// Get fetches the singleton item.
// Use WithFields with dot notation to include relational data (e.g. "translations.*").
func (s *Singleton[T]) Get(ctx context.Context, opts ...QueryOption) (*T, error) {
	query, err := buildQuery(opts)
	if err != nil {
		return nil, err
	}

	raw, err := s.client.Get(ctx, s.path(), query)
	if err != nil {
		return nil, fmt.Errorf("directus: get singleton %s: %w", s.collection, err)
	}

	var item T
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("directus: unmarshal singleton %s: %w", s.collection, err)
	}

	return &item, nil
}

// Update updates the singleton item.
func (s *Singleton[T]) Update(ctx context.Context, item *T) (*T, error) {
	raw, err := s.client.Patch(ctx, s.path(), item)
	if err != nil {
		return nil, fmt.Errorf("directus: update singleton %s: %w", s.collection, err)
	}

	var updated T
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal updated singleton %s: %w", s.collection, err)
	}

	return &updated, nil
}

// DateUpdated fetches the date_updated value from the singleton.
// DateUpdated fetches the modification timestamp from the singleton.
// Tries date_updated first, falls back to date_created.
// Used for version detection.
func (s *Singleton[T]) DateUpdated(ctx context.Context) (time.Time, error) {
	// Try date_updated first.
	t, err := s.fetchTimestamp(ctx, "date_updated")
	if err != nil {
		return time.Time{}, err
	}

	if !t.IsZero() {
		return t, nil
	}

	// Fallback: date_created.
	t, err = s.fetchTimestamp(ctx, "date_created")
	if err != nil {
		// Field might not exist — not fatal.
		return time.Time{}, nil
	}

	return t, nil
}

func (s *Singleton[T]) fetchTimestamp(ctx context.Context, field string) (time.Time, error) {
	query, err := buildQuery([]QueryOption{
		WithFields(field),
	})
	if err != nil {
		return time.Time{}, err
	}

	raw, err := s.client.Get(ctx, s.path(), query)
	if err != nil {
		return time.Time{}, fmt.Errorf("directus: fetch %s singleton %s: %w", field, s.collection, err)
	}

	var record map[string]*time.Time
	if err := json.Unmarshal(raw, &record); err != nil {
		return time.Time{}, fmt.Errorf("directus: unmarshal %s singleton %s: %w", field, s.collection, err)
	}

	if t := record[field]; t != nil && !t.IsZero() {
		return *t, nil
	}

	return time.Time{}, nil
}

func (s *Singleton[T]) path() string {
	return "items/" + s.collection
}
