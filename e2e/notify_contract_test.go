//go:build e2e

package e2e_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/swchck/director/notify"
	"github.com/swchck/director/notify/notifytest"
	pgnotify "github.com/swchck/director/notify/postgres"
	redisnotify "github.com/swchck/director/notify/redis"
)

// TestNotifyContract_Postgres runs the notify.Channel conformance suite against
// PostgreSQL LISTEN/NOTIFY.
func TestNotifyContract_Postgres(t *testing.T) {
	pool := testPgPool(t)

	notifytest.RunContract(t, func(t *testing.T) (notify.Channel, notify.Channel) {
		name := uniqueChannelName()

		publisher := pgnotify.NewChannel(pool, pgnotify.WithChannel(name), pgnotify.WithLogger(testLogger(t)))
		peer := pgnotify.NewChannel(pool, pgnotify.WithChannel(name), pgnotify.WithLogger(testLogger(t)))

		t.Cleanup(func() {
			publisher.Close()
			peer.Close()
		})

		return publisher, peer
	})
}

// TestNotifyContract_Redis runs the notify.Channel conformance suite against
// Redis Pub/Sub.
func TestNotifyContract_Redis(t *testing.T) {
	rdb := testRedisClient(t)

	notifytest.RunContract(t, func(t *testing.T) (notify.Channel, notify.Channel) {
		name := uniqueChannelName()

		publisher := redisnotify.NewChannel(rdb, redisnotify.WithChannel(name), redisnotify.WithLogger(testLogger(t)))
		peer := redisnotify.NewChannel(rdb, redisnotify.WithChannel(name), redisnotify.WithLogger(testLogger(t)))

		t.Cleanup(func() {
			publisher.Close()
			peer.Close()
		})

		return publisher, peer
	})
}

// uniqueChannelName isolates one contract subtest from the next. It must be a
// valid PostgreSQL identifier.
func uniqueChannelName() string {
	return fmt.Sprintf("contract_%d", time.Now().UnixNano())
}
