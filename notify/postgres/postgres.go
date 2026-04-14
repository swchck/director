// Package postgres implements notify.Channel using PostgreSQL LISTEN/NOTIFY.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/notify"
)

const (
	defaultChannel             = "config_sync"
	defaultHealthCheckInterval = 30 * time.Second

	minBackoff = 1 * time.Second
	maxBackoff = 30 * time.Second
)

// Channel implements notify.Channel using PostgreSQL LISTEN/NOTIFY.
type Channel struct {
	pool                *pgxpool.Pool
	channel             string
	healthCheckInterval time.Duration
	logger              dlog.Logger

	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
}

// Option configures a Channel.
type Option func(*Channel)

// WithChannel sets the PostgreSQL notification channel name.
// Default is "config_sync".
func WithChannel(name string) Option {
	return func(c *Channel) {
		c.channel = name
	}
}

// WithLogger sets the logger.
func WithLogger(logger dlog.Logger) Option {
	return func(c *Channel) {
		c.logger = logger
	}
}

// WithHealthCheckInterval sets how often the LISTEN connection is checked
// for liveness when no notifications arrive. A dead connection is detected
// via a ping after the interval elapses and automatically reconnected.
// Default is 30s.
func WithHealthCheckInterval(d time.Duration) Option {
	return func(c *Channel) {
		c.healthCheckInterval = d
	}
}

// NewChannel creates a new PostgreSQL notification channel.
func NewChannel(pool *pgxpool.Pool, opts ...Option) *Channel {
	c := &Channel{
		pool:                pool,
		channel:             defaultChannel,
		healthCheckInterval: defaultHealthCheckInterval,
		logger:              dlog.Nop(),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Publish sends an event via NOTIFY.
func (c *Channel) Publish(ctx context.Context, event notify.Event) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return notify.ErrClosed
	}
	c.mu.Unlock()

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("notify/postgres: marshal event: %w", err)
	}

	if _, err := c.pool.Exec(ctx, "SELECT pg_notify($1, $2)", c.channel, string(payload)); err != nil {
		return fmt.Errorf("notify/postgres: pg_notify: %w", err)
	}

	return nil
}

// Subscribe starts listening for notifications and returns a channel of events.
// The returned channel is closed when ctx is cancelled or Close is called.
//
// The listener automatically reconnects with exponential backoff if the
// underlying PostgreSQL connection dies (e.g. half-open TCP).
func (c *Channel) Subscribe(ctx context.Context) (<-chan notify.Event, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, notify.ErrClosed
	}
	c.mu.Unlock()

	listenCtx, cancel := context.WithCancel(ctx)

	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	ch := make(chan notify.Event, 16)

	go c.listenLoop(listenCtx, ch)

	return ch, nil
}

// listenLoop maintains a LISTEN connection, reconnecting on failure with
// exponential backoff. It closes ch on exit.
func (c *Channel) listenLoop(ctx context.Context, ch chan<- notify.Event) {
	defer close(ch)

	backoff := time.Duration(0)
	for {
		err := c.listen(ctx, ch)
		if ctx.Err() != nil {
			return
		}

		backoff = nextBackoff(backoff)
		c.logger.Warn("notify/postgres: listener connection lost, reconnecting",
			dlog.Err(err),
			dlog.String("backoff", backoff.String()),
		)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
	}
}

// listen acquires a connection, issues LISTEN, and reads notifications until
// an error occurs or the context is cancelled. It periodically pings the
// connection to detect half-open TCP failures.
func (c *Channel) listen(ctx context.Context, ch chan<- notify.Event) error {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("notify/postgres: acquire conn for listen: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN "+c.channel); err != nil {
		return fmt.Errorf("notify/postgres: listen %s: %w", c.channel, err)
	}

	c.logger.Debug("notify/postgres: listening", dlog.String("channel", c.channel))

	for {
		waitCtx, cancel := context.WithTimeout(ctx, c.healthCheckInterval)
		notification, err := conn.Conn().WaitForNotification(waitCtx)
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			if errors.Is(err, context.DeadlineExceeded) {
				if pingErr := conn.Conn().Ping(ctx); pingErr != nil {
					return fmt.Errorf("notify/postgres: health check failed: %w", pingErr)
				}
				continue
			}

			return fmt.Errorf("notify/postgres: wait for notification: %w", err)
		}

		var event notify.Event
		if err := json.Unmarshal([]byte(notification.Payload), &event); err != nil {
			c.logger.Error("notify/postgres: unmarshal notification",
				dlog.Err(err),
				dlog.String("payload", notification.Payload),
			)
			continue
		}

		select {
		case ch <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close stops listening and releases resources.
func (c *Channel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	if c.cancel != nil {
		c.cancel()
	}

	return nil
}

// nextBackoff returns the next backoff duration, doubling each time up to
// maxBackoff.
func nextBackoff(current time.Duration) time.Duration {
	if current < minBackoff {
		return minBackoff
	}

	next := current * 2
	if next > maxBackoff {
		return maxBackoff
	}

	return next
}
