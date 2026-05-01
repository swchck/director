package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/swchck/director/directus"
)

type schemaProduct struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

type schemaProductWithDrift struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Price    int    `json:"price"`
	OldField string `json:"old_field"` // not in Directus
}

type schemaProductWithExoticTags struct {
	ID       int    `json:"id"`
	Skipped  string `json:"-"`
	NoTag    string
	internal int      //nolint:unused // exercising unexported branch
	Items    []string `json:"items,omitempty"`
}

func TestListFields_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fields/products" {
			t.Errorf("path = %q, want /fields/products", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"field": "id", "type": "integer"},
				{"field": "name", "type": "string"},
				{"field": "price", "type": "integer"},
			},
		})
	}))
	defer srv.Close()

	c := directus.NewClient(srv.URL, "tok")
	got, err := c.ListFields(context.Background(), "products")
	if err != nil {
		t.Fatalf("ListFields() error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Field != "id" || got[0].Type != directus.FieldTypeInteger {
		t.Errorf("got[0] = %+v", got[0])
	}
}

func TestListFields_DirectusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := directus.NewClient(srv.URL, "tok")
	if _, err := c.ListFields(context.Background(), "products"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCompareStruct_NoDrift(t *testing.T) {
	fields := []directus.CollectionField{
		{Field: "id", Type: directus.FieldTypeInteger},
		{Field: "name", Type: directus.FieldTypeString},
		{Field: "price", Type: directus.FieldTypeInteger},
	}
	got := directus.CompareStruct(fields, schemaProduct{})
	if len(got) != 0 {
		t.Errorf("expected no drift, got %+v", got)
	}
}

func TestCompareStruct_FlagsMissingField(t *testing.T) {
	fields := []directus.CollectionField{
		{Field: "id", Type: directus.FieldTypeInteger},
		{Field: "name", Type: directus.FieldTypeString},
		{Field: "price", Type: directus.FieldTypeInteger},
	}
	got := directus.CompareStruct(fields, schemaProductWithDrift{})
	if len(got) != 1 {
		t.Fatalf("expected 1 drift, got %+v", got)
	}
	if got[0].JSONTag != "old_field" {
		t.Errorf("JSONTag = %q, want old_field", got[0].JSONTag)
	}
	if got[0].Field != "OldField" {
		t.Errorf("Field = %q, want OldField", got[0].Field)
	}
	if got[0].Reason != "missing_in_directus" {
		t.Errorf("Reason = %q", got[0].Reason)
	}
}

func TestCompareStruct_IgnoresHyphenAndUntaggedAndUnexported(t *testing.T) {
	// Directus has "id" and "items" only — no "Skipped", "NoTag", "internal".
	// Exotic-tag struct should match cleanly because skipped fields are out
	// of scope and untagged fields are conservatively ignored.
	fields := []directus.CollectionField{
		{Field: "id", Type: directus.FieldTypeInteger},
		{Field: "items", Type: directus.FieldTypeJSON},
	}
	got := directus.CompareStruct(fields, schemaProductWithExoticTags{})
	if len(got) != 0 {
		t.Errorf("expected no drift; ignored tags should not be flagged. got %+v", got)
	}
}

func TestCompareStruct_AcceptsPointer(t *testing.T) {
	fields := []directus.CollectionField{
		{Field: "id", Type: directus.FieldTypeInteger},
		{Field: "name", Type: directus.FieldTypeString},
		{Field: "price", Type: directus.FieldTypeInteger},
	}
	got := directus.CompareStruct(fields, &schemaProduct{})
	if len(got) != 0 {
		t.Errorf("pointer should be dereferenced; got drift %+v", got)
	}
}

func TestCompareStruct_NonStructReturnsNil(t *testing.T) {
	fields := []directus.CollectionField{{Field: "id"}}
	if got := directus.CompareStruct(fields, "not a struct"); got != nil {
		t.Errorf("string sample → got %+v, want nil", got)
	}
	if got := directus.CompareStruct(fields, 42); got != nil {
		t.Errorf("int sample → got %+v, want nil", got)
	}
	if got := directus.CompareStruct(fields, nil); got != nil {
		t.Errorf("nil sample → got %+v, want nil", got)
	}
}

func TestCompareStruct_IgnoresExtraDirectusFields(t *testing.T) {
	// "audit" and "tenant_id" exist in Directus but not in Go — should be
	// silently accepted, not flagged.
	fields := []directus.CollectionField{
		{Field: "id"},
		{Field: "name"},
		{Field: "price"},
		{Field: "audit"},
		{Field: "tenant_id"},
	}
	got := directus.CompareStruct(fields, schemaProduct{})
	if len(got) != 0 {
		t.Errorf("extra Directus fields should not be flagged; got %+v", got)
	}
}
