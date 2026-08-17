package config_test

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swchck/director/config"
)

func TestView_InitialComputationFromSource(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a", Level: 10},
		{ID: 2, Category: "b", Level: 20},
		{ID: 3, Category: "a", Level: 30},
	})

	view := config.NewView("a-items", c,
		[]config.FilterOption[item]{
			config.Where(func(i item) bool { return i.Category == "a" }),
		},
	)

	if view.Count() != 2 {
		t.Errorf("Count() = %d, want 2", view.Count())
	}

	if view.Name() != "a-items" {
		t.Errorf("Name() = %q, want 'a-items'", view.Name())
	}
}

func TestView_RecomputesOnCollectionSwap(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a"},
		{ID: 2, Category: "b"},
	})

	view := config.NewView("a-only", c,
		[]config.FilterOption[item]{
			config.Where(func(i item) bool { return i.Category == "a" }),
		},
	)

	if view.Count() != 1 {
		t.Fatalf("initial: Count() = %d, want 1", view.Count())
	}

	// Swap collection with new data — view should auto-recompute.
	_ = c.Swap(v2(), []item{
		{ID: 1, Category: "a"},
		{ID: 3, Category: "a"},
		{ID: 4, Category: "b"},
	})

	if view.Count() != 2 {
		t.Errorf("after swap: Count() = %d, want 2", view.Count())
	}

	// Verify the correct items are in the view.
	all := view.All()
	if all[0].ID != 1 || all[1].ID != 3 {
		t.Errorf("after swap: All() = %+v, want IDs [1, 3]", all)
	}
}

func TestView_RecomputesWithSort(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Level: 30},
		{ID: 2, Level: 10},
		{ID: 3, Level: 20},
	})

	view := config.NewView("sorted", c,
		[]config.FilterOption[item]{
			config.SortBy(func(a, b item) int { return cmp.Compare(a.Level, b.Level) }),
		},
	)

	all := view.All()
	if all[0].ID != 2 || all[1].ID != 3 || all[2].ID != 1 {
		t.Errorf("sorted view = %+v, want IDs [2, 3, 1]", all)
	}

	// Swap with new data — sort should be reapplied.
	_ = c.Swap(v2(), []item{
		{ID: 4, Level: 5},
		{ID: 1, Level: 30},
	})

	all = view.All()
	if len(all) != 2 || all[0].ID != 4 || all[1].ID != 1 {
		t.Errorf("after swap sorted view = %+v, want IDs [4, 1]", all)
	}
}

func TestView_RecomputesWithFilterSortLimit(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "food", Level: 30},
		{ID: 2, Category: "food", Level: 10},
		{ID: 3, Category: "drink", Level: 20},
		{ID: 4, Category: "food", Level: 20},
	})

	view := config.NewView("top-food", c,
		[]config.FilterOption[item]{
			config.Where(func(i item) bool { return i.Category == "food" }),
			config.SortBy(func(a, b item) int { return cmp.Compare(a.Level, b.Level) }),
			config.Limit[item](2),
		},
	)

	all := view.All()
	if len(all) != 2 {
		t.Fatalf("Count() = %d, want 2", len(all))
	}

	if all[0].ID != 2 || all[1].ID != 4 {
		t.Errorf("view = %+v, want IDs [2, 4] (lowest levels)", all)
	}
}

func TestView_Find(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a"},
		{ID: 2, Category: "a"},
	})

	view := config.NewView("all", c, nil)

	found, ok := view.Find(func(i item) bool { return i.ID == 2 })
	if !ok || found.ID != 2 {
		t.Errorf("Find = %+v, ok=%v", found, ok)
	}

	_, ok = view.Find(func(i item) bool { return i.ID == 99 })
	if ok {
		t.Error("Find should return false for non-existent item")
	}
}

func TestView_OnChange(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1}})

	view := config.NewView("all", c, nil)

	var hookCalled bool
	var newCount int
	view.OnChange(func(old, new []item) {
		hookCalled = true
		newCount = len(new)
	})

	_ = c.Swap(v2(), []item{{ID: 1}, {ID: 2}, {ID: 3}})

	if !hookCalled {
		t.Error("View OnChange hook was not called")
	}

	if newCount != 3 {
		t.Errorf("hook got newCount=%d, want 3", newCount)
	}
}

func TestView_VersionTracksSource(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1}})

	view := config.NewView("all", c, nil)

	// Swap to v2.
	_ = c.Swap(v2(), []item{{ID: 2}})

	// View should see v2's data.
	all := view.All()
	if len(all) != 1 || all[0].ID != 2 {
		t.Errorf("view after v2 swap = %+v, want [{ID:2}]", all)
	}
}

func TestView_ConcurrentAccess(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1}})

	view := config.NewView("all", c, nil)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Concurrent readers on the view.
	for range 10 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = view.All()
					_ = view.Count()
					_, _ = view.First()
				}
			}
		})
	}

	// Writer swaps collection — triggers view recompute.
	for i := range 100 {
		ver := config.NewVersion(time.Date(2025, 1, 1, 0, 0, i, 0, time.UTC))
		_ = c.Swap(ver, []item{{ID: i}})
	}

	close(stop)
	wg.Wait()
}

// SingletonView tests

func TestSingletonView_TransformsValue(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")
	_ = s.Swap(v1(), appConfig{MaxItems: 100, Debug: true})

	view := config.NewSingletonView("max-items", s,
		func(st appConfig) int { return st.MaxItems },
	)

	got, ok := view.Get()
	if !ok {
		t.Fatal("Get() returned false")
	}

	if got != 100 {
		t.Errorf("Get() = %d, want 100", got)
	}

	if view.Name() != "max-items" {
		t.Errorf("Name() = %q, want 'max-items'", view.Name())
	}
}

func TestSingletonView_EmptySource(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")

	view := config.NewSingletonView("max-items", s,
		func(st appConfig) int { return st.MaxItems },
	)

	_, ok := view.Get()
	if ok {
		t.Error("Get() should return false when source is empty")
	}
}

func TestSingletonView_RecomputesOnSwap(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")
	_ = s.Swap(v1(), appConfig{MaxItems: 50})

	view := config.NewSingletonView("max-items", s,
		func(st appConfig) int { return st.MaxItems },
	)

	got, _ := view.Get()
	if got != 50 {
		t.Fatalf("initial: Get() = %d, want 50", got)
	}

	_ = s.Swap(v2(), appConfig{MaxItems: 200})

	got, ok := view.Get()
	if !ok || got != 200 {
		t.Errorf("after swap: Get() = %d, ok=%v, want 200", got, ok)
	}
}

func TestSingletonView_WithPersistence(t *testing.T) {
	store := &mockPersistence{data: make(map[string][]byte)}

	s := config.NewSingleton[appConfig]("app_config")
	_ = s.Swap(v1(), appConfig{MaxItems: 42})

	_ = config.NewSingletonView("sv-persist", s,
		func(st appConfig) int { return st.MaxItems },
		config.WithSingletonViewPersistence[appConfig, int](store),
	)

	// Allow async persistence goroutine to complete.
	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	_, ok := store.data["sv-persist"]
	store.mu.Unlock()

	if !ok {
		t.Error("expected persistence Save to be called")
	}
}

// CompositeView tests

func TestCompositeView_MergesViews(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a", Level: 10},
		{ID: 2, Category: "b", Level: 20},
		{ID: 3, Category: "a", Level: 30},
	})

	viewA := config.NewView("cat-a", c, []config.FilterOption[item]{
		config.Where(func(i item) bool { return i.Category == "a" }),
	})

	viewB := config.NewView("cat-b", c, []config.FilterOption[item]{
		config.Where(func(i item) bool { return i.Category == "b" }),
	})

	combo := config.NewCompositeView("all-cats", nil, viewA, viewB)

	if combo.Name() != "all-cats" {
		t.Errorf("Name() = %q, want 'all-cats'", combo.Name())
	}

	all := combo.All()
	if len(all) != 3 {
		t.Errorf("All() = %d items, want 3", len(all))
	}

	if combo.Count() != 3 {
		t.Errorf("Count() = %d, want 3", combo.Count())
	}
}

func TestCompositeView_WithDedup(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a"},
		{ID: 2, Category: "b"},
	})

	// Both views include all items — without dedup we'd get 4.
	viewAll1 := config.NewView("all1", c, nil)
	viewAll2 := config.NewView("all2", c, nil)

	combo := config.NewCompositeView("deduped", func(a, b item) bool {
		return a.ID == b.ID
	}, viewAll1, viewAll2)

	if combo.Count() != 2 {
		t.Errorf("Count() = %d, want 2 (deduped)", combo.Count())
	}
}

func TestCompositeView_EmptyViews(t *testing.T) {
	c := config.NewCollection[item]("items")

	viewEmpty := config.NewView("empty", c, nil)

	combo := config.NewCompositeView("empty-combo", nil, viewEmpty)

	if combo.Count() != 0 {
		t.Errorf("Count() = %d, want 0", combo.Count())
	}

	if len(combo.All()) != 0 {
		t.Error("All() should return empty slice")
	}
}

func TestCompositeView_NoViews(t *testing.T) {
	combo := config.NewCompositeView[item]("no-views", nil)

	if combo.Count() != 0 {
		t.Errorf("Count() = %d, want 0", combo.Count())
	}
}

// Persistence tests

type mockPersistence struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *mockPersistence) Save(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data[key] = make([]byte, len(data))
	copy(m.data[key], data)

	return nil
}

func (m *mockPersistence) Load(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.data[key]
	if !ok {
		return nil, nil
	}

	return d, nil
}

func TestView_WithPersistence_SavesOnRecompute(t *testing.T) {
	store := &mockPersistence{data: make(map[string][]byte)}

	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1}, {ID: 2}})

	_ = config.NewView("persist-view", c, nil,
		config.WithPersistence[item](store),
	)

	// Allow async persistence goroutine to complete.
	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	saved, ok := store.data["persist-view"]
	store.mu.Unlock()

	if !ok || len(saved) == 0 {
		t.Error("expected persistence Save to be called with data")
	}
}

func TestView_WithPersistence_SavesOnSwap(t *testing.T) {
	store := &mockPersistence{data: make(map[string][]byte)}

	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1}})

	_ = config.NewView("persist-view", c, nil,
		config.WithPersistence[item](store),
	)

	time.Sleep(50 * time.Millisecond)

	// Swap triggers recompute which triggers save.
	_ = c.Swap(v2(), []item{{ID: 1}, {ID: 2}, {ID: 3}})

	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	saved := store.data["persist-view"]
	store.mu.Unlock()

	// The saved data should reflect 3 items.
	if len(saved) == 0 {
		t.Error("expected persistence to be updated after swap")
	}
}

// Error handler option tests

type failingPersistence struct{}

func (f *failingPersistence) Save(_ context.Context, _ string, _ []byte) error {
	return fmt.Errorf("save failed")
}

func (f *failingPersistence) Load(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("load failed")
}

func TestView_WithErrorHandler(t *testing.T) {
	var mu sync.Mutex
	var capturedName string
	var capturedErr error

	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1}})

	_ = config.NewView("err-view", c, nil,
		config.WithPersistence[item](&failingPersistence{}),
		config.WithErrorHandler[item](func(name string, err error) {
			mu.Lock()
			capturedName = name
			capturedErr = err
			mu.Unlock()
		}),
	)

	// Allow async persistence goroutine to fire.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if capturedName != "err-view" {
		t.Errorf("error handler name = %q, want 'err-view'", capturedName)
	}

	if capturedErr == nil {
		t.Error("expected error to be captured")
	}
}

// slowPersistence blocks Save until ctx is cancelled, so the test can verify
// that WithPersistenceTimeout actually cancels the Save context.
type slowPersistence struct {
	saveCalls atomic.Int32
	saveErr   chan error // receives the error that Save observed
}

func newSlowPersistence() *slowPersistence {
	return &slowPersistence{saveErr: make(chan error, 4)}
}

func (s *slowPersistence) Save(ctx context.Context, _ string, _ []byte) error {
	s.saveCalls.Add(1)
	<-ctx.Done()
	err := ctx.Err()
	select {
	case s.saveErr <- err:
	default:
	}
	return err
}

func (s *slowPersistence) Load(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}

// The timeout must cancel the Save context and the resulting error must reach the handler.
func TestView_WithPersistenceTimeout_FiresOnSlowSave(t *testing.T) {
	store := newSlowPersistence()

	var (
		errMu     sync.Mutex
		captured  error
		callCount int
	)

	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1}})

	_ = config.NewView("timeout-view", c, nil,
		config.WithPersistence[item](store),
		config.WithPersistenceTimeout[item](80*time.Millisecond),
		config.WithErrorHandler[item](func(_ string, err error) {
			errMu.Lock()
			defer errMu.Unlock()
			callCount++
			captured = err
		}),
	)

	// Wait for the slow Save to time out and surface the error.
	select {
	case observed := <-store.saveErr:
		if !errors.Is(observed, context.DeadlineExceeded) {
			t.Errorf("Save context error = %v, want context.DeadlineExceeded", observed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Save did not observe ctx cancellation within 2s")
	}

	// Allow the error to flow back to the handler.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		errMu.Lock()
		got := captured
		errMu.Unlock()
		if got != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	errMu.Lock()
	defer errMu.Unlock()
	if captured == nil {
		t.Fatal("error handler was not invoked after Save timeout")
	}
	if !errors.Is(captured, context.DeadlineExceeded) {
		t.Errorf("error handler got %v, want one wrapping context.DeadlineExceeded", captured)
	}
	if callCount == 0 {
		t.Error("expected at least one error handler invocation")
	}
}

func TestSingletonView_WithErrorHandler_OnPersistFailure(t *testing.T) {
	var (
		mu       sync.Mutex
		gotName  string
		gotErr   error
		gotCalls int
	)

	s := config.NewSingleton[appConfig]("app_config")
	_ = s.Swap(v1(), appConfig{MaxItems: 7})

	_ = config.NewSingletonView("sv-err", s,
		func(c appConfig) int { return c.MaxItems },
		config.WithSingletonViewPersistence[appConfig, int](&failingPersistence{}),
		config.WithSingletonViewErrorHandler[appConfig, int](func(name string, err error) {
			mu.Lock()
			defer mu.Unlock()
			gotCalls++
			gotName = name
			gotErr = err
		}),
	)

	// Async persistence runs in a goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := gotCalls > 0
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotCalls == 0 {
		t.Fatal("error handler was not invoked on persist failure")
	}
	if gotName != "sv-err" {
		t.Errorf("error handler name = %q, want 'sv-err'", gotName)
	}
	if gotErr == nil {
		t.Fatal("error handler got nil error")
	}
	if !strings.Contains(gotErr.Error(), "save failed") {
		t.Errorf("error handler error = %v, want to wrap 'save failed'", gotErr)
	}
}

func TestView_Close_StopsRecomputing(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1, Category: "a", Level: 10}})

	view := config.NewView("a-items", c, nil)

	if view.Count() != 1 {
		t.Fatalf("initial Count() = %d, want 1", view.Count())
	}

	view.Close()

	// After Close, source swaps should NOT update the view.
	_ = c.Swap(v2(), []item{
		{ID: 1, Category: "a", Level: 10},
		{ID: 2, Category: "b", Level: 20},
	})

	if view.Count() != 1 {
		t.Errorf("Count() after Close = %d, want 1 (should not recompute)", view.Count())
	}
}

func TestView_Close_Idempotent(t *testing.T) {
	c := config.NewCollection[item]("items")
	view := config.NewView("items", c, nil)

	// Should not panic on multiple Close calls.
	view.Close()
	view.Close()
}

func TestSingletonView_Close_StopsRecomputing(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")
	_ = s.Swap(v1(), appConfig{MaxItems: 10})

	sv := config.NewSingletonView("max-items", s,
		func(c appConfig) int { return c.MaxItems },
	)

	val, ok := sv.Get()
	if !ok || val != 10 {
		t.Fatalf("initial Get() = %d/%v, want 10/true", val, ok)
	}

	sv.Close()

	_ = s.Swap(v2(), appConfig{MaxItems: 20})

	val, _ = sv.Get()
	if val != 10 {
		t.Errorf("Get() after Close = %d, want 10 (should not recompute)", val)
	}
}

func TestView_Version_MatchesSource(t *testing.T) {
	c := config.NewCollection[item]("items")
	ver := v1()
	_ = c.Swap(ver, []item{{ID: 1}})

	view := config.NewView("all", c, nil)

	if view.Version() != ver {
		t.Errorf("Version() = %v, want %v", view.Version(), ver)
	}

	ver2 := v2()
	_ = c.Swap(ver2, []item{{ID: 1}, {ID: 2}})

	if view.Version() != ver2 {
		t.Errorf("Version() after swap = %v, want %v", view.Version(), ver2)
	}
}

func TestSingletonView_WithPersistenceTimeout(t *testing.T) {
	sp := newSlowPersistence()

	s := config.NewSingleton[appConfig]("app_config")
	_ = s.Swap(v1(), appConfig{MaxItems: 42})

	_ = config.NewSingletonView("sv-timeout", s,
		func(c appConfig) int { return c.MaxItems },
		config.WithSingletonViewPersistence[appConfig, int](sp),
		config.WithSingletonViewPersistenceTimeout[appConfig, int](10*time.Millisecond),
	)

	select {
	case err := <-sp.saveErr:
		if err == nil {
			t.Error("expected non-nil error from slow Save")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for persistence timeout to fire")
	}
}

func TestView_FindMany(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a", Level: 10},
		{ID: 2, Category: "b", Level: 20},
		{ID: 3, Category: "a", Level: 30},
	})

	view := config.NewView("all", c, nil)

	found := view.FindMany(func(i item) bool { return i.Category == "a" })
	if len(found) != 2 {
		t.Errorf("FindMany(category=a) = %d items, want 2", len(found))
	}

	none := view.FindMany(func(i item) bool { return i.Category == "z" })
	if len(none) != 0 {
		t.Errorf("FindMany(category=z) = %d items, want 0", len(none))
	}
}

func TestView_Filter(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{
		{ID: 1, Category: "a", Level: 10},
		{ID: 2, Category: "b", Level: 20},
		{ID: 3, Category: "a", Level: 30},
	})

	view := config.NewView("all", c, nil)

	filtered := view.Filter(
		config.Where(func(i item) bool { return i.Category == "a" }),
	)
	if len(filtered) != 2 {
		t.Errorf("Filter(category=a) = %d items, want 2", len(filtered))
	}

	// No args — should return safe copy of all items.
	all := view.Filter()
	if len(all) != 3 {
		t.Errorf("Filter() = %d items, want 3", len(all))
	}
}

func TestView_First_Empty(t *testing.T) {
	c := config.NewCollection[item]("items")
	view := config.NewView("empty", c, nil)

	_, ok := view.First()
	if ok {
		t.Error("First() should return false on empty view")
	}
}

func TestView_Close_ConcurrentSafe(t *testing.T) {
	c := config.NewCollection[item]("items")
	_ = c.Swap(v1(), []item{{ID: 1, Name: "a"}})
	view := config.NewView("v", c, nil)

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			view.Close()
		})
	}
	wg.Wait()

	// View should still be readable after close.
	if view.Count() != 1 {
		t.Errorf("Count() = %d, want 1", view.Count())
	}
}

func TestView_OnChange_HookCannotMutateSnapshot(t *testing.T) {
	c := config.NewCollection[item]("items")
	view := config.NewView("all", c, nil)

	view.OnChange(func(_, newItems []item) {
		if len(newItems) > 0 {
			newItems[0].Name = "CORRUPTED"
		}
	})

	_ = c.Swap(v1(), []item{{ID: 1, Name: "Original"}})

	all := view.All()
	if all[0].Name != "Original" {
		t.Errorf("hook mutated view snapshot: Name = %q, want 'Original'", all[0].Name)
	}
}

// Warm start tests

func storedItems(t *testing.T, items ...item) []byte {
	t.Helper()

	data, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal stored items: %v", err)
	}

	return data
}

func TestView_WithPersistence_WarmStart(t *testing.T) {
	tests := []struct {
		name       string
		stored     []byte
		sourceVer  config.Version
		sourceItem []item
		wantIDs    []int
	}{
		{
			name:    "stored snapshot stands while the source is unloaded",
			stored:  storedItems(t, item{ID: 7}, item{ID: 8}),
			wantIDs: []int{7, 8},
		},
		{
			name:       "a loaded source wins over a stale snapshot",
			stored:     storedItems(t, item{ID: 7}),
			sourceVer:  v1(),
			sourceItem: []item{{ID: 1}, {ID: 2}},
			wantIDs:    []int{1, 2},
		},
		{
			name:      "a source that synced empty wins over a snapshot",
			stored:    storedItems(t, item{ID: 7}),
			sourceVer: v1(),
			wantIDs:   nil,
		},
		{
			name:    "no stored entry leaves the view computed from the source",
			wantIDs: nil,
		},
		{
			name:    "a stored null is not a warm start",
			stored:  []byte("null"),
			wantIDs: nil,
		},
		{
			name:    "a stored empty snapshot is a warm start",
			stored:  []byte("[]"),
			wantIDs: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockPersistence{data: make(map[string][]byte)}
			if tc.stored != nil {
				store.data["warm"] = tc.stored
			}

			c := config.NewCollection[item]("items")
			if !tc.sourceVer.IsZero() {
				_ = c.Swap(tc.sourceVer, tc.sourceItem)
			}

			v := config.NewView("warm", c, nil, config.WithPersistence[item](store))

			if got := idsOf(v.All()); !equalInts(got, tc.wantIDs) {
				t.Errorf("All() = %v, want %v", got, tc.wantIDs)
			}

			if v.Version() != tc.sourceVer {
				t.Errorf("Version() = %v, want %v", v.Version(), tc.sourceVer)
			}
		})
	}
}

func TestView_WithPersistence_WarmStartSupersededByFirstSwap(t *testing.T) {
	store := &mockPersistence{data: map[string][]byte{
		"warm": storedItems(t, item{ID: 7}),
	}}

	c := config.NewCollection[item]("items")
	v := config.NewView("warm", c, nil, config.WithPersistence[item](store))

	if got := idsOf(v.All()); !equalInts(got, []int{7}) {
		t.Fatalf("All() before first sync = %v, want [7]", got)
	}

	_ = c.Swap(v1(), []item{{ID: 1}})

	if got := idsOf(v.All()); !equalInts(got, []int{1}) {
		t.Errorf("All() after first sync = %v, want [1]", got)
	}

	if v.Version() != v1() {
		t.Errorf("Version() = %v, want %v", v.Version(), v1())
	}
}

// Not recomputing on a warm start is also what stops a replica that comes up before its
// first sync from overwriting the shared snapshot with an empty one.
func TestView_WithPersistence_ColdStartKeepsStoredSnapshot(t *testing.T) {
	stored := storedItems(t, item{ID: 7})
	store := &mockPersistence{data: map[string][]byte{"warm": stored}}

	c := config.NewCollection[item]("items")
	_ = config.NewView("warm", c, nil, config.WithPersistence[item](store))

	// Allow any async persistence goroutine to run.
	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	defer store.mu.Unlock()

	if string(store.data["warm"]) != string(stored) {
		t.Errorf("stored snapshot = %s, want it untouched (%s)", store.data["warm"], stored)
	}
}

func TestSingletonView_WithPersistence_WarmStart(t *testing.T) {
	tests := []struct {
		name      string
		stored    []byte
		sourceVer config.Version
		source    appConfig
		wantVal   int
		wantOK    bool
	}{
		{
			name:    "stored value stands while the source is unloaded",
			stored:  []byte("7"),
			wantVal: 7,
			wantOK:  true,
		},
		{
			name:      "a loaded source wins over a stale value",
			stored:    []byte("7"),
			sourceVer: v1(),
			source:    appConfig{MaxItems: 42},
			wantVal:   42,
			wantOK:    true,
		},
		{
			name:   "no stored entry leaves the view unloaded",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockPersistence{data: make(map[string][]byte)}
			if tc.stored != nil {
				store.data["warm"] = tc.stored
			}

			s := config.NewSingleton[appConfig]("app_config")
			if !tc.sourceVer.IsZero() {
				_ = s.Swap(tc.sourceVer, tc.source)
			}

			sv := config.NewSingletonView("warm", s,
				func(c appConfig) int { return c.MaxItems },
				config.WithSingletonViewPersistence[appConfig, int](store),
			)

			val, ok := sv.Get()
			if ok != tc.wantOK || val != tc.wantVal {
				t.Errorf("Get() = %d, %v, want %d, %v", val, ok, tc.wantVal, tc.wantOK)
			}
		})
	}
}

func TestSingletonView_WithPersistence_WarmStartSupersededByFirstSwap(t *testing.T) {
	store := &mockPersistence{data: map[string][]byte{"warm": []byte("7")}}

	s := config.NewSingleton[appConfig]("app_config")
	sv := config.NewSingletonView("warm", s,
		func(c appConfig) int { return c.MaxItems },
		config.WithSingletonViewPersistence[appConfig, int](store),
	)

	if val, ok := sv.Get(); !ok || val != 7 {
		t.Fatalf("Get() before first sync = %d, %v, want 7, true", val, ok)
	}

	_ = s.Swap(v1(), appConfig{MaxItems: 42})

	if val, ok := sv.Get(); !ok || val != 42 {
		t.Errorf("Get() after first sync = %d, %v, want 42, true", val, ok)
	}
}

func idsOf(items []item) []int {
	ids := make([]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}

	return ids
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
