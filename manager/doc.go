// Package manager orchestrates config synchronization across replicas: polling,
// WebSocket-triggered syncs, leader election, snapshots, notify, and caching.
//
// # Single-writer invariant
//
// Registered config state is mutated only on the goroutine that calls Start, so
// swaps never overlap. A supported guarantee, not an implementation detail:
// consumers assemble cross-collection aggregates in OnChange hooks. Rules and
// consequences: docs/sync-protocol.md "Single-Writer Invariant".
package manager
