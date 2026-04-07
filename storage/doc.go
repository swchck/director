// Package storage defines the persistence interface for config snapshots,
// apply logging, and distributed lock acquisition.
//
// Implementations live in sub-packages:
//   - storage/postgres — PostgreSQL-backed storage with advisory locks
package storage
