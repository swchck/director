package config_test

import (
	"testing"
	"time"

	"github.com/swchck/director/config"
)

func TestVersion_NewVersion(t *testing.T) {
	ts := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)
	v := config.NewVersion(ts)

	if v.IsZero() {
		t.Error("NewVersion should not be zero")
	}

	if !v.Time().Equal(ts) {
		t.Errorf("Time() = %v, want %v", v.Time(), ts)
	}

	if v.String() == "" {
		t.Error("String() should not be empty")
	}
}

func TestVersion_ParseVersion(t *testing.T) {
	raw := "2025-03-15T10:30:00Z"
	v, err := config.ParseVersion(raw)
	if err != nil {
		t.Fatalf("ParseVersion error: %v", err)
	}

	if v.String() != raw {
		t.Errorf("String() = %q, want %q", v.String(), raw)
	}
}

func TestVersion_ParseVersion_Invalid(t *testing.T) {
	_, err := config.ParseVersion("not-a-date")
	if err == nil {
		t.Error("expected error for invalid version string")
	}
}

func TestVersion_IsZero(t *testing.T) {
	var v config.Version
	if !v.IsZero() {
		t.Error("zero value should be IsZero")
	}
}

func TestVersion_Equal(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	a := config.NewVersion(ts)
	b := config.NewVersion(ts)

	if !a.Equal(b) {
		t.Error("same timestamps should be equal")
	}
}

func TestVersion_After(t *testing.T) {
	older := config.NewVersion(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := config.NewVersion(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))

	if !newer.After(older) {
		t.Error("newer.After(older) should be true")
	}

	if older.After(newer) {
		t.Error("older.After(newer) should be false")
	}
}
