//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
)

var (
	testDirectusURL = envOrDefault("DIRECTUS_URL", "http://localhost:8055")
	testDatabaseURL = envOrDefault("DATABASE_URL", "postgres://directus:directus@localhost:5433/directus?sslmode=disable")
	testRedisURL    = envOrDefault("REDIS_URL", "localhost:6379")

	testAdminEmail    = envOrDefault("DIRECTUS_ADMIN_EMAIL", "admin@example.com")
	testAdminPassword = envOrDefault("DIRECTUS_ADMIN_PASSWORD", "admin")
	testStaticToken   = envOrDefault("DIRECTUS_TOKEN", "e2e-test-token")
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

var adminSetupOnce sync.Once

// ensureAdminAccess sets admin_access=true on the Administrator policy, once per
// test process. The static token can reach /policies in Directus 11 even without
// admin_access, which is what makes bootstrapping through the API possible.
func ensureAdminAccess(t *testing.T) {
	t.Helper()

	adminSetupOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		staticClient := directus.NewClient(testDirectusURL, testStaticToken)

		if err := staticClient.GrantAdminAccess(ctx); err != nil {
			t.Logf("warning: GrantAdminAccess via API failed (%v), may already be set", err)
		}
	})
}

// getAdminJWT logs into Directus and returns a fresh JWT carrying admin_access.
func getAdminJWT(t *testing.T) string {
	t.Helper()

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, testAdminEmail, testAdminPassword)

	resp, err := http.Post(
		testDirectusURL+"/auth/login",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	if result.Data.AccessToken == "" {
		t.Fatal("empty access token from login")
	}

	return result.Data.AccessToken
}

func testLogger(t *testing.T) dlog.Logger {
	return dlog.NewSlog(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// testDirectusClient returns a client authenticated with a login token, not the
// static one: a Directus 11 static token does not pick up policy changes made
// after it was issued, so it would never see the admin_access granted above.
func testDirectusClient(t *testing.T) *directus.Client {
	t.Helper()

	ensureAdminAccess(t)
	token := getAdminJWT(t)

	return directus.NewClient(testDirectusURL, token,
		directus.WithLogger(testLogger(t)),
	)
}

func testPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), testDatabaseURL)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	t.Cleanup(func() { pool.Close() })

	return pool
}

func testRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	rdb := redis.NewClient(&redis.Options{Addr: testRedisURL})
	t.Cleanup(func() { rdb.Close() })

	return rdb
}

// cleanupCollection deletes a collection, ignoring every error: teardown must not
// fail a test that already passed, and the collection may never have been created.
func cleanupCollection(t *testing.T, dc *directus.Client, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = dc.DeleteCollection(ctx, name)
}
