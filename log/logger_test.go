package log_test

import (
	"errors"
	"log/slog"
	"os"
	"testing"

	dlog "github.com/swchck/director/log"
)

func TestNop_DoesNotPanic(t *testing.T) {
	l := dlog.Nop()
	l.Debug("debug")
	l.Info("info")
	l.Warn("warn")
	l.Error("error")
	l.Debug("with fields", dlog.String("k", "v"), dlog.Int("n", 1), dlog.Bool("b", true))
}

func TestFieldBuilders(t *testing.T) {
	f := dlog.String("key", "val")
	if f.Key != "key" || f.Value != "val" {
		t.Errorf("String: %+v", f)
	}

	f = dlog.Int("n", 42)
	if f.Key != "n" || f.Value != 42 {
		t.Errorf("Int: %+v", f)
	}

	f = dlog.Bool("ok", true)
	if f.Key != "ok" || f.Value != true {
		t.Errorf("Bool: %+v", f)
	}

	f = dlog.Err(errors.New("boom"))
	if f.Key != "error" {
		t.Errorf("Err key: %+v", f)
	}
	if errVal, ok := f.Value.(error); !ok || errVal.Error() != "boom" {
		t.Errorf("Err value: %+v", f)
	}

	f = dlog.Err(nil)
	if f.Key != "error" || f.Value != nil {
		t.Errorf("Err(nil): %+v", f)
	}

	f = dlog.Strings("tags", []string{"a", "b"})
	if f.Key != "tags" {
		t.Errorf("Strings: %+v", f)
	}
}

func TestSlogAdapter(t *testing.T) {
	sl := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := dlog.NewSlog(sl)

	l.Debug("debug msg", dlog.String("k", "v"))
	l.Info("info msg", dlog.Int("n", 1))
	l.Warn("warn msg", dlog.Bool("b", false))
	l.Error("error msg", dlog.Err(errors.New("test")))
}
