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

// adminSetupOnce ensures GrantAdminAccess runs exactly once per test process.
var adminSetupOnce sync.Once

// ensureAdminAccess uses the static token (which can access /policies in Directus 11)
// to set admin_access=true on the Administrator policy via the ACL API.
// This replaces the old init sidecar/DB hack approach.
func ensureAdminAccess(t *testing.T) {
	t.Helper()

	adminSetupOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// The static token can access the policies endpoint even without admin_access.
		staticClient := directus.NewClient(testDirectusURL, testStaticToken)

		if err := staticClient.GrantAdminAccess(ctx); err != nil {
			t.Logf("warning: GrantAdminAccess via API failed (%v), may already be set", err)
		}
	})
}

// getAdminJWT logs into Directus and returns a fresh JWT with admin_access.
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

func testDirectusClient(t *testing.T) *directus.Client {
	t.Helper()

	// Ensure admin policy has admin_access=true.
	ensureAdminAccess(t)

	// Get a fresh JWT that includes the updated admin_access claim.
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

// cleanupCollection deletes a collection and ignores "not found" errors.
func cleanupCollection(t *testing.T, dc *directus.Client, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = dc.DeleteCollection(ctx, name)
}
