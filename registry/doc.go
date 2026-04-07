// Package registry defines the instance registry interface for tracking
// live service replicas during coordinated config sync.
//
// Implementations live in sub-packages:
//   - registry/postgres — PostgreSQL-backed registry with heartbeat
package registry
