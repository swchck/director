package directus_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListExtensions(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/extensions" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Extension{
			{ID: "ext-1", Name: "my-extension", Enabled: true},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	exts, err := client.ListExtensions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(exts) != 1 || exts[0].Name != "my-extension" {
		t.Errorf("extensions = %+v", exts)
	}

	if !exts[0].Enabled {
		t.Error("expected Enabled=true")
	}
}

func TestUpdateExtension(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/extensions/my-ext" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Extension{ID: "ext-1", Name: "my-ext", Enabled: false})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	ext, err := client.UpdateExtension(context.Background(), "my-ext", directus.Extension{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}

	if ext.Enabled {
		t.Error("expected Enabled=false")
	}
}

func TestMetrics(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server/metrics" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, "# HELP directus_requests_total\ndirectus_requests_total 42")
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	metrics, err := client.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if metrics == "" {
		t.Error("expected non-empty metrics")
	}
}
