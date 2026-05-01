// Package diff classifies items between two slices into added, updated, and
// removed buckets keyed by a user-supplied function.
//
// Typical use is inside an OnChange hook: the consumer receives (old, new)
// slices on every Swap and wants to react only to the items that actually
// changed — emit a Kafka event for new rows, invalidate an external cache
// entry for updated rows, and so on.
//
// Example:
//
//	products.OnChange(func(old, new []Product) {
//	    added, updated, removed := diff.By(old, new, func(p Product) int { return p.ID })
//	    for _, p := range added   { publish("product.created", p) }
//	    for _, p := range updated { publish("product.updated", p) }
//	    for _, p := range removed { publish("product.deleted", p) }
//	})
//
// Items are matched by key. If multiple items share a key the result is
// unspecified — keys must be unique within each slice.
package diff

import "reflect"

// By categorizes items between oldSlice and newSlice using keyFn for identity.
//
//   - added contains items whose key is present in newSlice but not in oldSlice
//   - updated contains items from newSlice whose key is present in both slices
//     but whose value differs (compared with reflect.DeepEqual)
//   - removed contains items whose key is present in oldSlice but not in newSlice
//
// Updated items are returned with their new values. To compare with the
// previous value, look up the key in oldSlice.
//
// Order within each result slice is unspecified. For custom equality (e.g.
// to compare only specific fields, or to skip reflect for performance), use
// ByEqual.
func By[T any, K comparable](oldSlice, newSlice []T, keyFn func(T) K) (added, updated, removed []T) {
	return ByEqual(oldSlice, newSlice, keyFn, func(a, b T) bool {
		return reflect.DeepEqual(a, b)
	})
}

// ByEqual is like By but uses equal as the per-item equality predicate.
// equal is called for keys that exist in both slices; it receives (oldItem,
// newItem) and returns true if the item is considered unchanged.
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
