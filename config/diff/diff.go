// Package diff sorts items between two slices into added, updated and removed buckets by a
// user-supplied key — typically inside a config OnChange hook. See docs/config-package.md.
package diff

import "reflect"

// By categorizes items by keyFn identity: added and removed for keys on one side only, updated
// (new value) for keys on both differing under reflect.DeepEqual. Keys must be unique per slice.
func By[T any, K comparable](oldSlice, newSlice []T, keyFn func(T) K) (added, updated, removed []T) {
	return ByEqual(oldSlice, newSlice, keyFn, func(a, b T) bool {
		return reflect.DeepEqual(a, b)
	})
}

// ByEqual is like By but calls equal(oldItem, newItem) for keys present in both slices,
// treating a true result as unchanged.
func ByEqual[T any, K comparable](oldSlice, newSlice []T, keyFn func(T) K, equal func(a, b T) bool) (added, updated, removed []T) {
	oldByKey := make(map[K]T, len(oldSlice))
	for _, item := range oldSlice {
		oldByKey[keyFn(item)] = item
	}

	seen := make(map[K]struct{}, len(newSlice))
	for _, n := range newSlice {
		k := keyFn(n)
		seen[k] = struct{}{}

		o, existed := oldByKey[k]
		switch {
		case !existed:
			added = append(added, n)
		case !equal(o, n):
			updated = append(updated, n)
		}
	}

	for k, o := range oldByKey {
		if _, stillThere := seen[k]; !stillThere {
			removed = append(removed, o)
		}
	}

	return added, updated, removed
}
