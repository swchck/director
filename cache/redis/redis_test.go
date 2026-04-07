package redis_test

import (
	"testing"

	"github.com/swchck/director/cache"
	cacheredis "github.com/swchck/director/cache/redis"
)

func TestCache_ImplementsCacheInterface(t *testing.T) {
	var _ cache.Cache = (*cacheredis.Cache)(nil)
}

func TestCache_Construction(t *testing.T) {
	c := cacheredis.NewCache(nil,
		cacheredis.WithTTL(0),
		cacheredis.WithKeyPrefix("test:"),
	)
	if c == nil {
		t.Error("NewCache returned nil")
	}
}

func TestViewStore_Construction(t *testing.T) {
	s := cacheredis.NewViewStore(nil,
		cacheredis.WithViewTTL(0),
		cacheredis.WithViewKeyPrefix("test:"),
	)
	if s == nil {
		t.Error("NewViewStore returned nil")
	}
}
