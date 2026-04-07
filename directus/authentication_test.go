package directus_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/swchck/director/directus"
)

func TestLogin(t *testing.T) {
	var gotBody map[string]string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth/login" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		writeJSONData(w, directus.AuthResponse{
			AccessToken:  "access-123",
			Expires:      900000,
			RefreshToken: "refresh-456",
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	resp, err := client.Login(context.Background(), "admin@test.com", "secret")
	if err != nil {
		t.Fatal(err)
	}

	if gotBody["email"] != "admin@test.com" {
		t.Errorf("email = %q", gotBody["email"])
	}

	if gotBody["password"] != "secret" {
		t.Errorf("password = %q", gotBody["password"])
	}

	if resp.AccessToken != "access-123" {
		t.Errorf("access_token = %q", resp.AccessToken)
	}

	if resp.RefreshToken != "refresh-456" {
		t.Errorf("refresh_token = %q", resp.RefreshToken)
	}

	if resp.Expires != 900000 {
		t.Errorf("expires = %d", resp.Expires)
	}
}

func TestLogin_Error(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "Invalid credentials"}},
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	_, err := client.Login(context.Background(), "bad@test.com", "wrong")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRefreshToken(t *testing.T) {
	var gotBody map[string]string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/refresh" {
			t.Errorf("path = %s", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		writeJSONData(w, directus.AuthResponse{
			AccessToken:  "new-access",
			Expires:      900000,
			RefreshToken: "new-refresh",
		})
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	resp, err := client.RefreshToken(context.Background(), "old-refresh")
	if err != nil {
		t.Fatal(err)
	}

	if gotBody["refresh_token"] != "old-refresh" {
		t.Errorf("refresh_token = %q", gotBody["refresh_token"])
	}

	if gotBody["mode"] != "json" {
		t.Errorf("mode = %q", gotBody["mode"])
	}

	if resp.AccessToken != "new-access" {
		t.Errorf("access_token = %q", resp.AccessToken)
	}
}

func TestLogout(t *testing.T) {
	var gotBody map[string]string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/logout" {
			t.Errorf("path = %s", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.Logout(context.Background(), "refresh-token")
	if err != nil {
		t.Fatal(err)
	}

	if gotBody["refresh_token"] != "refresh-token" {
		t.Errorf("refresh_token = %q", gotBody["refresh_token"])
	}
}

func TestRequestPasswordReset(t *testing.T) {
	var gotBody map[string]string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/password/request" {
			t.Errorf("path = %s", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.RequestPasswordReset(context.Background(), "user@test.com")
	if err != nil {
		t.Fatal(err)
	}

	if gotBody["email"] != "user@test.com" {
		t.Errorf("email = %q", gotBody["email"])
	}
}

func TestResetPassword(t *testing.T) {
	var gotBody map[string]string

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/password/reset" {
			t.Errorf("path = %s", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token")
	err := client.ResetPassword(context.Background(), "reset-token", "newpass123")
	if err != nil {
		t.Fatal(err)
	}

	if gotBody["token"] != "reset-token" {
		t.Errorf("token = %q", gotBody["token"])
	}

	if gotBody["password"] != "newpass123" {
		t.Errorf("password = %q", gotBody["password"])
	}
}
