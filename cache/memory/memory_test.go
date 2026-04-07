package memory_test

import (
	"context"
	"sync"
	"testing"

	"github.com/swchck/director/cache/memory"
)

func TestViewStore_SaveAndLoad(t *testing.T) {
	store := memory.NewViewStore()
	ctx := context.Background()

	// Load miss.
	data, err := store.Load(ctx, "missing")
	if err != nil || data != nil {
		t.Errorf("Load(missing) = %v, %v", data, err)
	}

	// Save and load.
	if err := store.Save(ctx, "key1", []byte(`{"foo":"bar"}`)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err = store.Load(ctx, "key1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if string(data) != `{"foo":"bar"}` {
		t.Errorf("Load = %q", data)
	}

	// Overwrite.
	if err := store.Save(ctx, "key1", []byte(`updated`)); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}

	data, _ = store.Load(ctx, "key1")
	if string(data) != "updated" {
		t.Errorf("Load after overwrite = %q", data)
	}
}

func TestViewStore_IsolatesData(t *testing.T) {
	store := memory.NewViewStore()
	ctx := context.Background()

	original := []byte("original")
	_ = store.Save(ctx, "k", original)

	// Mutate the original — should not affect stored data.
	original[0] = 'X'

	loaded, _ := store.Load(ctx, "k")
	if string(loaded) != "original" {
		t.Errorf("mutation leaked: %q", loaded)
	}
}

func TestViewStore_MultipleKeys(t *testing.T) {
	store := memory.NewViewStore()
	ctx := context.Background()

	store.Save(ctx, "view-a", []byte("data-a"))
	store.Save(ctx, "view-b", []byte("data-b"))

	a, _ := store.Load(ctx, "view-a")
	b, _ := store.Load(ctx, "view-b")

	if string(a) != "data-a" {
		t.Errorf("view-a = %q", a)
	}
	if string(b) != "data-b" {
		t.Errorf("view-b = %q", b)
	}
}

func TestViewStore_LoadDoesNotMutateInternalData(t *testing.T) {
	store := memory.NewViewStore()
	ctx := context.Background()

	store.Save(ctx, "key", []byte("immutable"))

	loaded, _ := store.Load(ctx, "key")
	loaded[0] = 'X' // mutate the returned slice

	reloaded, _ := store.Load(ctx, "key")
	if string(reloaded) != "immutable" {
		t.Errorf("internal data was mutated: %q", reloaded)
	}
}

func TestViewStore_ConcurrentAccess(t *testing.T) {
	store := memory.NewViewStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	const n = 100

	for range n {
		wg.Go(func() {
			store.Save(ctx, "key", []byte("data"))
		})
		wg.Go(func() {
			store.Load(ctx, "key")
		})
	}

	wg.Wait()
}

func TestViewStore_EmptyData(t *testing.T) {
	store := memory.NewViewStore()
	ctx := context.Background()

	store.Save(ctx, "empty", []byte{})

	data, err := store.Load(ctx, "empty")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if data == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(data) != 0 {
		t.Errorf("expected empty slice, got %d bytes", len(data))
	}
}
