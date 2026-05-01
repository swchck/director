package manager_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
)

// schemaTestArticle has a json tag that won't be in the Directus mock —
// drives the drift-detection assertion.
type schemaTestArticle struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Stale    string `json:"stale_field"` // not in Directus
	Category string `json:"category"`
}

// captureLogger is defined in validation_test.go and reused here. Its Warn
// captures msg + every key=value pair, so warnCount matches on field values
// as well as the message text.

func TestSchemaCheck_DisabledByDefault_NoFieldsRequest(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fields/") {
			hits++
		}
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "tok")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	articles := config.NewCollection[schemaTestArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "svc",
	})
	manager.RegisterCollection(mgr, articles, directus.NewItems[schemaTestArticle](dc, "articles"))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-errCh

	if hits != 0 {
		t.Errorf("schema check should not have hit /fields when disabled; got %d hits", hits)
	}
}

func TestSchemaCheck_LogsDriftWhenEnabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/fields/articles") {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"field": "id", "type": "integer"},
					{"field": "name", "type": "string"},
					{"field": "category", "type": "string"},
					// "stale_field" intentionally absent — should warn.
				},
			})
			return
		}
		// Fallback for any other Directus calls (sync, etc.) — return empty.
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "tok")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	logger := &captureLogger{}

	articles := config.NewCollection[schemaTestArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "svc",
	},
		manager.WithLogger(logger),
		manager.WithSchemaCheck(),
	)
	manager.RegisterCollection(mgr, articles, directus.NewItems[schemaTestArticle](dc, "articles"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-errCh

	// Must have a "schema drift detected" warning that mentions stale_field
	// in one of its key/value pairs.
	if got := logger.warnCount("schema drift detected"); got == 0 {
		t.Fatalf("expected schema drift warning; got warns: %v", logger.warns)
	}
	if got := logger.warnCount("stale_field"); got == 0 {
		t.Errorf("drift warning did not mention stale_field; got warns: %v", logger.warns)
	}
}

func TestSchemaCheck_NoDriftIsSilent(t *testing.T) {
	// All struct fields present in Directus → no warnings.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/fields/articles") {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"field": "id", "type": "integer"},
					{"field": "name", "type": "string"},
					{"field": "category", "type": "string"},
				},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "tok")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	logger := &captureLogger{}

	// testArticle from manager_test.go has fields id/name/category, all
	// present in the mock Directus response.
	articles := config.NewCollection[testArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "svc",
	},
		manager.WithLogger(logger),
		manager.WithSchemaCheck(),
	)
	manager.RegisterCollection(mgr, articles, directus.NewItems[testArticle](dc, "articles"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-errCh

	if got := logger.warnCount("schema drift detected"); got > 0 {
		t.Errorf("expected no drift warnings, got %d (warns: %v)", got, logger.warns)
	}
}

func TestSchemaCheck_FieldsFetchFailureDoesNotBlockStart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fields/") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "tok")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	logger := &captureLogger{}

	articles := config.NewCollection[schemaTestArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "svc",
	},
		manager.WithLogger(logger),
		manager.WithSchemaCheck(),
	)
	manager.RegisterCollection(mgr, articles, directus.NewItems[schemaTestArticle](dc, "articles"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// Manager must keep running; 200ms should be enough for any startup
	// failure to surface.
	time.Sleep(200 * time.Millisecond)
	cancel()
	if err := <-errCh; err != nil && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("Start returned unexpected error: %v", err)
	}

	if got := logger.warnCount("schema check skipped"); got == 0 {
		t.Errorf("expected 'schema check skipped' warning; got %v", logger.warns)
	}
}
