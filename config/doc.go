// Package config provides in-memory data stores — Collection[T] and Singleton[T] — with
// lock-free reads and materialized views that recompute inside the source's OnChange chain.
//
// See docs/config-package.md for the view catalogue and for the concurrency, panic, and
// warm-start rules shared by every type here.
package config
