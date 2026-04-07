package log

import (
	"context"
	"log/slog"
)

// SlogAdapter wraps a *slog.Logger to implement the Logger interface.
type SlogAdapter struct {
	sl *slog.Logger
}

// NewSlog creates a Logger backed by the standard library's slog.
func NewSlog(sl *slog.Logger) Logger {
	return &SlogAdapter{sl: sl}
}

func (a *SlogAdapter) Debug(msg string, fields ...Field) {
	a.sl.LogAttrs(context.Background(), slog.LevelDebug, msg, toSlogAttrs(fields)...)
}

func (a *SlogAdapter) Info(msg string, fields ...Field) {
	a.sl.LogAttrs(context.Background(), slog.LevelInfo, msg, toSlogAttrs(fields)...)
}

func (a *SlogAdapter) Warn(msg string, fields ...Field) {
	a.sl.LogAttrs(context.Background(), slog.LevelWarn, msg, toSlogAttrs(fields)...)
}

func (a *SlogAdapter) Error(msg string, fields ...Field) {
	a.sl.LogAttrs(context.Background(), slog.LevelError, msg, toSlogAttrs(fields)...)
}

func toSlogAttrs(fields []Field) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(fields))
	for _, f := range fields {
		switch v := f.Value.(type) {
		case string:
			attrs = append(attrs, slog.String(f.Key, v))
		case int:
			attrs = append(attrs, slog.Int(f.Key, v))
		case bool:
			attrs = append(attrs, slog.Bool(f.Key, v))
		case error:
			attrs = append(attrs, slog.String(f.Key, v.Error()))
		case nil:
			// skip
		default:
			attrs = append(attrs, slog.Any(f.Key, v))
		}
	}

	return attrs
}
