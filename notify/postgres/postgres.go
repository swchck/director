// Package postgres implements notify.Channel using PostgreSQL LISTEN/NOTIFY.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/notify"
)

const defaultChannel = "config_sync"

// Channel implements notify.Channel using PostgreSQL LISTEN/NOTIFY.
type Channel struct {
	pool    *pgxpool.Pool
	channel string
	logger  dlog.Logger

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

// NewChannel creates a new PostgreSQL notification channel.
func NewChannel(pool *pgxpool.Pool, opts ...Option) *Channel {
	c := &Channel{
		pool:    pool,
		channel: defaultChannel,
		logger:  dlog.Nop(),
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
func (c *Channel) Subscribe(ctx context.Context) (<-chan notify.Event, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, notify.ErrClosed
	}
	c.mu.Unlock()

	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("notify/postgres: acquire conn for listen: %w", err)
	}

	if _, err := conn.Exec(ctx, "LISTEN "+c.channel); err != nil {
		conn.Release()
		return nil, fmt.Errorf("notify/postgres: listen %s: %w", c.channel, err)
	}

	listenCtx, cancel := context.WithCancel(ctx)

	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	ch := make(chan notify.Event, 16)

	go func() {
		defer conn.Release()
		defer close(ch)

		for {
			notification, err := conn.Conn().WaitForNotification(listenCtx)
			if err != nil {
				if listenCtx.Err() != nil {
					return
				}

				c.logger.Error("notify/postgres: wait for notification failed", dlog.Err(err))
				return
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
			case <-listenCtx.Done():
				return
			}
		}
	}()

	return ch, nil
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
