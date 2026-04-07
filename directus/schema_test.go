package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestCreateCollection_AddsSchemaIfMissing(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{"collection": "test"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.CreateCollection(context.Background(), directus.CreateCollectionInput{
		Collection: "test",
		Fields:     []directus.FieldInput{directus.PrimaryKeyField("id")},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Schema should be present (not nil) even when not specified.
	if gotBody["schema"] == nil {
		t.Error("schema should be {} not nil")
	}
}

func TestCreateCollection_SplitsSpecialFields(t *testing.T) {
	var requests []string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		writeJSONData(w, map[string]any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.CreateCollection(context.Background(), directus.CreateCollectionInput{
		Collection: "test",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.StringField("name"),
			directus.DateCreatedField(), // has special: date-created → deferred
			directus.DateUpdatedField(), // has special: date-updated → deferred
		},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Expect: 1 POST /collections (with PK + name), 2 POST /fields/test (date_created, date_updated)
	if len(requests) != 3 {
		t.Fatalf("expected 3 requests, got %d: %v", len(requests), requests)
	}

	if requests[0] != "POST /collections" {
		t.Errorf("request 0 = %q", requests[0])
	}

	if requests[1] != "POST /fields/test" {
		t.Errorf("request 1 = %q", requests[1])
	}

	if requests[2] != "POST /fields/test" {
		t.Errorf("request 2 = %q", requests[2])
	}
}

func TestCreateCollectionFolder_SchemaNil(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.CreateCollectionFolder(context.Background(), "my_folder", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Folder should have schema: null (omitted in JSON when Schema is nil pointer).
	if gotBody["schema"] != nil {
		t.Errorf("folder schema should be null, got %v", gotBody["schema"])
	}
}

func TestFieldHelpers_HaveInterface(t *testing.T) {
	tests := []struct {
		name  string
		field directus.FieldInput
		iface string
	}{
		{"String", directus.StringField("x"), "input"},
		{"Text", directus.TextField("x"), "input-multiline"},
		{"Integer", directus.IntegerField("x"), "input"},
		{"Float", directus.FloatField("x"), "input"},
		{"Decimal", directus.DecimalField("x"), "input"},
		{"Boolean", directus.BooleanField("x"), "boolean"},
		{"JSON", directus.JSONField("x"), "input-code"},
		{"M2O", directus.M2OField("x", "rel"), "select-dropdown-m2o"},
		{"Status", directus.StatusField(), "select-dropdown"},
		{"PK", directus.PrimaryKeyField("id"), "input"},
		{"DateCreated", directus.DateCreatedField(), "datetime"},
		{"DateUpdated", directus.DateUpdatedField(), "datetime"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.field.Meta == nil {
				t.Fatal("Meta is nil")
			}

			if tt.field.Meta.Interface != tt.iface {
				t.Errorf("Interface = %q, want %q", tt.field.Meta.Interface, tt.iface)
			}
		})
	}
}

func TestM2O_Builder(t *testing.T) {
	rel := directus.M2O("products", "category_id", "categories")

	if rel.Collection != "products" || rel.Field != "category_id" || rel.Related != "categories" {
		t.Errorf("M2O = %+v", rel)
	}
}

func TestM2M_Builder(t *testing.T) {
	source, target := directus.M2M(directus.M2MInput{
		Collection:          "products",
		Related:             "tags",
		JunctionCollection:  "products_tags",
		JunctionSourceField: "products_id",
		JunctionTargetField: "tags_id",
		AliasField:          "tags",
	})

	if source.Collection != "products_tags" || source.Field != "products_id" || source.Related != "products" {
		t.Errorf("source = %+v", source)
	}

	if target.Collection != "products_tags" || target.Field != "tags_id" || target.Related != "tags" {
		t.Errorf("target = %+v", target)
	}

	if source.Meta == nil || source.Meta.JunctionField == nil || *source.Meta.JunctionField != "tags_id" {
		t.Error("source meta junction_field not set")
	}
}

func TestTranslations_Builder(t *testing.T) {
	src, lang := directus.Translations("products", "products_tr", "products_id", "lang_code", "languages")

	if src.Collection != "products_tr" || src.Related != "products" {
		t.Errorf("source = %+v", src)
	}

	if lang.Collection != "products_tr" || lang.Field != "lang_code" || lang.Related != "languages" {
		t.Errorf("lang = %+v", lang)
	}
}
