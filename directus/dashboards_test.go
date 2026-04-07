package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestListDashboards(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dashboards" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Dashboard{
			{ID: "d1", Name: "Overview"},
			{ID: "d2", Name: "Sales"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	dashboards, err := client.ListDashboards(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(dashboards) != 2 {
		t.Fatalf("got %d dashboards, want 2", len(dashboards))
	}

	if dashboards[0].Name != "Overview" {
		t.Errorf("dashboards[0].Name = %q", dashboards[0].Name)
	}
}

func TestGetDashboard(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dashboards/d1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Dashboard{ID: "d1", Name: "Overview"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	d, err := client.GetDashboard(context.Background(), "d1")
	if err != nil {
		t.Fatal(err)
	}

	if d.ID != "d1" || d.Name != "Overview" {
		t.Errorf("dashboard = %+v", d)
	}
}

func TestCreateDashboard(t *testing.T) {
	var gotBody directus.Dashboard

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}

		json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSONData(w, directus.Dashboard{ID: "d-new", Name: gotBody.Name})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	d, err := client.CreateDashboard(context.Background(), directus.Dashboard{Name: "New Dashboard", Icon: "chart"})
	if err != nil {
		t.Fatal(err)
	}

	if d.ID != "d-new" {
		t.Errorf("ID = %q", d.ID)
	}
}

func TestUpdateDashboard(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/dashboards/d1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Dashboard{ID: "d1", Name: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	d, err := client.UpdateDashboard(context.Background(), "d1", directus.Dashboard{Name: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if d.Name != "Updated" {
		t.Errorf("Name = %q", d.Name)
	}
}

func TestDeleteDashboard(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/dashboards/d1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeleteDashboard(context.Background(), "d1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestListPanels(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panels" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, []directus.Panel{
			{ID: "p1", Name: "Revenue", Dashboard: "d1"},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	panels, err := client.ListPanels(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(panels) != 1 || panels[0].Name != "Revenue" {
		t.Errorf("panels = %+v", panels)
	}
}

func TestGetPanel(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panels/p1" {
			t.Errorf("path = %s", r.URL.Path)
		}

		writeJSONData(w, directus.Panel{ID: "p1", Name: "Revenue"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	p, err := client.GetPanel(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}

	if p.ID != "p1" {
		t.Errorf("panel = %+v", p)
	}
}

func TestCreatePanel(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/panels" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Panel{ID: "p-new", Name: "New Panel", Dashboard: "d1"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	p, err := client.CreatePanel(context.Background(), directus.Panel{Name: "New Panel", Dashboard: "d1"})
	if err != nil {
		t.Fatal(err)
	}

	if p.ID != "p-new" {
		t.Errorf("ID = %q", p.ID)
	}
}

func TestUpdatePanel(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/panels/p1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		writeJSONData(w, directus.Panel{ID: "p1", Name: "Updated"})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	p, err := client.UpdatePanel(context.Background(), "p1", directus.Panel{Name: "Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if p.Name != "Updated" {
		t.Errorf("Name = %q", p.Name)
	}
}

func TestDeletePanel(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/panels/p1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.DeletePanel(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
}
