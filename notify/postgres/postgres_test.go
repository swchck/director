package postgres_test

import (
	"testing"
	"time"

	"github.com/swchck/director/notify"
	pgnotify "github.com/swchck/director/notify/postgres"
)

func TestChannel_ImplementsChannelInterface(t *testing.T) {
	var _ notify.Channel = (*pgnotify.Channel)(nil)
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
