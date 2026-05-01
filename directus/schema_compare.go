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

	// Reason is a short machine-readable code describing the mismatch.
	// Currently only "missing_in_directus" is emitted.
	Reason string
}

// CompareStruct reflects sample (a zero-value struct of the type backing a
// collection) against fields fetched from Directus and returns drifts where
// a Go-declared field is absent from the Directus schema.
//
// Only the missing-field direction is reported. Fields that exist in
// Directus but not in Go are silently ignored — they're typically intentional
// (admin-only fields, system metadata, etc.).
//
// Rules:
//   - Unexported fields are skipped.
//   - Fields with no `json` tag are skipped (no contract to compare).
//   - Tag value "-" is skipped (explicit opt-out).
//   - Embedded structs are not flattened — the embed itself is checked
//     against Directus by its declared json tag if any.
//
// Pass either a struct value or a pointer to a struct. Returns nil if sample
// is not a struct.
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
