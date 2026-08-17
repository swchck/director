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

// Field creates a single-field filter: Field("status", "_eq", "published")
// serializes to {"status": {"_eq": "published"}}.
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

// RelationQuery filters, sorts or limits the items of one nested relational field
// (M2O, O2M, M2M, M2A, translations) via the Directus "deep" parameter.
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

// WithFields restricts the response to the given fields. Dot notation pulls in
// relational data, at any depth: WithFields("*", "comments.author.*").
func WithFields(fields ...string) QueryOption {
	return func(q *queryParams) {
		q.fields = fields
	}
}

// WithDeep applies rq to a nested relational field. Calls for distinct relations
// merge; a repeated relation keeps the last rq.
func WithDeep(relation string, rq RelationQuery) QueryOption {
	return func(q *queryParams) {
		if q.deep == nil {
			q.deep = make(map[string]RelationQuery)
		}

		q.deep[relation] = rq
	}
}

// WithTranslations includes translations.* and keeps only langCode. langField is the
// language column on the junction collection, conventionally "languages_code".
func WithTranslations(langField, langCode string) QueryOption {
	return func(q *queryParams) {
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
