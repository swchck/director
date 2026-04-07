package config_test

import (
	"testing"

	"github.com/swchck/director/config"
)

type appConfig struct {
	MaxItems int
	Debug    bool
}

func TestSingleton_NewSingleton_EmptyByDefault(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")

	if s.Name() != "app_config" {
		t.Errorf("Name() = %q", s.Name())
	}

	_, ok := s.Get()
	if ok {
		t.Error("Get() should return false on empty singleton")
	}

	if !s.Version().IsZero() {
		t.Errorf("Version() should be zero")
	}
}

func TestSingleton_Swap_UpdatesValue(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")

	err := s.Swap(v1(), appConfig{MaxItems: 100, Debug: true})
	if err != nil {
		t.Fatalf("Swap error: %v", err)
	}

	got, ok := s.Get()
	if !ok {
		t.Fatal("Get() returned false after Swap")
	}

	if got.MaxItems != 100 || !got.Debug {
		t.Errorf("Get() = %+v", got)
	}
}

func TestSingleton_Swap_UpdatesVersion(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")

	_ = s.Swap(v1(), appConfig{})
	if !s.Version().Equal(v1()) {
		t.Errorf("Version() = %s, want %s", s.Version(), v1())
	}

	_ = s.Swap(v2(), appConfig{})
	if !s.Version().Equal(v2()) {
		t.Errorf("Version() = %s, want %s", s.Version(), v2())
	}
}

func TestSingleton_OnChange_FiresOnSwap(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")

	var oldVal, newVal *appConfig
	s.OnChange(func(old, new *appConfig) {
		oldVal = old
		newVal = new
	})

	_ = s.Swap(v1(), appConfig{MaxItems: 50})

	if oldVal != nil {
		t.Errorf("first swap: old should be nil, got %+v", oldVal)
	}

	if newVal == nil || newVal.MaxItems != 50 {
		t.Errorf("first swap: new = %+v, want MaxItems=50", newVal)
	}

	_ = s.Swap(v2(), appConfig{MaxItems: 100})

	if oldVal == nil || oldVal.MaxItems != 50 {
		t.Errorf("second swap: old = %+v, want MaxItems=50", oldVal)
	}

	if newVal == nil || newVal.MaxItems != 100 {
		t.Errorf("second swap: new = %+v, want MaxItems=100", newVal)
	}
}

func TestSingleton_Swap_RecoversPanicInHook(t *testing.T) {
	s := config.NewSingleton[appConfig]("app_config")

	s.OnChange(func(_, _ *appConfig) {
		panic("boom")
	})

	err := s.Swap(v1(), appConfig{MaxItems: 42})
	if err == nil {
		t.Fatal("expected error from panicking hook")
	}

	// Data should still be swapped.
	got, ok := s.Get()
	if !ok || got.MaxItems != 42 {
		t.Errorf("data not swapped: ok=%v, got=%+v", ok, got)
	}
}
