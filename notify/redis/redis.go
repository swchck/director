// Package redis implements notify.Channel using Redis Pub/Sub.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	goredis "github.com/redis/go-redis/v9"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/notify"
)

const defaultChannel = "config_sync"

// Channel implements notify.Channel using Redis Pub/Sub.
type Channel struct {
	client  goredis.UniversalClient
	channel string
	logger  dlog.Logger

	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
}

// Option configures a Channel.
type Option func(*Channel)

// WithChannel sets the Redis Pub/Sub channel name.
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

// NewChannel creates a new Redis notification channel.
func NewChannel(client goredis.UniversalClient, opts ...Option) *Channel {
	c := &Channel{
		client:  client,
		channel: defaultChannel,
		logger:  dlog.Nop(),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Publish sends an event to the Redis channel.
func (c *Channel) Publish(ctx context.Context, event notify.Event) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return notify.ErrClosed
	}
	c.mu.Unlock()

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("notify/redis: marshal event: %w", err)
	}

	if err := c.client.Publish(ctx, c.channel, string(payload)).Err(); err != nil {
		return fmt.Errorf("notify/redis: publish: %w", err)
	}

	return nil
}

// Subscribe starts listening on the Redis channel and returns a channel of events.
// The returned channel is closed when ctx is cancelled or Close is called.
func (c *Channel) Subscribe(ctx context.Context) (<-chan notify.Event, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, notify.ErrClosed
	}
	c.mu.Unlock()

	subCtx, cancel := context.WithCancel(ctx)

	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	pubsub := c.client.Subscribe(subCtx, c.channel)

	// Wait for confirmation that subscription is active.
	if _, err := pubsub.Receive(subCtx); err != nil {
		cancel()
		_ = pubsub.Close()
		return nil, fmt.Errorf("notify/redis: subscribe: %w", err)
	}

	ch := make(chan notify.Event, 16)
	redisCh := pubsub.Channel()

	go func() {
		defer func() {
			_ = pubsub.Close()
			close(ch)
		}()

		for {
			select {
			case msg, ok := <-redisCh:
				if !ok {
					return
				}

				var event notify.Event
				if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
					c.logger.Error("notify/redis: unmarshal message",
						dlog.Err(err),
						dlog.String("payload", msg.Payload),
					)
					continue
				}

				select {
				case ch <- event:
				case <-subCtx.Done():
					return
				}

			case <-subCtx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// Close stops the subscription and releases resources.
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
