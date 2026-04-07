// Package manager orchestrates config synchronization across replicas.
//
// It handles polling, WebSocket-triggered syncs, leader election via advisory
// locks, snapshot persistence, cross-replica notifications, and optional caching.
package manager
