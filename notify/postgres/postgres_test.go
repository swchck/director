package postgres_test

import (
	"testing"

	"github.com/swchck/director/notify"
	pgnotify "github.com/swchck/director/notify/postgres"
)

func TestChannel_ImplementsChannelInterface(t *testing.T) {
	var _ notify.Channel = (*pgnotify.Channel)(nil)
}
