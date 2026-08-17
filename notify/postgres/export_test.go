package postgres

import "time"

func NextBackoff(current time.Duration) time.Duration {
	return nextBackoff(current)
}
