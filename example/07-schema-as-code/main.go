// Package main demonstrates creating Directus collections with relational fields
// (M2O, O2M, M2M) and translations using the schema management API.
//
// This creates a full e-commerce schema:
//
//	categories          (collection)
//	products            (collection, M2O → categories, M2M → tags, translations)
//	tags                (collection)
//	products_tags       (junction for M2M)
//	products_translations (junction for translations)
//	game_settings       (singleton)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
)

func main() {
	sl := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger := dlog.NewSlog(sl)

	ctx := context.Background()

	directusURL := envOr("DIRECTUS_URL", "http://localhost:8055")
	directusToken := envOr("DIRECTUS_TOKEN", "e2e-test-token")

	dc := directus.NewClient(directusURL, directusToken,
		directus.WithLogger(logger),
	)

	// -----------------------------------------------------------------------
	// 1. Create a collection folder to group our e-commerce collections
	// -----------------------------------------------------------------------

	logger.Info("creating 'E-Commerce' collection folder")

	if err := dc.CreateCollectionFolder(ctx, "ecommerce", &directus.CollectionMeta{
		Icon:     "storefront",
		Note:     "E-commerce product catalog collections",
		Collapse: directus.CollapseOpen,
	}); err != nil {
		sl.Error("create folder", "error", err)
		os.Exit(1)
	}

	// -----------------------------------------------------------------------
	// 2. Create "categories" collection (inside the folder)
	// -----------------------------------------------------------------------

	logger.Info("creating categories collection")

	if err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "categories",
		Meta:       &directus.CollectionMeta{Icon: "category", Group: "ecommerce"},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateCreatedField(),
			directus.DateUpdatedField(),
			directus.StatusField(),
			directus.SortField(),
			withRequired(directus.StringField("name")),
			withUnique(directus.StringField("slug")),
		},
	}); err != nil {
		sl.Error("create categories", "error", err)
		os.Exit(1)
	}

	// -----------------------------------------------------------------------
	// 3. Create "tags" collection
	// -----------------------------------------------------------------------

	logger.Info("creating tags collection")

	if err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "tags",
		Meta:       &directus.CollectionMeta{Icon: "label", Group: "ecommerce"},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			withRequired(directus.StringField("name")),
			withDefault(directus.StringField("color"), "#000000"),
		},
	}); err != nil {
		sl.Error("create tags", "error", err)
		os.Exit(1)
	}

	// -----------------------------------------------------------------------
	// 4. Create "products" collection
	// -----------------------------------------------------------------------

	logger.Info("creating products collection")

	if err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "products",
		Meta:       &directus.CollectionMeta{Icon: "shopping_cart", Group: "ecommerce"},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateCreatedField(),
			directus.DateUpdatedField(),
			directus.StatusField(),
			directus.SortField(),
			withUnique(directus.StringField("sku")),
			directus.DecimalField("price"),
			withDefault(directus.BooleanField("active"), true),
			// M2O field — shows rich dropdown in Directus UI with search + create.
			directus.M2OField("category_id", "categories"),
		},
	}); err != nil {
		sl.Error("create products", "error", err)
		os.Exit(1)
	}

	// -----------------------------------------------------------------------
	// 5. M2O relation: products.category_id → categories
	//    "Each product belongs to one category"
	// -----------------------------------------------------------------------

	logger.Info("creating M2O: products.category_id → categories")

	if err := dc.CreateRelation(ctx, directus.M2O("products", "category_id", "categories")); err != nil {
		sl.Error("create M2O relation", "error", err)
		os.Exit(1)
	}

	// The reverse O2M alias is automatically available in Directus.
	// You can also explicitly define it:
	//
	//   directus.O2M("categories", "products", "products", "category_id")

	// -----------------------------------------------------------------------
	// 6. M2M junction: products ←→ tags
	//    "Products have many tags, tags have many products"
	// -----------------------------------------------------------------------

	logger.Info("creating M2M junction: products_tags")

	// 5a. Create the junction collection.
	if err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "products_tags",
		Meta:       &directus.CollectionMeta{Hidden: true, Icon: "import_export"},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.IntegerField("products_id"),
			directus.IntegerField("tags_id"),
		},
	}); err != nil {
		sl.Error("create junction collection", "error", err)
		os.Exit(1)
	}

	// 5b. Create both sides of the M2M relation.
	source, target := directus.M2M(directus.M2MInput{
		Collection:          "products",
		Related:             "tags",
		JunctionCollection:  "products_tags",
		JunctionSourceField: "products_id",
		JunctionTargetField: "tags_id",
		AliasField:          "tags",
	})

	logger.Info("creating M2M relations: products ←→ tags")

	if err := dc.CreateRelation(ctx, source); err != nil {
		sl.Error("create M2M source relation", "error", err)
		os.Exit(1)
	}

	if err := dc.CreateRelation(ctx, target); err != nil {
		sl.Error("create M2M target relation", "error", err)
		os.Exit(1)
	}

	// -----------------------------------------------------------------------
	// 7. Translations: products ←→ languages
	//    "Products have per-language name and description"
	// -----------------------------------------------------------------------

	// 6a. Create the languages collection (stores available languages).
	logger.Info("creating languages collection")

	if err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "languages",
		Fields: []directus.FieldInput{
			{Field: "code", Type: directus.FieldTypeString, Schema: &directus.FieldSchema{
				IsPrimaryKey: true,
				IsNullable:   new(bool),
			}},
			{Field: "name", Type: directus.FieldTypeString},
		},
	}); err != nil {
		sl.Error("create languages", "error", err)
		os.Exit(1)
	}

	// 6b. Create the translations junction collection.
	logger.Info("creating translations junction: products_translations")

	if err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "products_translations",
		Meta:       &directus.CollectionMeta{Hidden: true},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.IntegerField("products_id"),
			directus.StringField("languages_code"),
			withRequired(directus.StringField("name")),
			directus.TextField("description"),
		},
	}); err != nil {
		sl.Error("create translations junction", "error", err)
		os.Exit(1)
	}

	// 6c. Create the translations relations.
	trSource, trLang := directus.Translations(
		"products",
		"products_translations",
		"products_id",
		"languages_code",
		"languages",
	)

	logger.Info("creating translation relations")

	if err := dc.CreateRelation(ctx, trSource); err != nil {
		sl.Error("create translation source relation", "error", err)
		os.Exit(1)
	}

	if err := dc.CreateRelation(ctx, trLang); err != nil {
		sl.Error("create translation lang relation", "error", err)
		os.Exit(1)
	}

	// -----------------------------------------------------------------------
	// 8. Singleton: game_settings
	// -----------------------------------------------------------------------

	logger.Info("creating game_settings singleton")

	if err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "game_settings",
		Meta:       &directus.CollectionMeta{Singleton: true, Icon: "settings"},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateUpdatedField(),
			withDefault(directus.IntegerField("max_players"), 100),
			withDefault(directus.FloatField("tick_rate"), 60.0),
			withDefault(directus.BooleanField("maintenance_mode"), false),
		},
	}); err != nil {
		sl.Error("create game_settings", "error", err)
		os.Exit(1)
	}

	// -----------------------------------------------------------------------
	// 9. Automation flow: log when a product is created
	// -----------------------------------------------------------------------

	logger.Info("creating product creation flow")

	flow, err := dc.CreateFlow(ctx, directus.NewHookFlow("Log Product Create", directus.HookFlowOptions{
		Type:        "action",
		Scope:       []string{"items.create"},
		Collections: []string{"products"},
	}))
	if err != nil {
		sl.Error("create flow", "error", err)
		os.Exit(1)
	}

	logOp, err := dc.CreateOperation(ctx, directus.Operation{
		Name:      "Log Creation",
		Key:       "log_creation",
		Type:      directus.OpLog,
		Flow:      flow.ID,
		PositionX: 20,
		PositionY: 1,
		Options:   map[string]any{"message": "New product created: {{$trigger.payload.sku}}"},
	})
	if err != nil {
		sl.Error("create log operation", "error", err)
		os.Exit(1)
	}

	if _, err := dc.UpdateFlow(ctx, flow.ID, directus.Flow{
		Operation: &logOp.ID,
	}); err != nil {
		sl.Error("link flow to operation", "error", err)
		os.Exit(1)
	}

	// -----------------------------------------------------------------------
	// Done
	// -----------------------------------------------------------------------

	fmt.Println("Schema created successfully!")
	fmt.Println()
	fmt.Println("Collections:")
	fmt.Println("  categories           — with status, sort, timestamps")
	fmt.Println("  tags                 — simple name+color")
	fmt.Println("  products             — M2O→categories, M2M↔tags, translations")
	fmt.Println("  products_tags        — junction (hidden)")
	fmt.Println("  products_translations — translations junction (hidden)")
	fmt.Println("  game_settings        — singleton")
	fmt.Println()
	fmt.Println("Relations:")
	fmt.Println("  products.category_id → categories  (M2O)")
	fmt.Println("  products ↔ tags via products_tags   (M2M)")
	fmt.Println("  products ↔ languages via products_translations (translations)")
	fmt.Println()
	fmt.Println("Flows:")
	fmt.Println("  Log Product Create — hook flow, logs on product creation")
}

// Helper to set Required on a field.
func withRequired(f directus.FieldInput) directus.FieldInput {
	if f.Meta == nil {
		f.Meta = &directus.FieldMeta{}
	}

	f.Meta.Required = true

	return f
}

// Helper to set IsUnique on a field.
func withUnique(f directus.FieldInput) directus.FieldInput {
	if f.Schema == nil {
		f.Schema = &directus.FieldSchema{}
	}

	f.Schema.IsUnique = true

	return f
}

// Helper to set DefaultValue on a field.
func withDefault(f directus.FieldInput, val any) directus.FieldInput {
	if f.Schema == nil {
		f.Schema = &directus.FieldSchema{}
	}

	f.Schema.DefaultValue = val

	return f
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}
