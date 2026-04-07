package config

import "time"

// Version represents an opaque config version derived from Directus date_updated.
// It uses RFC3339 format for deterministic comparison across replicas.
type Version struct {
	raw string
	t   time.Time
}

// NewVersion creates a Version from a time.Time value.
func NewVersion(t time.Time) Version {
	return Version{
		raw: t.UTC().Format(time.RFC3339Nano),
		t:   t.UTC(),
	}
}

// ParseVersion creates a Version from a raw RFC3339 string.
func ParseVersion(raw string) (Version, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return Version{}, err
	}

	return Version{raw: raw, t: t}, nil
}

// String returns the RFC3339 representation.
func (v Version) String() string {
	return v.raw
}

// Time returns the underlying time value.
func (v Version) Time() time.Time {
	return v.t
}

// IsZero reports whether the version is unset.
func (v Version) IsZero() bool {
	return v.raw == ""
}

// Equal reports whether two versions represent the same point in time.
func (v Version) Equal(other Version) bool {
	return v.raw == other.raw
}

// After reports whether v is newer than other.
func (v Version) After(other Version) bool {
	return v.t.After(other.t)
}
