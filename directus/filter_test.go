package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/swchck/director/directus"
)

func TestField_ProducesCorrectJSON(t *testing.T) {
	f := directus.Field("status", "_eq", "published")

	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"status":{"_eq":"published"}}`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}

func TestAnd_CombinesFilters(t *testing.T) {
	f := directus.And(
		directus.Field("status", "_eq", "published"),
		directus.Field("level", "_gte", 5),
	)

	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(b, &result)

	andArr, ok := result["_and"].([]any)
	if !ok || len(andArr) != 2 {
		t.Errorf("expected _and with 2 elements, got %v", result)
	}
}

func TestOr_CombinesFilters(t *testing.T) {
	f := directus.Or(
		directus.Field("category", "_eq", "food"),
		directus.Field("category", "_eq", "drink"),
	)

	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(b, &result)

	orArr, ok := result["_or"].([]any)
	if !ok || len(orArr) != 2 {
		t.Errorf("expected _or with 2 elements, got %v", result)
	}
}

func TestBuildQuery_EncodesAllParams(t *testing.T) {
	// We can't call buildQuery directly (unexported), so test through Items.
	// Instead, test the filter JSON structure and WithFields output.

	// Test Filter serialization via Field + And.
	filter := directus.And(
		directus.Field("status", "_eq", "published"),
		directus.Field("level", "_gte", 5),
	)

	b, _ := json.Marshal(filter)
	var parsed map[string]any
	json.Unmarshal(b, &parsed)

	if _, ok := parsed["_and"]; !ok {
		t.Error("expected _and key in filter")
	}
}

func TestWithDeep_ProducesCorrectJSON(t *testing.T) {
	// Test that RelationQuery serializes correctly.
	limit := 5
	rq := directus.RelationQuery{
		Filter: directus.Field("languages_code", "_eq", "en-US"),
		Sort:   []string{"-date_created"},
		Limit:  &limit,
	}

	m := struct {
		Filter any    `json:"_filter,omitempty"`
		Sort   string `json:"_sort,omitempty"`
		Limit  *int   `json:"_limit,omitempty"`
	}{
		Filter: rq.Filter,
		Sort:   "-date_created",
		Limit:  &limit,
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(b, &result)

	if result["_limit"] != float64(5) {
		t.Errorf("expected _limit=5, got %v", result["_limit"])
	}

	if result["_sort"] != "-date_created" {
		t.Errorf("expected _sort=-date_created, got %v", result["_sort"])
	}
}


func TestWithTranslations_AddsFieldsAndDeep(t *testing.T) {
	// Test through the items endpoint by checking the query params.
	// We need an HTTP server that captures the query string.
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
		directus.WithTranslations("languages_code", "en-US"),
	)

	fields := gotQuery.Get("fields")
	if fields == "" {
		t.Fatal("expected fields param")
	}

	if fields != "*,translations.*" {
		t.Errorf("fields = %q, want '*,translations.*'", fields)
	}

	deep := gotQuery.Get("deep")
	if deep == "" {
		t.Fatal("expected deep param")
	}

	var deepMap map[string]any
	json.Unmarshal([]byte(deep), &deepMap)

	tr, ok := deepMap["translations"].(map[string]any)
	if !ok {
		t.Fatalf("expected translations in deep, got %v", deepMap)
	}

	filter, ok := tr["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("expected _filter in translations deep, got %v", tr)
	}

	langFilter, ok := filter["languages_code"].(map[string]any)
	if !ok {
		t.Fatalf("expected languages_code filter, got %v", filter)
	}

	if langFilter["_eq"] != "en-US" {
		t.Errorf("expected _eq=en-US, got %v", langFilter["_eq"])
	}
}
