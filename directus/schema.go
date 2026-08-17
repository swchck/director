package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// CollapseMode controls the default display state of a collection folder.
type CollapseMode string

const (
	CollapseOpen   CollapseMode = "open"
	CollapseClosed CollapseMode = "closed"
	CollapseLocked CollapseMode = "locked"
)

// CollectionMeta configures a Directus collection.
type CollectionMeta struct {
	Collection string `json:"collection,omitempty"`
	// Singleton makes the collection hold exactly one item.
	Singleton bool   `json:"singleton,omitempty"`
	Note      string `json:"note,omitempty"`
	Icon      string `json:"icon,omitempty"`
	Color     string `json:"color,omitempty"`
	Hidden    bool   `json:"hidden,omitempty"`
	Sort      int    `json:"sort,omitempty"`
	// Group is the parent collection folder this collection sits in.
	Group string `json:"group,omitempty"`
	// Collapse applies only when this collection is itself a folder.
	Collapse CollapseMode `json:"collapse,omitempty"`
}

// CreateCollectionInput is the request body for creating a Directus collection.
type CreateCollectionInput struct {
	Collection string          `json:"collection"`
	Meta       *CollectionMeta `json:"meta,omitempty"`
	// Schema must be present, hence no omitempty: {} makes Directus create the
	// database table, null (nil pointer) makes a folder with no table.
	Schema *SchemaOptions `json:"schema"`
	Fields []FieldInput   `json:"fields,omitempty"`

	// isFolder keeps CreateCollection from auto-filling Schema with an empty object.
	isFolder bool `json:"-"`
}

// SchemaOptions configures the database schema for a collection.
type SchemaOptions struct {
	Name    string `json:"name,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// CreateCollection creates a new Directus collection and its fields.
func (c *Client) CreateCollection(ctx context.Context, input CreateCollectionInput) error {
	// Empty rather than absent: see CreateCollectionInput.Schema.
	if input.Schema == nil && !input.isFolder {
		input.Schema = &SchemaOptions{}
	}

	// Directus 11 quirk: fields carrying special metadata (date-created, uuid, ...)
	// lose that behaviour when created inline, so they are created afterwards.
	inlineFields, deferredFields := splitSpecialFields(input.Fields)
	input.Fields = inlineFields

	_, err := c.Post(ctx, "collections", input)
	if err != nil {
		return fmt.Errorf("directus: create collection %s: %w", input.Collection, err)
	}

	for _, field := range deferredFields {
		if err := c.CreateField(ctx, input.Collection, field); err != nil {
			return fmt.Errorf("directus: create collection %s field %s: %w", input.Collection, field.Field, err)
		}
	}

	return nil
}

// splitSpecialFields separates fields that must be created after the collection.
func splitSpecialFields(fields []FieldInput) (inline, deferred []FieldInput) {
	for _, f := range fields {
		if f.Meta != nil && hasSpecialTag(f.Meta.Special) {
			deferred = append(deferred, f)
		} else {
			inline = append(inline, f)
		}
	}

	return inline, deferred
}

func hasSpecialTag(special []string) bool {
	for _, s := range special {
		switch s {
		case "date-created", "date-updated", "uuid", "hash", "conceal":
			return true
		}
	}

	return false
}

// CreateCollectionFolder creates a virtual sidebar folder with no database table.
// Collections join it by setting CollectionMeta.Group to name.
func (c *Client) CreateCollectionFolder(ctx context.Context, name string, meta *CollectionMeta) error {
	if meta == nil {
		meta = &CollectionMeta{}
	}

	if meta.Collapse == "" {
		meta.Collapse = CollapseOpen
	}

	input := CreateCollectionInput{
		Collection: name,
		Meta:       meta,
		isFolder:   true,
		// Schema stays nil so it serializes as null: no table.
	}

	_, err := c.Post(ctx, "collections", input)
	if err != nil {
		return fmt.Errorf("directus: create collection folder %s: %w", name, err)
	}

	return nil
}

// MoveCollectionToFolder moves an existing collection into a folder by updating its group.
func (c *Client) MoveCollectionToFolder(ctx context.Context, collection, folder string) error {
	_, err := c.Patch(ctx, "collections/"+collection, map[string]any{
		"meta": map[string]any{
			"group": folder,
		},
	})
	if err != nil {
		return fmt.Errorf("directus: move %s to folder %s: %w", collection, folder, err)
	}

	return nil
}

// DeleteCollection removes a Directus collection and all its data.
func (c *Client) DeleteCollection(ctx context.Context, collection string) error {
	if err := c.Delete(ctx, "collections/"+collection); err != nil {
		return fmt.Errorf("directus: delete collection %s: %w", collection, err)
	}

	return nil
}

// FieldType represents Directus field types.
type FieldType string

const (
	FieldTypeString    FieldType = "string"
	FieldTypeText      FieldType = "text"
	FieldTypeInteger   FieldType = "integer"
	FieldTypeBigInt    FieldType = "bigInteger"
	FieldTypeFloat     FieldType = "float"
	FieldTypeDecimal   FieldType = "decimal"
	FieldTypeBoolean   FieldType = "boolean"
	FieldTypeJSON      FieldType = "json"
	FieldTypeCSV       FieldType = "csv"
	FieldTypeUUID      FieldType = "uuid"
	FieldTypeHash      FieldType = "hash"
	FieldTypeDate      FieldType = "date"
	FieldTypeTime      FieldType = "time"
	FieldTypeDatetime  FieldType = "dateTime"
	FieldTypeTimestamp FieldType = "timestamp"
)

// FieldMeta configures Directus-level field metadata. The accepted Interface,
// Display, Width and Special values are listed in docs/directus-package.md.
type FieldMeta struct {
	Required bool   `json:"required,omitempty"`
	Readonly bool   `json:"readonly,omitempty"`
	Hidden   bool   `json:"hidden,omitempty"`
	Note     string `json:"note,omitempty"`
	// Interface is the Directus UI editing widget. Required for UI display.
	Interface string `json:"interface,omitempty"`
	// Display is how the value appears in list views. Required for UI display.
	Display string `json:"display,omitempty"`
	// Special tags for Directus internal handling (e.g. "m2o", "uuid", "cast-boolean").
	Special []string `json:"special,omitempty"`
	// Sort order in the collection.
	Sort int `json:"sort,omitempty"`
	// Width in the detail view: "half", "half-left", "half-right", "full", "fill".
	Width string `json:"width,omitempty"`
	// Options for the interface component.
	Options map[string]any `json:"options,omitempty"`
	// DisplayOptions for the display component.
	DisplayOptions map[string]any `json:"display_options,omitempty"`
}

// FieldSchema configures the database-level field schema.
type FieldSchema struct {
	DefaultValue     any    `json:"default_value,omitempty"`
	MaxLength        *int   `json:"max_length,omitempty"`
	IsNullable       *bool  `json:"is_nullable,omitempty"`
	IsUnique         bool   `json:"is_unique,omitempty"`
	IsPrimaryKey     bool   `json:"is_primary_key,omitempty"`
	HasAutoIncrement bool   `json:"has_auto_increment,omitempty"`
	Comment          string `json:"comment,omitempty"`
}

// FieldInput is the request body for creating or updating a field.
type FieldInput struct {
	Field  string       `json:"field"`
	Type   FieldType    `json:"type"`
	Meta   *FieldMeta   `json:"meta,omitempty"`
	Schema *FieldSchema `json:"schema,omitempty"`
}

// PrimaryKeyField returns a standard auto-increment integer primary key field.
func PrimaryKeyField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeInteger,
		Meta: &FieldMeta{
			Hidden:    true,
			Readonly:  true,
			Interface: "input",
		},
		Schema: &FieldSchema{
			IsNullable:       new(bool),
			IsPrimaryKey:     true,
			HasAutoIncrement: true,
		},
	}
}

// UUIDPrimaryKeyField returns a UUID primary key field.
func UUIDPrimaryKeyField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeUUID,
		Meta: &FieldMeta{
			Hidden:    true,
			Readonly:  true,
			Interface: "input",
			Special:   []string{"uuid"},
		},
		Schema: &FieldSchema{
			IsNullable: new(bool),
		},
	}
}

// StatusField returns a standard Directus status field with draft/published/archived.
func StatusField() FieldInput {
	return FieldInput{
		Field: "status",
		Type:  FieldTypeString,
		Meta: &FieldMeta{
			Interface: "select-dropdown",
			Display:   "labels",
			Width:     "full",
			Options: map[string]any{
				"choices": []map[string]any{
					{"text": "Draft", "value": "draft", "color": "#FFC23B"},
					{"text": "Published", "value": "published", "color": "#2ECDA7"},
					{"text": "Archived", "value": "archived", "color": "#A2B5CD"},
				},
			},
		},
		Schema: &FieldSchema{
			DefaultValue: "draft",
		},
	}
}

// SortField returns a standard sort/order field.
func SortField() FieldInput {
	return FieldInput{
		Field: "sort",
		Type:  FieldTypeInteger,
		Meta: &FieldMeta{
			Interface: "input",
			Hidden:    true,
		},
	}
}

// DateCreatedField returns a standard date_created field.
func DateCreatedField() FieldInput {
	return FieldInput{
		Field: "date_created",
		Type:  FieldTypeTimestamp,
		Meta: &FieldMeta{
			Special:   []string{"date-created", "cast-timestamp"},
			Interface: "datetime",
			Display:   "datetime",
			Readonly:  true,
			Hidden:    true,
			Width:     "half",
		},
	}
}

// DateUpdatedField returns a standard date_updated field (used for version detection).
func DateUpdatedField() FieldInput {
	return FieldInput{
		Field: "date_updated",
		Type:  FieldTypeTimestamp,
		Meta: &FieldMeta{
			Special:   []string{"date-updated", "cast-timestamp"},
			Interface: "datetime",
			Display:   "datetime",
			Readonly:  true,
			Hidden:    true,
			Width:     "half",
		},
	}
}

// StringField returns a text input field with proper UI configuration.
func StringField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeString,
		Meta: &FieldMeta{
			Interface: "input",
		},
	}
}

// TextField returns a multiline text field.
func TextField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeText,
		Meta: &FieldMeta{
			Interface: "input-multiline",
		},
	}
}

// IntegerField returns an integer input field.
func IntegerField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeInteger,
		Meta: &FieldMeta{
			Interface: "input",
		},
	}
}

// FloatField returns a float/decimal input field.
func FloatField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeFloat,
		Meta: &FieldMeta{
			Interface: "input",
		},
	}
}

// DecimalField returns a decimal input field.
func DecimalField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeDecimal,
		Meta: &FieldMeta{
			Interface: "input",
		},
	}
}

// BooleanField returns a toggle switch field.
func BooleanField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeBoolean,
		Meta: &FieldMeta{
			Interface: "boolean",
			Display:   "boolean",
			Special:   []string{"cast-boolean"},
			Width:     "half",
		},
	}
}

// JSONField returns a JSON editor field.
func JSONField(name string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeJSON,
		Meta: &FieldMeta{
			Interface: "input-code",
			Display:   "raw",
			Special:   []string{"cast-json"},
			Options:   map[string]any{"language": "json"},
		},
	}
}

// M2OField returns the foreign-key field for a Many-to-One relation, with the
// dropdown selector configured. relatedCollection is bound by M2O, not by the field.
func M2OField(name, relatedCollection string) FieldInput {
	return FieldInput{
		Field: name,
		Type:  FieldTypeInteger,
		Meta: &FieldMeta{
			Interface: "select-dropdown-m2o",
			Display:   "related-values",
			Special:   []string{"m2o"},
			DisplayOptions: map[string]any{
				"template": "{{id}}",
			},
		},
	}
}

// CreateField adds a field to an existing collection.
func (c *Client) CreateField(ctx context.Context, collection string, input FieldInput) error {
	_, err := c.Post(ctx, "fields/"+collection, input)
	if err != nil {
		return fmt.Errorf("directus: create field %s.%s: %w", collection, input.Field, err)
	}

	return nil
}

// UpdateField modifies an existing field.
func (c *Client) UpdateField(ctx context.Context, collection, field string, input FieldInput) error {
	_, err := c.Patch(ctx, "fields/"+collection+"/"+field, input)
	if err != nil {
		return fmt.Errorf("directus: update field %s.%s: %w", collection, field, err)
	}

	return nil
}

// DeleteField removes a field from a collection.
func (c *Client) DeleteField(ctx context.Context, collection, field string) error {
	if err := c.Delete(ctx, "fields/"+collection+"/"+field); err != nil {
		return fmt.Errorf("directus: delete field %s.%s: %w", collection, field, err)
	}

	return nil
}

// CollectionField is the subset of a Directus field that ListFields returns:
// enough for schema drift detection, not enough to recreate the field.
type CollectionField struct {
	Field string    `json:"field"`
	Type  FieldType `json:"type"`
}

// ListFields returns the fields declared on a Directus collection.
func (c *Client) ListFields(ctx context.Context, collection string) ([]CollectionField, error) {
	raw, err := c.Get(ctx, "fields/"+collection, nil)
	if err != nil {
		return nil, fmt.Errorf("directus: list fields %s: %w", collection, err)
	}

	var fields []CollectionField
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("directus: decode fields %s: %w", collection, err)
	}
	return fields, nil
}

// RelationInput defines a relationship between two collections.
type RelationInput struct {
	Collection string          `json:"collection"`
	Field      string          `json:"field"`
	Related    string          `json:"related_collection"`
	Meta       *RelationMeta   `json:"meta,omitempty"`
	Schema     *RelationSchema `json:"schema,omitempty"`
}

// RelationMeta configures Directus-level relation metadata.
type RelationMeta struct {
	// SortField is used for manual sorting of related items.
	SortField *string `json:"sort_field,omitempty"`
	// OneDeselectAction: "nullify" or "delete".
	OneDeselectAction string `json:"one_deselect_action,omitempty"`
	// OneField is the field on the "one" side that stores the O2M alias.
	OneField *string `json:"one_field,omitempty"`
	// JunctionField is the field on the junction collection pointing to the "many" side (M2M).
	JunctionField *string `json:"junction_field,omitempty"`
}

// RelationSchema configures the database-level relation schema.
type RelationSchema struct {
	OnDelete string `json:"on_delete,omitempty"` // "SET NULL", "CASCADE", "NO ACTION"
}

// CreateRelation creates a relationship in Directus.
func (c *Client) CreateRelation(ctx context.Context, input RelationInput) error {
	_, err := c.Post(ctx, "relations", input)
	if err != nil {
		return fmt.Errorf("directus: create relation %s.%s -> %s: %w",
			input.Collection, input.Field, input.Related, err)
	}

	return nil
}

// DeleteRelation removes a relationship.
func (c *Client) DeleteRelation(ctx context.Context, collection, field string) error {
	if err := c.Delete(ctx, "relations/"+collection+"/"+field); err != nil {
		return fmt.Errorf("directus: delete relation %s.%s: %w", collection, field, err)
	}

	return nil
}

// GetRelations lists all relations, optionally filtered to a collection.
func (c *Client) GetRelations(ctx context.Context, collection string) (json.RawMessage, error) {
	path := "relations"
	if collection != "" {
		path = "relations/" + collection
	}

	raw, err := c.Get(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("directus: get relations: %w", err)
	}

	return raw, nil
}

// M2O relates many items in collection to one item in related, via a foreign key
// on the "many" side: M2O("products", "category_id", "categories").
func M2O(collection, field, related string) RelationInput {
	return RelationInput{
		Collection: collection,
		Field:      field,
		Related:    related,
		Schema: &RelationSchema{
			OnDelete: "SET NULL",
		},
	}
}

// O2M relates one item in collection to many in related. aliasField is a virtual
// field on the "one" side; foreignKey must already exist on the "many" side.
func O2M(collection, aliasField, related, foreignKey string) RelationInput {
	return RelationInput{
		Collection: related,
		Field:      foreignKey,
		Related:    collection,
		Meta: &RelationMeta{
			OneField: &aliasField,
		},
		Schema: &RelationSchema{
			OnDelete: "SET NULL",
		},
	}
}

// M2MInput configures a Many-to-Many relationship.
type M2MInput struct {
	// Collection is the source collection.
	Collection string
	// Related is the target collection.
	Related string
	// JunctionCollection is the name of the junction/pivot table.
	// If it doesn't exist, you must create it first.
	JunctionCollection string
	// JunctionSourceField is the FK on the junction pointing to Collection.
	JunctionSourceField string
	// JunctionTargetField is the FK on the junction pointing to Related.
	JunctionTargetField string
	// AliasField is the virtual field name on Collection for accessing related items.
	AliasField string
}

// M2M builds the relation input for each side of a Many-to-Many junction. The
// junction collection and its FK fields must already exist — see docs/directus-package.md.
func M2M(input M2MInput) (source RelationInput, target RelationInput) {
	source = RelationInput{
		Collection: input.JunctionCollection,
		Field:      input.JunctionSourceField,
		Related:    input.Collection,
		Meta: &RelationMeta{
			OneField:      &input.AliasField,
			JunctionField: &input.JunctionTargetField,
		},
		Schema: &RelationSchema{
			OnDelete: "SET NULL",
		},
	}

	target = RelationInput{
		Collection: input.JunctionCollection,
		Field:      input.JunctionTargetField,
		Related:    input.Related,
		Schema: &RelationSchema{
			OnDelete: "SET NULL",
		},
	}

	return source, target
}

// Translations builds the relation inputs for the Directus translations interface:
// an M2M whose junction holds the per-language content. See docs/directus-package.md.
func Translations(collection, junctionCollection, sourceField, langField, langCollection string) (source RelationInput, lang RelationInput) {
	aliasField := "translations"
	source = RelationInput{
		Collection: junctionCollection,
		Field:      sourceField,
		Related:    collection,
		Meta: &RelationMeta{
			OneField:      &aliasField,
			JunctionField: &langField,
		},
		Schema: &RelationSchema{
			OnDelete: "SET NULL",
		},
	}

	lang = RelationInput{
		Collection: junctionCollection,
		Field:      langField,
		Related:    langCollection,
		Schema: &RelationSchema{
			OnDelete: "SET NULL",
		},
	}

	return source, lang
}

