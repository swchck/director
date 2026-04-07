// Package log defines the Logger interface used throughout the director library.
//
// The library does not depend on any specific logging implementation.
// A built-in slog adapter is provided. For other loggers (zerolog, zap, etc.),
// implement the Logger interface — it's 4 methods.
// A no-op logger is used by default when no logger is configured.
//
// Usage with slog (built-in):
//
//	import dlog "github.com/swchck/director/log"
//	logger := dlog.NewSlog(slog.Default())
//
// Custom logger — implement the Logger interface:
//
//	type myLogger struct { ... }
//	func (l *myLogger) Debug(msg string, fields ...dlog.Field) { ... }
//	func (l *myLogger) Info(msg string, fields ...dlog.Field) { ... }
//	func (l *myLogger) Warn(msg string, fields ...dlog.Field) { ... }
//	func (l *myLogger) Error(msg string, fields ...dlog.Field) { ... }
package log

// Logger is the structured logging interface used by all library components.
//
// Implementations must be safe for concurrent use.
type Logger interface {
	// Debug logs a message at debug level with optional key-value fields.
	Debug(msg string, fields ...Field)

	// Info logs a message at info level with optional key-value fields.
	Info(msg string, fields ...Field)

	// Warn logs a message at warn level with optional key-value fields.
	Warn(msg string, fields ...Field)

	// Error logs a message at error level with optional key-value fields.
	Error(msg string, fields ...Field)
}

// Field is a structured log field (key-value pair).
type Field struct {
	Key   string
	Value any
}

// String creates a string field.
func String(key, value string) Field {
	return Field{Key: key, Value: value}
}

// Int creates an integer field.
func Int(key string, value int) Field {
	return Field{Key: key, Value: value}
}

// Bool creates a boolean field.
func Bool(key string, value bool) Field {
	return Field{Key: key, Value: value}
}

// Err creates an error field with the key "error".
// The error value is stored as-is, preserving the error chain for
// errors.Is/errors.As in logging handlers.
func Err(err error) Field {
	if err == nil {
		return Field{Key: "error", Value: nil}
	}

	return Field{Key: "error", Value: err}
}

// Strings creates a string slice field.
func Strings(key string, values []string) Field {
	return Field{Key: key, Value: values}
}

// Nop returns a logger that discards all output.
func Nop() Logger {
	return nopLogger{}
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...Field) {}
func (nopLogger) Info(string, ...Field)  {}
func (nopLogger) Warn(string, ...Field)  {}
func (nopLogger) Error(string, ...Field) {}
