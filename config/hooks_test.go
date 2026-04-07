package config_test

import (
	"testing"
	"time"

	"github.com/swchck/director/config"
)

func TestSafeCallHooks_RecoversPanic(t *testing.T) {
	c := config.NewCollection[item]("test")
	c.OnChange(func(_, _ []item) {
		panic("hook panic")
	})

	err := c.Swap(config.NewVersion(time.Now()), []item{{ID: 1}})
	if err == nil {
		t.Fatal("expected error from panicking hook")
	}

	if c.Count() != 1 {
		t.Error("swap should commit before hooks")
	}
}

func TestMultipleHooks_AllFire(t *testing.T) {
	c := config.NewCollection[item]("test")

	var calls int
	c.OnChange(func(_, _ []item) { calls++ })
	c.OnChange(func(_, _ []item) { calls++ })
	c.OnChange(func(_, _ []item) { calls++ })

	_ = c.Swap(config.NewVersion(time.Now()), []item{{ID: 1}})

	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestSingleton_HookPanic_Recovers(t *testing.T) {
	s := config.NewSingleton[appConfig]("test")
	s.OnChange(func(_, _ *appConfig) {
		panic("singleton panic")
	})

	err := s.Swap(config.NewVersion(time.Now()), appConfig{MaxItems: 1})
	if err == nil {
		t.Fatal("expected error")
	}

	got, ok := s.Get()
	if !ok || got.MaxItems != 1 {
		t.Error("swap should commit before hooks")
	}
}
