//go:build e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/swchck/director/directus"
)

func TestE2E_CreateCollection(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Cleanup(func() { cleanupCollection(t, dc, "e2e_basic") })

	err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_basic",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateUpdatedField(),
			{Field: "name", Type: directus.FieldTypeString},
			{Field: "active", Type: directus.FieldTypeBoolean, Schema: &directus.FieldSchema{DefaultValue: true}},
		},
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	// Verify we can write and read items.
	type BasicItem struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}

	type BasicCreate struct {
		Name string `json:"name"`
	}

	items := directus.NewItems[BasicItem](dc, "e2e_basic")
	createItems := directus.NewItems[BasicCreate](dc, "e2e_basic")

	_, err = createItems.Create(ctx, &BasicCreate{Name: "test-item"})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	listed, err := items.List(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}

	if len(listed) != 1 || listed[0].Name != "test-item" {
		t.Errorf("listed = %+v, want [{Name: test-item}]", listed)
	}
}

func TestE2E_CreateSingletonCollection(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Cleanup(func() { cleanupCollection(t, dc, "e2e_settings") })

	err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_settings",
		Meta:       &directus.CollectionMeta{Singleton: true},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateUpdatedField(),
			{Field: "max_players", Type: directus.FieldTypeInteger, Schema: &directus.FieldSchema{DefaultValue: 50}},
			{Field: "debug", Type: directus.FieldTypeBoolean, Schema: &directus.FieldSchema{DefaultValue: false}},
		},
	})
	if err != nil {
		t.Fatalf("create singleton: %v", err)
	}

	type Settings struct {
		ID         int  `json:"id"`
		MaxPlayers int  `json:"max_players"`
		Debug      bool `json:"debug"`
	}

	s := directus.NewSingleton[Settings](dc, "e2e_settings")

	got, err := s.Get(ctx)
	if err != nil {
		t.Fatalf("get singleton: %v", err)
	}

	if got.MaxPlayers != 50 {
		t.Errorf("MaxPlayers = %d, want 50", got.MaxPlayers)
	}

	updated, err := s.Update(ctx, &Settings{MaxPlayers: 200, Debug: true})
	if err != nil {
		t.Fatalf("update singleton: %v", err)
	}

	if updated.MaxPlayers != 200 || !updated.Debug {
		t.Errorf("updated = %+v, want MaxPlayers=200, Debug=true", updated)
	}
}

func TestE2E_M2ORelation(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Cleanup(func() {
		cleanupCollection(t, dc, "e2e_products")
		cleanupCollection(t, dc, "e2e_categories")
	})

	// Create categories.
	err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_categories",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			{Field: "name", Type: directus.FieldTypeString},
		},
	})
	if err != nil {
		t.Fatalf("create categories: %v", err)
	}

	// Create products with M2O FK field.
	err = dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_products",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateUpdatedField(),
			{Field: "name", Type: directus.FieldTypeString},
			{Field: "category_id", Type: directus.FieldTypeInteger},
		},
	})
	if err != nil {
		t.Fatalf("create products: %v", err)
	}

	// Create M2O relation.
	err = dc.CreateRelation(ctx, directus.M2O("e2e_products", "category_id", "e2e_categories"))
	if err != nil {
		t.Fatalf("create M2O relation: %v", err)
	}

	// Seed data.
	type Category struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	// Struct for fetching with expanded M2O.
	type ProductWithCategory struct {
		ID       int       `json:"id"`
		Name     string    `json:"name"`
		Category *Category `json:"category_id"`
	}

	catItems := directus.NewItems[Category](dc, "e2e_categories")
	prodItems := directus.NewItems[ProductWithCategory](dc, "e2e_products")

	cat, err := catItems.Create(ctx, &Category{Name: "Food"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	type ProductCreate struct {
		Name       string `json:"name"`
		CategoryID int    `json:"category_id"`
	}

	createItems := directus.NewItems[ProductCreate](dc, "e2e_products")
	_, err = createItems.Create(ctx, &ProductCreate{Name: "Pizza", CategoryID: cat.ID})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	// Fetch with M2O expanded.
	products, err := prodItems.List(ctx, directus.WithFields("*", "category_id.*"))
	if err != nil {
		t.Fatalf("list products: %v", err)
	}

	if len(products) != 1 {
		t.Fatalf("got %d products, want 1", len(products))
	}

	if products[0].Category == nil || products[0].Category.Name != "Food" {
		t.Errorf("M2O not populated: category = %+v", products[0].Category)
	}
}

func TestE2E_M2MRelation(t *testing.T) {
	dc := testDirectusClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Cleanup(func() {
		cleanupCollection(t, dc, "e2e_articles_tags")
		cleanupCollection(t, dc, "e2e_articles")
		cleanupCollection(t, dc, "e2e_tags")
	})

	// Create tags.
	err := dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_tags",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			{Field: "name", Type: directus.FieldTypeString},
		},
	})
	if err != nil {
		t.Fatalf("create tags: %v", err)
	}

	// Create articles.
	err = dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_articles",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.DateUpdatedField(),
			{Field: "title", Type: directus.FieldTypeString},
		},
	})
	if err != nil {
		t.Fatalf("create articles: %v", err)
	}

	// Create junction collection.
	err = dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "e2e_articles_tags",
		Meta:       &directus.CollectionMeta{Hidden: true},
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			{Field: "e2e_articles_id", Type: directus.FieldTypeInteger},
			{Field: "e2e_tags_id", Type: directus.FieldTypeInteger},
		},
	})
	if err != nil {
		t.Fatalf("create junction: %v", err)
	}

	// Create M2M relations.
	source, target := directus.M2M(directus.M2MInput{
		Collection:          "e2e_articles",
		Related:             "e2e_tags",
		JunctionCollection:  "e2e_articles_tags",
		JunctionSourceField: "e2e_articles_id",
		JunctionTargetField: "e2e_tags_id",
		AliasField:          "tags",
	})

	if err := dc.CreateRelation(ctx, source); err != nil {
		t.Fatalf("create M2M source: %v", err)
	}

	if err := dc.CreateRelation(ctx, target); err != nil {
		t.Fatalf("create M2M target: %v", err)
	}

	// Seed data.
	type Tag struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	type TagCreate struct {
		Name string `json:"name"`
	}

	tagItems := directus.NewItems[Tag](dc, "e2e_tags")
	tagCreateItems := directus.NewItems[TagCreate](dc, "e2e_tags")

	_, err = tagCreateItems.Create(ctx, &TagCreate{Name: "go"})
	if err != nil {
		t.Fatalf("create tag 1: %v", err)
	}

	_, err = tagCreateItems.Create(ctx, &TagCreate{Name: "directus"})
	if err != nil {
		t.Fatalf("create tag 2: %v", err)
	}

	// Fetch tags to get their IDs.
	tags, err := tagItems.List(ctx)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}

	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags))
	}

	tag1 := tags[0]
	tag2 := tags[1]

	// Create article with tags via M2M.
	type ArticleCreate struct {
		Title string           `json:"title"`
		Tags  []map[string]int `json:"tags"`
	}

	createArticles := directus.NewItems[ArticleCreate](dc, "e2e_articles")
	_, err = createArticles.Create(ctx, &ArticleCreate{
		Title: "Getting Started",
		Tags: []map[string]int{
			{"e2e_tags_id": tag1.ID},
			{"e2e_tags_id": tag2.ID},
		},
	})
	if err != nil {
		t.Fatalf("create article: %v", err)
	}

	// Verify the junction was populated by listing junction items.
	type JunctionItem struct {
		ID            int `json:"id"`
		E2eArticlesID int `json:"e2e_articles_id"`
		E2eTagsID     int `json:"e2e_tags_id"`
	}

	junctionItems := directus.NewItems[JunctionItem](dc, "e2e_articles_tags")
	junctions, err := junctionItems.List(ctx)
	if err != nil {
		t.Fatalf("list junction: %v", err)
	}

	if len(junctions) != 2 {
		t.Fatalf("junction has %d items, want 2", len(junctions))
	}

	// Verify both tags are linked.
	linkedTagIDs := make(map[int]bool)
	for _, j := range junctions {
		linkedTagIDs[j.E2eTagsID] = true
	}

	if !linkedTagIDs[tag1.ID] || !linkedTagIDs[tag2.ID] {
		t.Errorf("expected tags [%d, %d] linked, got %v", tag1.ID, tag2.ID, linkedTagIDs)
	}
}
