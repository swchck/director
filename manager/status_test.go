package manager_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
)

type testSiteSettings struct {
	SiteName string `json:"site_name"`
	Locale   string `json:"locale"`
}

func TestStatus_BeforeStart(t *testing.T) {
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	mgr := manager.New(store, notif, reg, manager.Options{ServiceName: "svc"},
		manager.WithInstanceID("inst-1"),
	)

	articles := config.NewCollection[testArticle]("articles")
	dc := directus.NewClient("http://example.invalid", "tok")
	manager.RegisterCollection(mgr, articles, directus.NewItems[testArticle](dc, "articles"))

	s := mgr.Status()
	if s.InstanceID != "inst-1" {
		t.Errorf("InstanceID = %q, want inst-1", s.InstanceID)
	}
	if s.ServiceName != "svc" {
		t.Errorf("ServiceName = %q, want svc", s.ServiceName)
	}
	if s.IsLeader {
		t.Error("IsLeader should be false before Start")
	}
	if len(s.Configs) != 1 {
		t.Fatalf("Configs len = %d, want 1", len(s.Configs))
	}
	c := s.Configs[0]
	if c.Name != "articles" {
		t.Errorf("Name = %q", c.Name)
	}
	if c.Kind != manager.ConfigKindCollection {
		t.Errorf("Kind = %q, want collection", c.Kind)
	}
	if c.Version != "" {
		t.Errorf("Version = %q, want empty before sync", c.Version)
	}
	if c.ItemCount != 0 {
		t.Errorf("ItemCount = %d, want 0", c.ItemCount)
	}
	if !c.LastSyncAt.IsZero() {
		t.Errorf("LastSyncAt should be zero before any sync")
	}
	if c.LastSyncErr != "" {
		t.Errorf("LastSyncErr = %q, want empty", c.LastSyncErr)
	}
}

func TestStatus_AfterSuccessfulSync(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("limit") == "1" && r.URL.Query().Get("sort") == "-date_updated" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"date_updated": now.Format(time.RFC3339)}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []testArticle{
				{ID: 1, Name: "Alpha", Category: "food"},
				{ID: 2, Name: "Beta", Category: "drink"},
			},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "tok")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	articles := config.NewCollection[testArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})
	manager.RegisterCollection(mgr, articles, directus.NewItems[testArticle](dc, "articles"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// Poll for sync completion instead of fixed sleep — sync timing varies
	// with HTTP overhead and race-detector slowdown.
	s := waitForStatus(t, mgr, "articles", 3*time.Second, func(c manager.ConfigStatus) bool {
		return !c.LastSyncAt.IsZero() && c.Version != ""
	})

	cancel()
	<-errCh

	if !s.IsLeader {
		t.Error("IsLeader = false, want true after successful syncAll")
	}
	if len(s.Configs) != 1 {
		t.Fatalf("Configs len = %d, want 1", len(s.Configs))
	}
	c := s.Configs[0]
	if c.Version == "" {
		t.Error("Version should be set after successful sync")
	}
	if c.ItemCount != 2 {
		t.Errorf("ItemCount = %d, want 2", c.ItemCount)
	}
	if c.LastSyncAt.IsZero() {
		t.Error("LastSyncAt should be non-zero after sync")
	}
	if c.LastSyncErr != "" {
		t.Errorf("LastSyncErr = %q, want empty after success", c.LastSyncErr)
	}
}

// waitForStatus polls mgr.Status until cond returns true for the named
// config, or timeout elapses.
func waitForStatus(t *testing.T, mgr *manager.Manager, name string, timeout time.Duration, cond func(manager.ConfigStatus) bool) manager.Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s := mgr.Status()
		for _, c := range s.Configs {
			if c.Name == name && cond(c) {
				return s
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitForStatus: condition never met for %q within %v", name, timeout)
	return manager.Status{}
}

func TestStatus_AfterFailedSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "tok")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	articles := config.NewCollection[testArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "svc",
	})
	manager.RegisterCollection(mgr, articles, directus.NewItems[testArticle](dc, "articles"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	s := waitForStatus(t, mgr, "articles", 3*time.Second, func(c manager.ConfigStatus) bool {
		return c.LastSyncErr != ""
	})

	cancel()
	<-errCh

	if len(s.Configs) != 1 {
		t.Fatalf("Configs len = %d", len(s.Configs))
	}
	c := s.Configs[0]
	if c.LastSyncAt.IsZero() {
		t.Error("LastSyncAt should be set even when sync failed")
	}
	if c.LastSyncErr == "" {
		t.Error("LastSyncErr should be set after failure")
	}
	if c.Version != "" {
		t.Errorf("Version should remain empty after failed sync; got %q", c.Version)
	}
	if c.ItemCount != 0 {
		t.Errorf("ItemCount = %d, want 0 after failed sync", c.ItemCount)
	}
}

func TestStatus_FlagsReflectOptions(t *testing.T) {
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	articles := config.NewCollection[testArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		ServiceName:           "svc",
		ManualSyncOnly:        true,
		RequireUnanimousApply: true,
	})
	dc := directus.NewClient("http://example.invalid", "tok")
	manager.RegisterCollection(mgr, articles, directus.NewItems[testArticle](dc, "articles"))

	s := mgr.Status()
	if !s.ManualSync {
		t.Error("ManualSync = false, want true")
	}
	if !s.Strict2PC {
		t.Error("Strict2PC = false, want true")
	}
}

func TestStatus_SingletonReportsKindAndZeroItemCount(t *testing.T) {
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	settings := config.NewSingleton[testSiteSettings]("site_settings")
	mgr := manager.New(store, notif, reg, manager.Options{ServiceName: "svc"})
	dc := directus.NewClient("http://example.invalid", "tok")
	manager.RegisterSingleton(mgr, settings, directus.NewSingleton[testSiteSettings](dc, "site_settings"))

	s := mgr.Status()
	if len(s.Configs) != 1 {
		t.Fatalf("Configs len = %d", len(s.Configs))
	}
	c := s.Configs[0]
	if c.Kind != manager.ConfigKindSingleton {
		t.Errorf("Kind = %q, want singleton", c.Kind)
	}
	if c.ItemCount != 0 {
		t.Errorf("ItemCount = %d, want 0 for singleton", c.ItemCount)
	}
}

func TestStatus_ConfigsSortedByName(t *testing.T) {
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	mgr := manager.New(store, notif, reg, manager.Options{ServiceName: "svc"})
	dc := directus.NewClient("http://example.invalid", "tok")

	bananas := config.NewCollection[testArticle]("bananas")
	apples := config.NewCollection[testArticle]("apples")
	cherries := config.NewCollection[testArticle]("cherries")

	manager.RegisterCollection(mgr, bananas, directus.NewItems[testArticle](dc, "bananas"))
	manager.RegisterCollection(mgr, apples, directus.NewItems[testArticle](dc, "apples"))
	manager.RegisterCollection(mgr, cherries, directus.NewItems[testArticle](dc, "cherries"))

	s := mgr.Status()
	if len(s.Configs) != 3 {
		t.Fatalf("Configs len = %d", len(s.Configs))
	}
	want := []string{"apples", "bananas", "cherries"}
	for i, name := range want {
		if s.Configs[i].Name != name {
			t.Errorf("Configs[%d].Name = %q, want %q", i, s.Configs[i].Name, name)
		}
	}
}
