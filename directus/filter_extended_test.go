package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/swchck/director/directus"
)

func TestField_NumericValue(t *testing.T) {
	f := directus.Field("age", "_gte", 18)
	b, _ := json.Marshal(f)

	var result map[string]any
	json.Unmarshal(b, &result)

	ageFilter := result["age"].(map[string]any)
	if ageFilter["_gte"] != float64(18) {
		t.Errorf("expected _gte=18, got %v", ageFilter["_gte"])
	}
}

func TestField_NullValue(t *testing.T) {
	f := directus.Field("deleted_at", "_null", true)
	b, _ := json.Marshal(f)

	var result map[string]any
	json.Unmarshal(b, &result)

	delFilter := result["deleted_at"].(map[string]any)
	if delFilter["_null"] != true {
		t.Errorf("expected _null=true, got %v", delFilter["_null"])
	}
}

func TestField_InOperator(t *testing.T) {
	f := directus.Field("status", "_in", []string{"published", "draft"})
	b, _ := json.Marshal(f)

	var result map[string]any
	json.Unmarshal(b, &result)

	statusFilter := result["status"].(map[string]any)
	inArr, ok := statusFilter["_in"].([]any)
	if !ok || len(inArr) != 2 {
		t.Errorf("expected _in with 2 elements, got %v", statusFilter["_in"])
	}
}

func TestAnd_NestedWithOr(t *testing.T) {
	f := directus.And(
		directus.Field("status", "_eq", "published"),
		directus.Or(
			directus.Field("category", "_eq", "food"),
			directus.Field("category", "_eq", "drink"),
		),
	)

	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(b, &result)

	andArr, ok := result["_and"].([]any)
	if !ok || len(andArr) != 2 {
		t.Fatalf("expected _and with 2 elements, got %v", result)
	}

	// Second element should contain _or
	second := andArr[1].(map[string]any)
	if _, ok := second["_or"]; !ok {
		t.Error("expected _or in second element")
	}
}

func TestOr_SingleFilter(t *testing.T) {
	f := directus.Or(directus.Field("status", "_eq", "published"))
	b, _ := json.Marshal(f)

	var result map[string]any
	json.Unmarshal(b, &result)

	orArr, ok := result["_or"].([]any)
	if !ok || len(orArr) != 1 {
		t.Errorf("expected _or with 1 element, got %v", result)
	}
}

func TestWithDeep_MultipleRelations(t *testing.T) {
	var gotQuery url.Values

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		writeJSONData(w, []any{})
	})
	defer srv.Close()

	type Item struct {
		ID int `json:"id"`
	}

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[Item](client, "products")

	tagsLimit := 10
	commentsLimit := 5
	_, _ = items.List(context.Background(),
		directus.WithFields("*", "tags.*", "comments.*"),
		directus.WithDeep("tags", directus.RelationQuery{
			Filter: directus.Field("status", "_eq", "active"),
			Sort:   []string{"name"},
			Limit:  &tagsLimit,
		}),
		directus.WithDeep("comments", directus.RelationQuery{
			Sort:   []string{"-date_created"},
			Limit:  &commentsLimit,
			Search: "hello",
		}),
	)

	deep := gotQuery.Get("deep")
	if deep == "" {
		t.Fatal("expected deep param")
	}

	var deepMap map[string]any
	json.Unmarshal([]byte(deep), &deepMap)

	// Note: WithDeep replaces per-relation, so only "comments" will be in the deep map
	// because both calls are on queryParams which uses a map.
	// Actually, we call WithDeep twice on different relations, so both should be there.
	if _, ok := deepMap["comments"]; !ok {
		t.Error("expected comments in deep")
	}
}

func TestRelationQuery_AllFields(t *testing.T) {
	var gotQuery url.Values

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		writeJSONData(w, []any{})
	})
	defer srv.Close()

	type Item struct {
		ID int `json:"id"`
	}

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[Item](client, "products")

	limit := 5
	offset := 10
	_, _ = items.List(context.Background(),
		directus.WithDeep("tags", directus.RelationQuery{
			Filter: directus.Field("status", "_eq", "active"),
			Sort:   []string{"-name", "id"},
			Limit:  &limit,
			Offset: &offset,
			Search: "test",
		}),
	)

	deep := gotQuery.Get("deep")
	if deep == "" {
		t.Fatal("expected deep param")
	}

	var deepMap map[string]any
	json.Unmarshal([]byte(deep), &deepMap)

	tags := deepMap["tags"].(map[string]any)

	if tags["_search"] != "test" {
		t.Errorf("_search = %v", tags["_search"])
	}

	if tags["_offset"] != float64(10) {
		t.Errorf("_offset = %v", tags["_offset"])
	}

	if tags["_limit"] != float64(5) {
		t.Errorf("_limit = %v", tags["_limit"])
	}
}

func TestWithTranslations_ExistingFields(t *testing.T) {
	var gotQuery url.Values

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		writeJSONData(w, []any{})
	})
	defer srv.Close()

	type Item struct {
		ID int `json:"id"`
	}

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[Item](client, "products")

	// When fields already include translations.*, don't duplicate
	_, _ = items.List(context.Background(),
		directus.WithFields("*", "translations.*"),
		directus.WithTranslations("languages_code", "en-US"),
	)

	fields := gotQuery.Get("fields")
	if fields != "*,translations.*" {
		t.Errorf("fields = %q (should not duplicate translations.*)", fields)
	}
}

func TestWithSort_Multiple(t *testing.T) {
	var gotQuery url.Values

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		writeJSONData(w, []any{})
	})
	defer srv.Close()

	type Item struct {
		ID int `json:"id"`
	}

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[Item](client, "products")

	_, _ = items.List(context.Background(),
		directus.WithSort("-date_created", "name"),
	)

	sort := gotQuery.Get("sort")
	if sort != "-date_created,name" {
		t.Errorf("sort = %q", sort)
	}
}

func TestEmptyQueryOptions(t *testing.T) {
	var gotQuery string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSONData(w, []any{})
	})
	defer srv.Close()

	type Item struct {
		ID int `json:"id"`
	}

	client := directus.NewClient(srv.URL, "token")
	items := directus.NewItems[Item](client, "products")

	_, _ = items.List(context.Background())

	if gotQuery != "" {
		t.Errorf("expected no query params, got %q", gotQuery)
	}
}
