package postgres_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/swchck/director/notify"
	pgnotify "github.com/swchck/director/notify/postgres"
)

func TestChannel_ImplementsChannelInterface(t *testing.T) {
	var _ notify.Channel = (*pgnotify.Channel)(nil)
}

func TestNewChannel_ValidChannelNames(t *testing.T) {
	pool := (*pgxpool.Pool)(nil)

	valid := []string{"config_sync", "my_channel", "_private", "A", "a1_2_3"}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("valid channel name %q caused panic: %v", name, r)
				}
			}()
			_ = pgnotify.NewChannel(pool, pgnotify.WithChannel(name))
		})
	}
}

func TestNewChannel_InvalidChannelNames(t *testing.T) {
	pool := (*pgxpool.Pool)(nil)

	invalid := []string{
		"",
		"123abc",
		"foo bar",
		"foo;DROP TABLE x",
		"foo--comment",
		"foo'bar",
		"channel.name",
	}
	for _, name := range invalid {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("invalid channel name %q did not panic", name)
				}
			}()
			_ = pgnotify.NewChannel(pool, pgnotify.WithChannel(name))
		})
	}
}

func TestNewChannel_DefaultChannelIsValid(t *testing.T) {
	pool := (*pgxpool.Pool)(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("default channel name caused panic: %v", r)
		}
	}()
	_ = pgnotify.NewChannel(pool)
}

func TestNextBackoff(t *testing.T) {
	tests := []struct {
		name    string
		current time.Duration
		want    time.Duration
	}{
		{"zero returns min", 0, 1 * time.Second},
		{"below min returns min", 500 * time.Millisecond, 1 * time.Second},
		{"1s doubles to 2s", 1 * time.Second, 2 * time.Second},
		{"2s doubles to 4s", 2 * time.Second, 4 * time.Second},
		{"4s doubles to 8s", 4 * time.Second, 8 * time.Second},
		{"16s doubles to 30s cap", 16 * time.Second, 30 * time.Second},
		{"30s stays at 30s cap", 30 * time.Second, 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pgnotify.NextBackoff(tt.current)
			if got != tt.want {
				t.Errorf("NextBackoff(%v) = %v, want %v", tt.current, got, tt.want)
			}
		})
	}
}
