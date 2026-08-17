package config

// ReadableCollection is the read-only surface of a Collection. Export this from a config unit,
// keeping the Collection unexported, so only the manager reaches Swap.
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

var _ ReadableCollection[int] = (*Collection[int])(nil)
var _ ReadableCollection[int] = (*View[int])(nil)
var _ ReadableCollection[int] = (*RelatedView[int, int])(nil)
var _ ReadableCollection[int] = (*TranslatedView[int, int])(nil)
var _ ReadableSingleton[int] = (*Singleton[int])(nil)
