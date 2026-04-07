package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestCreateField(t *testing.T) {
	var gotBody directus.FieldInput

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/fields/products" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{"field": gotBody.Field})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.CreateField(context.Background(), "products", directus.StringField("name"))
	if err != nil {
		t.Fatal(err)
	}

	if gotBody.Field != "name" {
		t.Errorf("field = %q", gotBody.Field)
	}

	if gotBody.Type != directus.FieldTypeString {
		t.Errorf("type = %q", gotBody.Type)
	}
}

func TestUpdateField(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/fields/products/name" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, map[string]any{"field": "name"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.UpdateField(context.Background(), "products", "name", directus.FieldInput{
		Field: "name",
		Type:  directus.FieldTypeString,
		Meta:  &directus.FieldMeta{Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeleteField(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/fields/products/old_field" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteField(context.Background(), "products", "old_field")
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateRelation(t *testing.T) {
	var gotBody directus.RelationInput

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/relations" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	rel := directus.M2O("products", "category_id", "categories")
	err := client.CreateRelation(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}

	if gotBody.Collection != "products" {
		t.Errorf("Collection = %q", gotBody.Collection)
	}

	if gotBody.Field != "category_id" {
		t.Errorf("Field = %q", gotBody.Field)
	}

	if gotBody.Related != "categories" {
		t.Errorf("Related = %q", gotBody.Related)
	}
}

func TestDeleteRelation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/relations/products/category_id" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteRelation(context.Background(), "products", "category_id")
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetRelations(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relations/products" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []map[string]any{
			{"collection": "products", "field": "category_id", "related_collection": "categories"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	raw, err := client.GetRelations(context.Background(), "products")
	if err != nil {
		t.Fatal(err)
	}

	if len(raw) == 0 {
		t.Error("expected non-empty relations")
	}
}

func TestGetRelations_All(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relations" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []map[string]any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.GetRelations(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestMoveCollectionToFolder(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/collections/products" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.MoveCollectionToFolder(context.Background(), "products", "content")
	if err != nil {
		t.Fatal(err)
	}

	meta, ok := gotBody["meta"].(map[string]any)
	if !ok {
		t.Fatal("expected meta in body")
	}

	if meta["group"] != "content" {
		t.Errorf("group = %v", meta["group"])
	}
}

func TestDeleteCollection(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/collections/products" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteCollection(context.Background(), "products")
	if err != nil {
		t.Fatal(err)
	}
}

func TestO2M_Builder(t *testing.T) {
	rel := directus.O2M("categories", "products", "products", "category_id")

	if rel.Collection != "products" {
		t.Errorf("Collection = %q", rel.Collection)
	}

	if rel.Field != "category_id" {
		t.Errorf("Field = %q", rel.Field)
	}

	if rel.Related != "categories" {
		t.Errorf("Related = %q", rel.Related)
	}

	if rel.Meta == nil || rel.Meta.OneField == nil || *rel.Meta.OneField != "products" {
		t.Error("OneField not set correctly")
	}
}

func TestUUIDPrimaryKeyField(t *testing.T) {
	f := directus.UUIDPrimaryKeyField("id")

	if f.Type != directus.FieldTypeUUID {
		t.Errorf("Type = %q", f.Type)
	}

	if f.Meta == nil || f.Meta.Interface != "input" {
		t.Error("Meta not set correctly")
	}

	if !f.Meta.Hidden || !f.Meta.Readonly {
		t.Error("expected Hidden=true, Readonly=true")
	}

	hasUUID := false
	for _, s := range f.Meta.Special {
		if s == "uuid" {
			hasUUID = true
		}
	}
	if !hasUUID {
		t.Error("expected uuid special tag")
	}
}

func TestSortField(t *testing.T) {
	f := directus.SortField()

	if f.Field != "sort" {
		t.Errorf("Field = %q", f.Field)
	}

	if f.Type != directus.FieldTypeInteger {
		t.Errorf("Type = %q", f.Type)
	}

	if f.Meta == nil || !f.Meta.Hidden {
		t.Error("expected Hidden=true")
	}
}


func TestCreateCollectionFolder_WithMeta(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.CreateCollectionFolder(context.Background(), "my_folder", &directus.CollectionMeta{
		Icon:     "folder",
		Collapse: directus.CollapseClosed,
	})
	if err != nil {
		t.Fatal(err)
	}

	meta, ok := gotBody["meta"].(map[string]any)
	if !ok {
		t.Fatal("expected meta")
	}

	if meta["collapse"] != "closed" {
		t.Errorf("collapse = %v", meta["collapse"])
	}
}

func TestCreateCollectionFolder_DefaultCollapse(t *testing.T) {
	var gotBody map[string]any

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, map[string]any{})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.CreateCollectionFolder(context.Background(), "my_folder", &directus.CollectionMeta{
		Icon: "folder",
	})
	if err != nil {
		t.Fatal(err)
	}

	meta, ok := gotBody["meta"].(map[string]any)
	if !ok {
		t.Fatal("expected meta")
	}

	if meta["collapse"] != "open" {
		t.Errorf("collapse = %v (expected default 'open')", meta["collapse"])
	}
}

func TestCreateCollection_NoSpecialFields(t *testing.T) {
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
			directus.IntegerField("count"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// No special fields, so only 1 POST /collections request
	if len(requests) != 1 {
		t.Errorf("expected 1 request, got %d: %v", len(requests), requests)
	}
}

func TestM2OField_Builder(t *testing.T) {
	f := directus.M2OField("category_id", "categories")

	if f.Field != "category_id" {
		t.Errorf("Field = %q", f.Field)
	}

	if f.Type != directus.FieldTypeInteger {
		t.Errorf("Type = %q", f.Type)
	}

	if f.Meta == nil || f.Meta.Interface != "select-dropdown-m2o" {
		t.Error("Interface not set correctly")
	}

	hasM2O := false
	for _, s := range f.Meta.Special {
		if s == "m2o" {
			hasM2O = true
		}
	}
	if !hasM2O {
		t.Error("expected m2o special tag")
	}
}
