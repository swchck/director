package postgres

import "time"

// NextBackoff is exported for testing.
func NextBackoff(current time.Duration) time.Duration {
	return nextBackoff(current)
}
