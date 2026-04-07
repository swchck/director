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
	// Group is the name of the parent collection folder.
	// Set this to place a collection inside a folder.
	Group string `json:"group,omitempty"`
	// Collapse controls the default display state when this is a folder.
	// One of: "open", "closed", "locked".
	Collapse CollapseMode `json:"collapse,omitempty"`
}

// CreateCollectionInput is the request body for creating a Directus collection.
type CreateCollectionInput struct {
	Collection string          `json:"collection"`
	Meta       *CollectionMeta `json:"meta,omitempty"`
	// Schema must be present for Directus to create the database table.
	// For collection folders, Schema is nil which serializes to "schema": null.
	Schema *SchemaOptions `json:"schema"`
	Fields []FieldInput   `json:"fields,omitempty"`

	// isFolder is set internally by CreateCollectionFolder to prevent
	// auto-filling Schema with an empty object.
	isFolder bool `json:"-"`
}

// SchemaOptions configures the database schema for a collection.
type SchemaOptions struct {
	Name    string `json:"name,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// CreateCollection creates a new Directus collection.
//
// Example:
//
//	client.CreateCollection(ctx, directus.CreateCollectionInput{
//	    Collection: "products",
//	    Meta:       &directus.CollectionMeta{Icon: "shopping_cart"},
//	    Fields: []directus.FieldInput{
//	        directus.PrimaryKeyField("id"),
//	        {Field: "name", Type: FieldTypeString, Meta: &FieldMeta{Required: true}},
//	        {Field: "price", Type: FieldTypeFloat},
//	    },
//	})
func (c *Client) CreateCollection(ctx context.Context, input CreateCollectionInput) error {
	// Directus requires "schema" to be present (even empty) for table creation.
	// For collection folders, schema must be explicitly null (omitted via nil pointer).
	if input.Schema == nil && !input.isFolder {
		input.Schema = &SchemaOptions{}
	}

	// Directus 11 quirk: fields with special metadata (date-created, date-updated, etc.)
	// don't get their special behavior applied when created inline with the collection.
	// Split them out and create them separately after the collection.
	inlineFields, deferredFields := splitSpecialFields(input.Fields)
	input.Fields = inlineFields

	_, err := c.Post(ctx, "collections", input)
	if err != nil {
		return fmt.Errorf("directus: create collection %s: %w", input.Collection, err)
	}

	// Create deferred fields with special metadata separately.
	for _, field := range deferredFields {
		if err := c.CreateField(ctx, input.Collection, field); err != nil {
			return fmt.Errorf("directus: create collection %s field %s: %w", input.Collection, field.Field, err)
		}
	}

	return nil
}

// splitSpecialFields separates fields that need special metadata applied
// (must be created after the collection) from regular inline fields.
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

// CreateCollectionFolder creates a virtual collection folder for organizing
// other collections in the Directus sidebar.
//
// Folders have no database table — they exist only in the Directus metadata.
// Place collections inside a folder by setting CollectionMeta.Group on those collections.
//
// Example:
//
//	// Create a folder.
//	dc.CreateCollectionFolder(ctx, "content", &directus.CollectionMeta{
//	    Icon: "folder", Collapse: directus.CollapseOpen,
//	})
//
//	// Create a collection inside the folder.
//	dc.CreateCollection(ctx, directus.CreateCollectionInput{
//	    Collection: "articles",
//	    Meta:       &directus.CollectionMeta{Icon: "article", Group: "content"},
//	    Fields:     []directus.FieldInput{directus.PrimaryKeyField("id")},
//	})
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
		// Schema is intentionally nil — Directus interprets null schema as "no table".
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

// FieldMeta configures Directus-level field metadata.
//
// Interface and Display MUST be set for a field to appear properly in the
// Directus Data Studio. Without them, the field shows as "Database Only".
//
// Common interface values:
//   - "input" — text/number input
//   - "input-multiline" — textarea
//   - "boolean" — toggle switch
//   - "select-dropdown" — dropdown select
//   - "select-dropdown-m2o" — M2O relational dropdown with search and create
//   - "list-m2m" — M2M relational list
//   - "list-o2m" — O2M relational list
//   - "datetime" — date/time picker
//   - "input-code" — code editor (for JSON)
//   - "tags" — tag input
//
// Common display values:
//   - "formatted-value" — generic display
//   - "related-values" — show related item field(s) for M2O
//   - "labels" — colored labels
//   - "boolean" — true/false display
//   - "datetime" — formatted datetime
//   - "raw" — raw value
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

// M2OField returns a Many-to-One relational field with a dropdown selector.
// This creates the foreign key field that shows a rich dropdown in the Directus UI
// with search, preview, and ability to create new related items.
//
// Example — product belongs to a category:
//
//	directus.M2OField("category_id", "categories")
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

// M2O creates a Many-to-One relation: many items in `collection` point to one item in `related`.
//
// This creates the foreign key field on the "many" side.
//
// Example — each product belongs to one category:
//
//	directus.M2O("products", "category_id", "categories")
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

// O2M creates a One-to-Many relation: one item in `collection` has many items in `related`.
//
// aliasField is the virtual field name on the "one" side (no database column).
// foreignKey is the actual FK field on the "many" side that must already exist.
//
// Example — one category has many products:
//
//	directus.O2M("categories", "products", "products", "category_id")
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

// M2M creates the relation inputs needed for a Many-to-Many relationship.
//
// Returns two RelationInputs: one for each side of the junction.
// You must create the junction collection and its FK fields before calling CreateRelation.
//
// Example — products have many tags, tags have many products:
//
//	// 1. Create junction collection
//	client.CreateCollection(ctx, directus.CreateCollectionInput{
//	    Collection: "products_tags",
//	    Meta:       &directus.CollectionMeta{Hidden: true},
//	    Fields: []directus.FieldInput{
//	        directus.PrimaryKeyField("id"),
//	        {Field: "products_id", Type: directus.FieldTypeInteger},
//	        {Field: "tags_id", Type: directus.FieldTypeInteger},
//	    },
//	})
//
//	// 2. Create both sides of the M2M relation
//	source, target := directus.M2M(directus.M2MInput{
//	    Collection:          "products",
//	    Related:             "tags",
//	    JunctionCollection:  "products_tags",
//	    JunctionSourceField: "products_id",
//	    JunctionTargetField: "tags_id",
//	    AliasField:          "tags",
//	})
//	client.CreateRelation(ctx, source)
//	client.CreateRelation(ctx, target)
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

// Translations creates the relation inputs for the Directus translations interface.
//
// This is a specialized M2M using a junction collection that stores language-specific content.
// langCollection is the collection that stores available languages (e.g. "languages").
// You must create this collection before calling CreateRelation.
//
// Example — products with translations:
//
//	// 1. Create languages collection
//	client.CreateCollection(ctx, directus.CreateCollectionInput{
//	    Collection: "languages",
//	    Fields: []directus.FieldInput{
//	        {Field: "code", Type: directus.FieldTypeString, Schema: &directus.FieldSchema{IsPrimaryKey: true, IsNullable: new(bool)}},
//	        {Field: "name", Type: directus.FieldTypeString},
//	    },
//	})
//
//	// 2. Create translations junction collection
//	client.CreateCollection(ctx, directus.CreateCollectionInput{
//	    Collection: "products_translations",
//	    Meta:       &directus.CollectionMeta{Hidden: true},
//	    Fields: []directus.FieldInput{
//	        directus.PrimaryKeyField("id"),
//	        {Field: "products_id", Type: directus.FieldTypeInteger},
//	        {Field: "languages_code", Type: directus.FieldTypeString},
//	        {Field: "name", Type: directus.FieldTypeString},
//	        {Field: "description", Type: directus.FieldTypeText},
//	    },
//	})
//
//	// 3. Create the translations relations
//	source, lang := directus.Translations("products", "products_translations", "products_id", "languages_code", "languages")
//	client.CreateRelation(ctx, source)
//	client.CreateRelation(ctx, lang)
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

