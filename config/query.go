package config

import "sort"

// FilterOption transforms a slice of items in a pipeline fashion.
// Options are applied in order: filter first, then sort, then offset, then limit.
type FilterOption[T any] func([]T) []T

// Where filters items by a predicate.
func Where[T any](pred func(T) bool) FilterOption[T] {
	return func(items []T) []T {
		result := make([]T, 0, len(items))
		for _, item := range items {
			if pred(item) {
				result = append(result, item)
			}
		}

		return result
	}
}

// SortBy sorts items using a comparison function.
// The cmp function should return a negative number when a < b,
// zero when a == b, and a positive number when a > b.
func SortBy[T any](cmp func(a, b T) int) FilterOption[T] {
	return func(items []T) []T {
		result := make([]T, len(items))
		copy(result, items)
		sort.Slice(result, func(i, j int) bool {
			return cmp(result[i], result[j]) < 0
		})

		return result
	}
}

// Limit restricts the result to at most n items.
func Limit[T any](n int) FilterOption[T] {
	return func(items []T) []T {
		if n >= len(items) {
			return items
		}

		return items[:n]
	}
}

// Offset skips the first n items.
func Offset[T any](n int) FilterOption[T] {
	return func(items []T) []T {
		if n >= len(items) {
			return nil
		}

		return items[n:]
	}
}

// applyFilters runs all filter options in sequence.
func applyFilters[T any](items []T, opts []FilterOption[T]) []T {
	if len(opts) == 0 {
		return items
	}

	result := make([]T, len(items))
	copy(result, items)

	for _, opt := range opts {
		result = opt(result)
	}

	return result
}
