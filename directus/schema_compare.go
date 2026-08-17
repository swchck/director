package directus

import (
	"reflect"
	"strings"
)

// SchemaDrift describes a single mismatch between a Go struct and a Directus
// collection schema. Returned by CompareStruct.
type SchemaDrift struct {
	// Field is the Go struct field name (e.g. "Title").
	Field string

	// JSONTag is the json tag value used to map onto Directus (e.g. "title").
	JSONTag string

	// Reason is a machine-readable code; only "missing_in_directus" is emitted today.
	Reason string
}

// CompareStruct reports json-tagged fields of sample (a struct or pointer to one)
// that Directus does not declare. Direction and skip rules: docs/directus-package.md.
func CompareStruct(fields []CollectionField, sample any) []SchemaDrift {
	t := reflect.TypeOf(sample)
	if t == nil {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	directusFields := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		directusFields[f.Field] = struct{}{}
	}

	var drifts []SchemaDrift
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		if _, ok := directusFields[name]; !ok {
			drifts = append(drifts, SchemaDrift{
				Field:   f.Name,
				JSONTag: name,
				Reason:  "missing_in_directus",
			})
		}
	}
	return drifts
}
