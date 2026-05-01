package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swchck/director/source"
	"github.com/swchck/director/source/file"
)

type product struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type settings struct {
	SiteName string `json:"site_name"`
	Locale   string `json:"locale"`
}

// Compile-time interface checks.
var (
	_ source.CollectionSource[product] = (*file.Collection[product])(nil)
	_ source.CollectionSource[product] = (*file.KeyCollection[product])(nil)
	_ source.SingletonSource[settings] = (*file.Singleton[settings])(nil)
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestCollection_List(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "products.json")
	writeFile(t, path, `[{"id":1,"name":"Widget"},{"id":2,"name":"Gadget"}]`)

	src := file.NewCollection[product](path)
	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "Widget" || got[1].Name != "Gadget" {
		t.Errorf("got = %+v", got)
	}
}

func TestCollection_List_EmptyArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "products.json")
	writeFile(t, path, `[]`)

	src := file.NewCollection[product](path)
	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestCollection_List_MissingFileReturnsError(t *testing.T) {
	src := file.NewCollection[product](filepath.Join(t.TempDir(), "missing.json"))
	if _, err := src.List(context.Background()); err == nil {
		t.Fatal("List() = nil, want error")
	}
}

func TestCollection_List_BadJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	writeFile(t, path, `{not valid`)

	src := file.NewCollection[product](path)
	if _, err := src.List(context.Background()); err == nil {
		t.Fatal("List() = nil, want JSON error")
	}
}

func TestCollection_LastModified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "products.json")
	writeFile(t, path, `[]`)

	src := file.NewCollection[product](path)
	got, err := src.LastModified(context.Background())
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}
	if got.IsZero() {
		t.Fatal("LastModified() returned zero time for existing file")
	}
}

func TestCollection_LastModified_MissingFileReturnsZero(t *testing.T) {
	src := file.NewCollection[product](filepath.Join(t.TempDir(), "missing.json"))
	got, err := src.LastModified(context.Background())
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("LastModified() = %v, want zero", got)
	}
}

func TestCollection_LastModified_TracksWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "products.json")
	writeFile(t, path, `[]`)

	src := file.NewCollection[product](path)
	first, err := src.LastModified(context.Background())
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	writeFile(t, path, `[{"id":1,"name":"X"}]`)

	second, err := src.LastModified(context.Background())
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}
	if !second.After(first) {
		t.Errorf("expected modtime to advance: first=%v second=%v", first, second)
	}
}

func TestSingleton_Get(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeFile(t, path, `{"site_name":"My Site","locale":"en"}`)

	src := file.NewSingleton[settings](path)
	got, err := src.Get(context.Background())
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if got.SiteName != "My Site" || got.Locale != "en" {
		t.Errorf("got = %+v", got)
	}
}

func TestSingleton_Get_MissingFileReturnsError(t *testing.T) {
	src := file.NewSingleton[settings](filepath.Join(t.TempDir(), "missing.json"))
	if _, err := src.Get(context.Background()); err == nil {
		t.Fatal("Get() = nil, want error")
	}
}

func TestSingleton_Get_BadJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeFile(t, path, `not json`)

	src := file.NewSingleton[settings](path)
	if _, err := src.Get(context.Background()); err == nil {
		t.Fatal("Get() = nil, want error")
	}
}

func TestSingleton_LastModified_MissingFileReturnsZero(t *testing.T) {
	src := file.NewSingleton[settings](filepath.Join(t.TempDir(), "missing.json"))
	got, err := src.LastModified(context.Background())
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("LastModified() = %v, want zero", got)
	}
}

func TestKeyCollection_List(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.json")
	writeFile(t, path, `{"products":[{"id":1,"name":"A"}],"categories":[{"id":7,"name":"books"}]}`)

	src := file.NewKeyCollection[product](path, "products")
	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "A" {
		t.Errorf("got = %+v", got)
	}
}

func TestKeyCollection_List_MissingKeyReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.json")
	writeFile(t, path, `{"other":[]}`)

	src := file.NewKeyCollection[product](path, "products")
	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestKeyCollection_List_BadKeyValueReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.json")
	writeFile(t, path, `{"products":"not-an-array"}`)

	src := file.NewKeyCollection[product](path, "products")
	if _, err := src.List(context.Background()); err == nil {
		t.Fatal("List() = nil, want error for non-array value")
	}
}

func TestKeyCollection_List_MissingFileReturnsError(t *testing.T) {
	src := file.NewKeyCollection[product](filepath.Join(t.TempDir(), "missing.json"), "products")
	if _, err := src.List(context.Background()); err == nil {
		t.Fatal("List() = nil, want error")
	}
}

func TestKeyCollection_LastModified_MissingFileReturnsZero(t *testing.T) {
	src := file.NewKeyCollection[product](filepath.Join(t.TempDir(), "missing.json"), "products")
	got, err := src.LastModified(context.Background())
	if err != nil {
		t.Fatalf("LastModified() error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("LastModified() = %v, want zero", got)
	}
}
