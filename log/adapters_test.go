package log_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	dlog "github.com/swchck/director/log"
)

func TestNop_ImplementsLogger(t *testing.T) {
	var _ = dlog.Nop()
}

func TestNop_AllLevels_NoPanic(t *testing.T) {
	l := dlog.Nop()

	l.Debug("msg")
	l.Info("msg", dlog.String("k", "v"))
	l.Warn("msg", dlog.Int("n", 42), dlog.Bool("b", true))
	l.Error("msg", dlog.Err(errors.New("test")), dlog.Strings("s", []string{"a"}))
}

func TestSlogAdapter_ImplementsLogger(t *testing.T) {
	sl := slog.Default()
	var _ = dlog.NewSlog(sl)
}

func TestSlogAdapter_WritesMessages(t *testing.T) {
	var buf bytes.Buffer
	sl := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := dlog.NewSlog(sl)

	l.Info("hello world", dlog.String("key", "value"))

	output := buf.String()
	if !strings.Contains(output, "hello world") {
		t.Errorf("output does not contain message: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("output does not contain field: %s", output)
	}
}

func TestSlogAdapter_AllLevels(t *testing.T) {
	var buf bytes.Buffer
	sl := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := dlog.NewSlog(sl)

	l.Debug("debug msg")
	l.Info("info msg")
	l.Warn("warn msg")
	l.Error("error msg")

	output := buf.String()
	for _, level := range []string{"DEBUG", "INFO", "WARN", "ERROR"} {
		if !strings.Contains(output, level) {
			t.Errorf("output missing level %s: %s", level, output)
		}
	}
}

func TestSlogAdapter_NilError(t *testing.T) {
	var buf bytes.Buffer
	sl := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := dlog.NewSlog(sl)

	l.Info("no error", dlog.Err(nil))
}

func TestSlogAdapter_IntField(t *testing.T) {
	var buf bytes.Buffer
	sl := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := dlog.NewSlog(sl)

	l.Info("count", dlog.Int("n", 42))

	if !strings.Contains(buf.String(), "n=42") {
		t.Errorf("output does not contain n=42: %s", buf.String())
	}
}

func TestSlogAdapter_BoolField(t *testing.T) {
	var buf bytes.Buffer
	sl := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := dlog.NewSlog(sl)

	l.Info("flag", dlog.Bool("enabled", true))

	if !strings.Contains(buf.String(), "enabled=true") {
		t.Errorf("output does not contain enabled=true: %s", buf.String())
	}
}

func TestField_String(t *testing.T) {
	f := dlog.String("name", "test")
	if f.Key != "name" || f.Value != "test" {
		t.Errorf("String field: %+v", f)
	}
}

func TestField_Int(t *testing.T) {
	f := dlog.Int("count", 10)
	if f.Key != "count" || f.Value != 10 {
		t.Errorf("Int field: %+v", f)
	}
}

func TestField_Bool(t *testing.T) {
	f := dlog.Bool("active", false)
	if f.Key != "active" || f.Value != false {
		t.Errorf("Bool field: %+v", f)
	}
}

func TestField_Err(t *testing.T) {
	err := errors.New("something failed")
	f := dlog.Err(err)
	if f.Key != "error" {
		t.Errorf("Err key = %q, want 'error'", f.Key)
	}
	errVal, ok := f.Value.(error)
	if !ok {
		t.Fatalf("Err value type = %T, want error", f.Value)
	}
	if errVal.Error() != "something failed" {
		t.Errorf("Err value = %v, want 'something failed'", errVal)
	}
	if !errors.Is(errVal, err) {
		t.Errorf("Err value should match original error via errors.Is")
	}
}

func TestField_ErrNil(t *testing.T) {
	f := dlog.Err(nil)
	if f.Key != "error" {
		t.Errorf("Err(nil) key = %q", f.Key)
	}
	if f.Value != nil {
		t.Errorf("Err(nil) value = %v, want nil", f.Value)
	}
}

func TestField_Strings(t *testing.T) {
	f := dlog.Strings("tags", []string{"go", "test"})
	if f.Key != "tags" {
		t.Errorf("Strings key = %q", f.Key)
	}

	vals, ok := f.Value.([]string)
	if !ok {
		t.Fatalf("Strings value type = %T", f.Value)
	}
	if len(vals) != 2 || vals[0] != "go" || vals[1] != "test" {
		t.Errorf("Strings value = %v", vals)
	}
}
