package directus

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Filter represents a Directus filter expression.
// It serializes to the JSON format expected by the Directus filter query parameter.
type Filter map[string]any

// Field creates a single-field filter: {"field": {"op": value}}.
//
// Example:
//
//	directus.Field("status", "_eq", "published")
//	// => {"status": {"_eq": "published"}}
func Field(field, op string, value any) Filter {
	return Filter{field: map[string]any{op: value}}
}

// And combines multiple filters with the _and logical operator.
func And(filters ...Filter) Filter {
	items := make([]Filter, len(filters))
	copy(items, filters)

	return Filter{"_and": items}
}

// Or combines multiple filters with the _or logical operator.
func Or(filters ...Filter) Filter {
	items := make([]Filter, len(filters))
	copy(items, filters)

	return Filter{"_or": items}
}

// RelationQuery configures query parameters applied to a nested relational field
// via the Directus "deep" parameter.
//
// This is how you filter, sort, or limit related items in M2O, O2M, M2M, and M2A
// relationships, as well as translations.
//
// Example — fetch only English translations:
//
//	directus.WithDeep("translations", directus.RelationQuery{
//	    Filter: directus.Field("languages_code", "_eq", "en-US"),
//	})
type RelationQuery struct {
	Filter Filter
	Sort   []string
	Limit  *int
	Offset *int
	Search string
}

// toMap converts the relation query to Directus deep parameter format.
func (rq RelationQuery) toMap() map[string]any {
	m := make(map[string]any)

	if rq.Filter != nil {
		m["_filter"] = rq.Filter
	}

	if len(rq.Sort) > 0 {
		m["_sort"] = strings.Join(rq.Sort, ",")
	}

	if rq.Limit != nil {
		m["_limit"] = *rq.Limit
	}

	if rq.Offset != nil {
		m["_offset"] = *rq.Offset
	}

	if rq.Search != "" {
		m["_search"] = rq.Search
	}

	return m
}

// QueryOption configures query parameters for a Directus API request.
type QueryOption func(*queryParams)

type queryParams struct {
	filter Filter
	sort   []string
	limit  *int
	offset *int
	fields []string
	deep   map[string]RelationQuery
}

// WithFilter sets the filter for the request.
func WithFilter(f Filter) QueryOption {
	return func(q *queryParams) {
		q.filter = f
	}
}

// WithSort sets the sort order. Prefix a field with "-" for descending.
//
// Example:
//
//	directus.WithSort("-date_created", "name")
func WithSort(fields ...string) QueryOption {
	return func(q *queryParams) {
		q.sort = fields
	}
}

// WithLimit sets the maximum number of items to return.
func WithLimit(n int) QueryOption {
	return func(q *queryParams) {
		q.limit = &n
	}
}

// WithOffset sets the number of items to skip.
func WithOffset(n int) QueryOption {
	return func(q *queryParams) {
		q.offset = &n
	}
}

// WithFields restricts the response to the specified fields.
//
// Use dot notation to include relational data:
//
//	directus.WithFields("*", "author.*")           // M2O: include the related author
//	directus.WithFields("*", "tags.*")              // M2M: include related tags
//	directus.WithFields("*", "translations.*")      // include all translations
//	directus.WithFields("*", "comments.author.*")   // nested: comment authors
func WithFields(fields ...string) QueryOption {
	return func(q *queryParams) {
		q.fields = fields
	}
}

// WithDeep configures query parameters for a nested relational field.
// Multiple calls to WithDeep for different relations are merged.
//
// This supports all Directus relation types: M2O, O2M, M2M, M2A, and translations.
//
// Example — filter M2M tags to only published ones:
//
//	directus.WithDeep("tags", directus.RelationQuery{
//	    Filter: directus.Field("status", "_eq", "published"),
//	    Sort:   []string{"name"},
//	})
//
// Example — limit O2M comments and sort by date:
//
//	directus.WithDeep("comments", directus.RelationQuery{
//	    Sort:  []string{"-date_created"},
//	    Limit: new(int), // or use &myVar for non-zero values
//	})
func WithDeep(relation string, rq RelationQuery) QueryOption {
	return func(q *queryParams) {
		if q.deep == nil {
			q.deep = make(map[string]RelationQuery)
		}

		q.deep[relation] = rq
	}
}

// WithTranslations is a convenience option that includes all translation fields
// and optionally filters by language code.
//
// It combines WithFields("*", "translations.*") with a deep filter on the
// specified language field.
//
// langField is the name of the language code field in the translations junction
// (typically "languages_code" in Directus).
//
// Example:
//
//	directus.WithTranslations("languages_code", "en-US")
func WithTranslations(langField, langCode string) QueryOption {
	return func(q *queryParams) {
		// Ensure translations are included in fields.
		hasTranslations := false
		for _, f := range q.fields {
			if f == "translations.*" || f == "translations" {
				hasTranslations = true
				break
			}
		}

		if !hasTranslations {
			if len(q.fields) == 0 {
				q.fields = []string{"*", "translations.*"}
			} else {
				q.fields = append(q.fields, "translations.*")
			}
		}

		// Apply language filter via deep.
		if q.deep == nil {
			q.deep = make(map[string]RelationQuery)
		}

		q.deep["translations"] = RelationQuery{
			Filter: Field(langField, "_eq", langCode),
		}
	}
}

// encode converts the query parameters into url.Values suitable for a Directus request.
func (qp *queryParams) encode() (url.Values, error) {
	v := url.Values{}

	if qp.filter != nil {
		b, err := json.Marshal(qp.filter)
		if err != nil {
			return nil, fmt.Errorf("directus: marshal filter: %w", err)
		}

		v.Set("filter", string(b))
	}

	if len(qp.sort) > 0 {
		v.Set("sort", strings.Join(qp.sort, ","))
	}

	if qp.limit != nil {
		v.Set("limit", strconv.Itoa(*qp.limit))
	}

	if qp.offset != nil {
		v.Set("offset", strconv.Itoa(*qp.offset))
	}

	if len(qp.fields) > 0 {
		v.Set("fields", strings.Join(qp.fields, ","))
	}

	if len(qp.deep) > 0 {
		deepMap := make(map[string]any, len(qp.deep))
		for relation, rq := range qp.deep {
			deepMap[relation] = rq.toMap()
		}

		b, err := json.Marshal(deepMap)
		if err != nil {
			return nil, fmt.Errorf("directus: marshal deep: %w", err)
		}

		v.Set("deep", string(b))
	}

	return v, nil
}

// buildQuery applies options and returns encoded url.Values.
func buildQuery(opts []QueryOption) (url.Values, error) {
	qp := &queryParams{}
	for _, opt := range opts {
		opt(qp)
	}

	return qp.encode()
}
