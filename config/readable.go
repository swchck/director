package config

// ReadableCollection is a read-only view of a Collection.
// Use this as the exported type in config units to prevent
// consumers from calling Swap() directly.
//
//	type Products struct {
//	    col *config.Collection[Product]          // unexported: has Swap()
//	    All config.ReadableCollection[Product]   // exported: read-only
//	}
type ReadableCollection[T any] interface {
	Name() string
	Version() Version
	All() []T
	Count() int
	First() (T, bool)
	Find(func(T) bool) (T, bool)
	FindMany(func(T) bool) []T
	Filter(...FilterOption[T]) []T
}

// ReadableSingleton is a read-only view of a Singleton.
type ReadableSingleton[T any] interface {
	Name() string
	Version() Version
	Get() (T, bool)
}

// Compile-time interface compliance checks.
var _ ReadableCollection[int] = (*Collection[int])(nil)
var _ ReadableSingleton[int] = (*Singleton[int])(nil)
