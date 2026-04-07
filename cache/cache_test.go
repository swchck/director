package cache_test

import (
	"testing"

	"github.com/swchck/director/cache"
)

func TestStrategy_String(t *testing.T) {
	tests := []struct {
		s    cache.Strategy
		want string
	}{
		{cache.ReadThrough, "read-through"},
		{cache.WriteThrough, "write-through"},
		{cache.WriteBehind, "write-behind"},
		{cache.ReadWriteThrough, "read-write-through"},
		{cache.Strategy(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestStrategy_ReadsFromCache(t *testing.T) {
	if !cache.ReadThrough.ReadsFromCache() {
		t.Error("ReadThrough should read")
	}

	if !cache.ReadWriteThrough.ReadsFromCache() {
		t.Error("ReadWriteThrough should read")
	}

	if cache.WriteThrough.ReadsFromCache() {
		t.Error("WriteThrough should not read")
	}

	if cache.WriteBehind.ReadsFromCache() {
		t.Error("WriteBehind should not read")
	}
}

func TestStrategy_WritesToCache(t *testing.T) {
	if !cache.WriteThrough.WritesToCache() {
		t.Error("WriteThrough should write")
	}

	if !cache.WriteBehind.WritesToCache() {
		t.Error("WriteBehind should write")
	}

	if !cache.ReadWriteThrough.WritesToCache() {
		t.Error("ReadWriteThrough should write")
	}

	if cache.ReadThrough.WritesToCache() {
		t.Error("ReadThrough should not write")
	}
}

func TestStrategy_IsAsync(t *testing.T) {
	if !cache.WriteBehind.IsAsync() {
		t.Error("WriteBehind should be async")
	}

	if cache.WriteThrough.IsAsync() {
		t.Error("WriteThrough should not be async")
	}
}

func TestSentinelErrors(t *testing.T) {
	if cache.ErrCacheMiss == nil {
		t.Error("ErrCacheMiss should not be nil")
	}
	if cache.ErrClosed == nil {
		t.Error("ErrClosed should not be nil")
	}
}

func TestEntry_Fields(t *testing.T) {
	e := cache.Entry{
		Collection: "settings",
		Version:    "v1",
		Content:    []byte("content"),
	}

	if e.Collection != "settings" {
		t.Errorf("Collection = %q", e.Collection)
	}
	if e.Version != "v1" {
		t.Errorf("Version = %q", e.Version)
	}
	if string(e.Content) != "content" {
		t.Errorf("Content = %q", e.Content)
	}
}
