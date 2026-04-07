package redis_test

import (
	"testing"

	"github.com/swchck/director/notify"
	redisnotify "github.com/swchck/director/notify/redis"
)

func TestChannel_ImplementsChannelInterface(t *testing.T) {
	var _ notify.Channel = (*redisnotify.Channel)(nil)
}
