package config

import "time"

// Version is an opaque config version, normally the source's last-modified timestamp. Equal
// compares the raw RFC3339Nano string: byte-level agreement across replicas is the invariant.
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

// ParseVersion creates a Version from a raw RFC3339 string, keeping raw verbatim so
// it round-trips through String unchanged.
func ParseVersion(raw string) (Version, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return Version{}, err
	}

	return Version{raw: raw, t: t}, nil
}

// String returns the raw RFC3339Nano representation.
func (v Version) String() string {
	return v.raw
}

// Time returns the underlying time value.
func (v Version) Time() time.Time {
	return v.t
}

// IsZero reports whether the version is unset. Other packages read that as "unknown
// version, fetch unconditionally".
func (v Version) IsZero() bool {
	return v.raw == ""
}

// Equal reports whether two versions have the same raw string; two spellings of one instant
// are not Equal.
func (v Version) Equal(other Version) bool {
	return v.raw == other.raw
}

// After reports whether v is newer than other, comparing parsed times rather than raw strings.
func (v Version) After(other Version) bool {
	return v.t.After(other.t)
}
