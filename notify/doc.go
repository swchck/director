// Package notify defines the cross-replica notification interface.
//
// Implementations live in sub-packages:
//   - notify/postgres — PostgreSQL LISTEN/NOTIFY
//   - notify/redis — Redis Pub/Sub
package notify
