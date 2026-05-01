// Package file provides filesystem-backed implementations of source.CollectionSource
// and source.SingletonSource. They read JSON files on disk and are intended for
// CI, local development, and end-to-end tests, where running a real CMS adds
// friction without buying additional fidelity.
//
// Switch backends via env: DIRECTOR_SOURCE=filesystem → file; default → Directus.
//
// Example:
//
//	src := file.NewCollection[Product]("testdata/products.json")
//	manager.RegisterCollectionSource(mgr, products, src)
package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/swchck/director/source"
)

// Collection reads a JSON array of T from a file.
type Collection[T any] struct {
	path string
}

// NewCollection returns a CollectionSource that reads []T from a JSON file at path.
func NewCollection[T any](path string) source.CollectionSource[T] {
	return &Collection[T]{path: path}
}

// List reads the file and decodes its contents into []T.
func (c *Collection[T]) List(_ context.Context) ([]T, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("file: read %s: %w", c.path, err)
	}
	var out []T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("file: decode %s: %w", c.path, err)
	}
	return out, nil
}

// LastModified returns the file's modification time. If the file does not
// exist, it returns the zero time so the manager will retry on the next cycle
// instead of treating absence as an error.
func (c *Collection[T]) LastModified(_ context.Context) (time.Time, error) {
	return statModTime(c.path)
}

// Singleton reads a JSON object as a single *T from a file.
type Singleton[T any] struct {
	path string
}

// NewSingleton returns a SingletonSource that reads *T from a JSON file at path.
func NewSingleton[T any](path string) source.SingletonSource[T] {
	return &Singleton[T]{path: path}
}

// Get reads the file and decodes its contents into *T.
func (s *Singleton[T]) Get(_ context.Context) (*T, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("file: read %s: %w", s.path, err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("file: decode %s: %w", s.path, err)
	}
	return &out, nil
}

// LastModified returns the file's modification time. If the file does not
// exist, it returns the zero time so the manager will retry on the next cycle.
func (s *Singleton[T]) LastModified(_ context.Context) (time.Time, error) {
	return statModTime(s.path)
}

// KeyCollection reads a JSON object {"<key>": [...], ...} from a file and
// decodes the array under key into []T. Useful when many collections share a
// single config bundle and are addressed by named sections.
type KeyCollection[T any] struct {
	path string
	key  string
}

// NewKeyCollection returns a CollectionSource that reads []T from the field
// named key in a JSON object at path.
//
// If the key is absent from the file, List returns a nil slice without error
// — the file is considered well-formed but the section is intentionally empty.
func NewKeyCollection[T any](path, key string) source.CollectionSource[T] {
	return &KeyCollection[T]{path: path, key: key}
}

// List reads the file, extracts the value at the configured key, and decodes
// it into []T.
func (k *KeyCollection[T]) List(_ context.Context) ([]T, error) {
	data, err := os.ReadFile(k.path)
	if err != nil {
		return nil, fmt.Errorf("file: read %s: %w", k.path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("file: decode %s: %w", k.path, err)
	}
	section, ok := raw[k.key]
	if !ok {
		return nil, nil
	}
	var out []T
	if err := json.Unmarshal(section, &out); err != nil {
		return nil, fmt.Errorf("file: decode %s[%q]: %w", k.path, k.key, err)
	}
	return out, nil
}

// LastModified returns the file's modification time. If the file does not
// exist, it returns the zero time so the manager will retry on the next cycle.
func (k *KeyCollection[T]) LastModified(_ context.Context) (time.Time, error) {
	return statModTime(k.path)
}

func statModTime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("file: stat %s: %w", path, err)
	}
	return info.ModTime(), nil
}
