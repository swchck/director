package manager_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/source"
)

// Mock source implementations

type mockCollectionSource[T any] struct {
	items        []T
	lastModified time.Time
	listErr      error
	versionErr   error
}

func (s *mockCollectionSource[T]) List(_ context.Context) ([]T, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.items, nil
}

func (s *mockCollectionSource[T]) LastModified(_ context.Context) (time.Time, error) {
	if s.versionErr != nil {
		return time.Time{}, s.versionErr
	}
	return s.lastModified, nil
}

type mockSingletonSource[T any] struct {
	value        *T
	lastModified time.Time
	getErr       error
	versionErr   error
}

func (s *mockSingletonSource[T]) Get(_ context.Context) (*T, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.value, nil
}

func (s *mockSingletonSource[T]) LastModified(_ context.Context) (time.Time, error) {
	if s.versionErr != nil {
		return time.Time{}, s.versionErr
	}
	return s.lastModified, nil
}

// Test types

type product struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

type siteSettings struct {
	SiteName string `json:"site_name"`
	Locale   string `json:"locale"`
}

func TestRegisterCollectionSource_SyncsData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	mockSrc := &mockCollectionSource[product]{
		items: []product{
			{ID: 1, Name: "Widget", Price: 100},
			{ID: 2, Name: "Gadget", Price: 200},
		},
		lastModified: now,
	}

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[product]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollectionSource(mgr, products, mockSrc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	time.Sleep(500 * time.Millisecond)

	if products.Count() != 2 {
		t.Errorf("Count() = %d, want 2", products.Count())
	}

	all := products.All()
	if all[0].Name != "Widget" {
		t.Errorf("first item Name = %q, want 'Widget'", all[0].Name)
	}

	if products.Version().IsZero() {
		t.Error("Version should not be zero after sync")
	}

	cancel()
	<-errCh
}

func TestRegisterSingletonSource_SyncsData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	val := siteSettings{SiteName: "My Site", Locale: "en"}
	mockSrc := &mockSingletonSource[siteSettings]{
		value:        &val,
		lastModified: now,
	}

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	settings := config.NewSingleton[siteSettings]("settings")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterSingletonSource(mgr, settings, mockSrc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	time.Sleep(500 * time.Millisecond)

	got, ok := settings.Get()
	if !ok {
		t.Fatal("settings.Get() returned false")
	}

	if got.SiteName != "My Site" {
		t.Errorf("SiteName = %q, want 'My Site'", got.SiteName)
	}

	if got.Locale != "en" {
		t.Errorf("Locale = %q, want 'en'", got.Locale)
	}

	if settings.Version().IsZero() {
		t.Error("Version should not be zero after sync")
	}

	cancel()
	<-errCh
}

func TestRegisterCollection_DirectusShorthand(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("limit") == "1" && r.URL.Query().Get("sort") == "-date_updated" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"date_updated": now.Format(time.RFC3339)},
				},
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"data": []product{
				{ID: 1, Name: "Alpha", Price: 50},
			},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "token")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[product]("products")
	items := directus.NewItems[product](dc, "products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollection(mgr, products, items)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go mgr.Start(ctx)
	time.Sleep(500 * time.Millisecond)

	if products.Count() != 1 {
		t.Errorf("Count() = %d, want 1", products.Count())
	}

	cancel()
}

func TestRegisterSingleton_DirectusShorthand(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// DateUpdated queries
		if r.URL.Query().Get("fields[]") == "date_updated" || r.URL.Query().Get("fields") == "date_updated" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"date_updated": now.Format(time.RFC3339),
				},
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"data": siteSettings{SiteName: "Test", Locale: "fr"},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "token")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	settings := config.NewSingleton[siteSettings]("settings")
	singleton := directus.NewSingleton[siteSettings](dc, "settings")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterSingleton(mgr, settings, singleton)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go mgr.Start(ctx)
	time.Sleep(500 * time.Millisecond)

	got, ok := settings.Get()
	if !ok {
		t.Fatal("settings.Get() returned false")
	}

	if got.SiteName != "Test" {
		t.Errorf("SiteName = %q, want 'Test'", got.SiteName)
	}

	cancel()
}

func TestManager_MultipleCollections(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	productsSrc := &mockCollectionSource[product]{
		items:        []product{{ID: 1, Name: "P1"}},
		lastModified: now,
	}

	settingsSrc := &mockSingletonSource[siteSettings]{
		value:        &siteSettings{SiteName: "Site", Locale: "en"},
		lastModified: now,
	}

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[product]("products")
	settings := config.NewSingleton[siteSettings]("settings")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollectionSource(mgr, products, productsSrc)
	manager.RegisterSingletonSource(mgr, settings, settingsSrc)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	time.Sleep(2 * time.Second)

	if products.Count() != 1 {
		t.Errorf("products Count() = %d, want 1", products.Count())
	}

	got, ok := settings.Get()
	if !ok {
		t.Fatal("settings.Get() returned false")
	}
	if got.SiteName != "Site" {
		t.Errorf("settings SiteName = %q", got.SiteName)
	}

	// Both should have published events.
	events := notif.publishedEvents()
	if len(events) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(events))
	}

	cancel()
	<-errCh
}

func TestManager_SourceListError_DoesNotCrash(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	mockSrc := &mockCollectionSource[product]{
		listErr:      errors.New("network error"),
		lastModified: now,
	}

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[product]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollectionSource(mgr, products, mockSrc)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Should still be empty since the source errored.
	if products.Count() != 0 {
		t.Errorf("Count() = %d, want 0 after source error", products.Count())
	}

	cancel()
	<-errCh
}

func TestManager_SourceVersionError_DoesNotCrash(t *testing.T) {
	mockSrc := &mockCollectionSource[product]{
		items:      []product{{ID: 1, Name: "Test"}},
		versionErr: errors.New("version check failed"),
	}

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[product]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollectionSource(mgr, products, mockSrc)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Should be empty since version check errored.
	if products.Count() != 0 {
		t.Errorf("Count() = %d, want 0 after version error", products.Count())
	}

	cancel()
	<-errCh
}

func TestMockCollectionSource_ImplementsInterface(t *testing.T) {
	var _ source.CollectionSource[product] = &mockCollectionSource[product]{}
}

func TestMockSingletonSource_ImplementsInterface(t *testing.T) {
	var _ source.SingletonSource[siteSettings] = &mockSingletonSource[siteSettings]{}
}

func TestWithCollectionDefaults_AppliesDefaults(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	mockSrc := &mockCollectionSource[product]{
		items: []product{
			{ID: 1, Name: "Widget", Price: 0},  // Price missing
			{ID: 2, Name: "Gadget", Price: 200}, // Price set
		},
		lastModified: now,
	}

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[product]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollectionSource(mgr, products, mockSrc,
		manager.WithCollectionDefaults(func(p product) product {
			if p.Price == 0 {
				p.Price = 99
			}
			return p
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	time.Sleep(500 * time.Millisecond)

	all := products.All()
	if len(all) != 2 {
		t.Fatalf("Count() = %d, want 2", len(all))
	}

	if all[0].Price != 99 {
		t.Errorf("item 0 Price = %d, want 99 (default)", all[0].Price)
	}
	if all[1].Price != 200 {
		t.Errorf("item 1 Price = %d, want 200 (original)", all[1].Price)
	}

	cancel()
	<-errCh
}

func TestWithSingletonDefaults_AppliesDefaults(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	val := siteSettings{SiteName: "My Site", Locale: ""} // Locale missing
	mockSrc := &mockSingletonSource[siteSettings]{
		value:        &val,
		lastModified: now,
	}

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	settings := config.NewSingleton[siteSettings]("settings")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterSingletonSource(mgr, settings, mockSrc,
		manager.WithSingletonDefaults(func(s siteSettings) siteSettings {
			if s.Locale == "" {
				s.Locale = "en-US"
			}
			return s
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	time.Sleep(500 * time.Millisecond)

	got, ok := settings.Get()
	if !ok {
		t.Fatal("settings.Get() returned false")
	}

	if got.SiteName != "My Site" {
		t.Errorf("SiteName = %q, want 'My Site'", got.SiteName)
	}
	if got.Locale != "en-US" {
		t.Errorf("Locale = %q, want 'en-US' (default)", got.Locale)
	}

	cancel()
	<-errCh
}

func TestWithCollectionDefaults_RunsBeforeValidator(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	mockSrc := &mockCollectionSource[product]{
		items:        []product{{ID: 1, Name: "Widget", Price: 0}},
		lastModified: now,
	}

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[product]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollectionSource(mgr, products, mockSrc,
		manager.WithCollectionDefaults(func(p product) product {
			if p.Price == 0 {
				p.Price = 50
			}
			return p
		}),
		manager.WithCollectionValidator(func(items []product) error {
			for _, p := range items {
				if p.Price == 0 {
					return errors.New("price must not be zero")
				}
			}
			return nil
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Defaults run before validator, so Price=0 becomes Price=50,
	// and the validator passes (no zero prices).
	all := products.All()
	if len(all) != 1 {
		t.Fatalf("Count() = %d, want 1", len(all))
	}
	if all[0].Price != 50 {
		t.Errorf("Price = %d, want 50", all[0].Price)
	}

	cancel()
	<-errCh
}
