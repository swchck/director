// Basic CRUD operations with the Directus client.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/swchck/director/directus"
)

type Task struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type TaskCreate struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

func main() {
	dc := directus.NewClient(
		envOr("DIRECTUS_URL", "http://localhost:8055"),
		envOr("DIRECTUS_TOKEN", "e2e-test-token"),
	)
	ctx := context.Background()

	// Ensure collection exists.
	dc.CreateCollection(ctx, directus.CreateCollectionInput{
		Collection: "tasks",
		Fields: []directus.FieldInput{
			directus.PrimaryKeyField("id"),
			directus.StringField("title"),
			directus.StringField("status"),
		},
	})

	items := directus.NewItems[Task](dc, "tasks")
	create := directus.NewItems[TaskCreate](dc, "tasks")

	// Create.
	_, _ = create.Create(ctx, &TaskCreate{Title: "Buy groceries", Status: "todo"})
	_, _ = create.Create(ctx, &TaskCreate{Title: "Write tests", Status: "done"})
	_, _ = create.Create(ctx, &TaskCreate{Title: "Deploy app", Status: "todo"})

	// List all.
	all, _ := items.List(ctx)
	fmt.Printf("All tasks: %d\n", len(all))

	// Filter.
	todo, _ := items.List(ctx,
		directus.WithFilter(directus.Field("status", "_eq", "todo")),
		directus.WithSort("title"),
	)
	fmt.Printf("Todo tasks: %d\n", len(todo))
	for _, t := range todo {
		fmt.Printf("  - %s\n", t.Title)
	}

	// Update.
	if len(todo) > 0 {
		_, _ = items.Update(ctx, fmt.Sprintf("%d", todo[0].ID), &Task{Status: "done"})
		fmt.Printf("Marked '%s' as done\n", todo[0].Title)
	}

	// Delete.
	if len(all) > 0 {
		_ = items.Delete(ctx, fmt.Sprintf("%d", all[0].ID))
		fmt.Println("Deleted first task")
	}

	remaining, _ := items.List(ctx)
	fmt.Printf("Remaining: %d\n", len(remaining))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
