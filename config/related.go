package config

import (
	"sync"
	"sync/atomic"
)

// relatedSnapshot holds flattened related items.
type relatedSnapshot[R any] struct {
	items   []R
	version Version
}

// RelatedView extracts and flattens nested related items (M2M, O2M) from a
// parent Collection into a queryable, auto-updating view.
//
// When the parent collection changes, the view re-extracts and re-flattens
// all related items automatically.
//
// Example — articles have an M2M connection to tags:
//
//	type Article struct {
//	    ID   int    `json:"id"`
//	    Name string `json:"name"`
//	    Tags []Tag  `json:"tags"` // populated via WithFields("*", "tags.*")
//	}
//
//	type Tag struct {
//	    ID    int     `json:"id"`
//	    Name  string  `json:"name"`
//	    Score float64 `json:"score"`
//	}
//
//	// Create a flattened view of all tags across all articles.
//	allTags := config.NewRelatedView("article-tags", articles,
//	    func(a Article) []Tag { return a.Tags },
//	)
//
//	// Query the related items directly.
//	highPriority := allTags.Filter(
//	    config.Where(func(t Tag) bool { return t.Priority > 5 }),
//	)
//	tag, ok := allTags.Find(func(t Tag) bool { return t.ID == 5 })
//	count := allTags.Count()
type RelatedView[T any, R any] struct {
	name    string
	source  *Collection[T]
	extract func(T) []R
	dedup   func(R, R) bool // optional: returns true if two items are the same
	onError ErrorFunc
	unsub   func()

	data atomic.Pointer[relatedSnapshot[R]]

	mu    sync.RWMutex
	hooks []func(old, new []R)
}

// RelatedViewOption configures a RelatedView.
type RelatedViewOption[T any, R any] func(*RelatedView[T, R])

// WithDedup sets a deduplication function for the related view.
// When M2M items appear under multiple parents, dedup prevents duplicates.
// The function should return true if two items represent the same entity.
//
// Note: deduplication uses linear search per item, resulting in O(n²) time
// complexity where n is the total number of related items. For collections
// with thousands of related items, consider deduplicating in the extract
// function or using an IndexedView instead.
//
// Example:
//
//	config.WithDedup[Article, Tag](func(a, b Tag) bool { return a.ID == b.ID })
func WithDedup[T any, R any](fn func(a, b R) bool) RelatedViewOption[T, R] {
	return func(v *RelatedView[T, R]) {
		v.dedup = fn
	}
}

// WithRelatedViewErrorHandler sets an error callback for hook panics.
// Without this, recovered hook panics are silently ignored.
func WithRelatedViewErrorHandler[T any, R any](fn ErrorFunc) RelatedViewOption[T, R] {
	return func(v *RelatedView[T, R]) {
		v.onError = fn
	}
}

// NewRelatedView creates an auto-updating view that extracts and flattens nested
// related items from each item in the source collection.
//
// extract is called for each parent item to get its related items.
// The results are flattened into a single slice.
//
// Use WithDedup to remove duplicates when the same related item appears under
// multiple parents (common with M2M relations).
func NewRelatedView[T any, R any](name string, source *Collection[T], extract func(T) []R, opts ...RelatedViewOption[T, R]) *RelatedView[T, R] {
	v := &RelatedView[T, R]{
		name:    name,
		source:  source,
		extract: extract,
	}

	for _, opt := range opts {
		opt(v)
	}

	v.data.Store(&relatedSnapshot[R]{})

	// Compute initial state.
	v.recompute(source.All(), source.Version())

	// Auto-update when source changes.
	v.unsub = source.OnChange(func(_, newItems []T) {
		v.recompute(newItems, source.Version())
	})

	return v
}

// Name returns the view name.
func (v *RelatedView[T, R]) Name() string {
	return v.name
}

// Version returns the current snapshot version.
func (v *RelatedView[T, R]) Version() Version {
	return v.data.Load().version
}

// Close unsubscribes the view from its source collection. After Close,
// the view stops recomputing on source changes. It is safe to call
// Close multiple times.
func (v *RelatedView[T, R]) Close() {
	if v.unsub != nil {
		v.unsub()
		v.unsub = nil
	}
}

// All returns a copy of all flattened related items.
func (v *RelatedView[T, R]) All() []R {
	items := v.data.Load().items
	result := make([]R, len(items))
	copy(result, items)

	return result
}

// Count returns the number of flattened related items.
func (v *RelatedView[T, R]) Count() int {
	return len(v.data.Load().items)
}

// First returns the first related item, or false if empty.
func (v *RelatedView[T, R]) First() (R, bool) {
	items := v.data.Load().items
	if len(items) == 0 {
		var zero R
		return zero, false
	}

	return items[0], true
}

// Find returns the first related item matching the predicate.
//
// The predicate must not panic. If it does, the panic propagates to the caller.
func (v *RelatedView[T, R]) Find(pred func(R) bool) (R, bool) {
	for _, item := range v.data.Load().items {
		if pred(item) {
			return item, true
		}
	}

	var zero R
	return zero, false
}

// FindMany returns all related items matching the predicate.
//
// The predicate must not panic. If it does, the panic propagates to the caller.
func (v *RelatedView[T, R]) FindMany(pred func(R) bool) []R {
	var result []R
	for _, item := range v.data.Load().items {
		if pred(item) {
			result = append(result, item)
		}
	}

	return result
}

// Filter applies filter options to the flattened related items.
func (v *RelatedView[T, R]) Filter(opts ...FilterOption[R]) []R {
	return applyFilters(v.data.Load().items, opts)
}

// OnChange registers a callback that fires after the view recomputes.
// Returns a function that removes the hook when called.
func (v *RelatedView[T, R]) OnChange(fn func(old, new []R)) func() {
	v.mu.Lock()
	defer v.mu.Unlock()

	idx := len(v.hooks)
	v.hooks = append(v.hooks, fn)

	return func() {
		v.mu.Lock()
		defer v.mu.Unlock()

		if idx < len(v.hooks) {
			v.hooks[idx] = nil
		}
	}
}

// ForParent returns the related items for a specific parent item.
// This is useful when you need related items for one parent without scanning all.
//
// Example:
//
//	article, _ := articles.Find(func(a Article) bool { return a.ID == 42 })
//	article42Tags := allTags.ForParent(articles, article,
//	    func(a Article) []Tag { return a.Tags },
//	)
//
// Note: this reads from the source collection directly, not from the flattened cache.
// For most cases, querying the parent struct directly (article.Tags) is simpler.

func (v *RelatedView[T, R]) recompute(parents []T, version Version) {
	var items []R
	for _, parent := range parents {
		related := v.extract(parent)
		if v.dedup == nil {
			items = append(items, related...)
			continue
		}

		for _, r := range related {
			if !v.contains(items, r) {
				items = append(items, r)
			}
		}
	}

	old := v.data.Load()
	v.data.Store(&relatedSnapshot[R]{
		items:   items,
		version: version,
	})

	v.mu.RLock()
	hooks := v.hooks
	v.mu.RUnlock()

	wrappers := make([]func(), 0, len(hooks))
	for _, fn := range hooks {
		if fn == nil {
			continue
		}

		fn := fn
		wrappers = append(wrappers, func() { fn(old.items, items) })
	}

	if err := safeCallHooks(wrappers...); err != nil {
		if v.onError != nil {
			v.onError(v.name, err)
		}
	}
}

func (v *RelatedView[T, R]) contains(items []R, candidate R) bool {
	for _, item := range items {
		if v.dedup(item, candidate) {
			return true
		}
	}

	return false
}
