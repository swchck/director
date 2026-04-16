package config

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// FindTranslation returns the first translation where langFn(item) matches the target language.
//
// langFn extracts the language code from a translation struct.
// This approach avoids requiring translation types to implement an interface.
//
// Example:
//
//	type ProductTranslation struct {
//	    LanguagesCode string `json:"languages_code"`
//	    Name          string `json:"name"`
//	    Description   string `json:"description"`
//	}
//
//	tr, ok := config.FindTranslation(product.Translations,
//	    func(t ProductTranslation) string { return t.LanguagesCode },
//	    "en-US",
//	)
func FindTranslation[T any](translations []T, langFn func(T) string, targetLang string) (T, bool) {
	for _, tr := range translations {
		if langFn(tr) == targetLang {
			return tr, true
		}
	}

	var zero T
	return zero, false
}

// FindTranslationWithFallback returns the translation for targetLang, or the first
// matching fallback language, or false if none match.
//
// Example:
//
//	tr, ok := config.FindTranslationWithFallback(product.Translations,
//	    func(t ProductTranslation) string { return t.LanguagesCode },
//	    "de-DE",       // preferred
//	    "en-US", "en", // fallbacks
//	)
func FindTranslationWithFallback[T any](translations []T, langFn func(T) string, targetLang string, fallbacks ...string) (T, bool) {
	if tr, ok := FindTranslation(translations, langFn, targetLang); ok {
		return tr, true
	}

	for _, lang := range fallbacks {
		if tr, ok := FindTranslation(translations, langFn, lang); ok {
			return tr, true
		}
	}

	var zero T
	return zero, false
}

// TranslationMap converts a translations slice to a map keyed by language code.
//
// Example:
//
//	trMap := config.TranslationMap(product.Translations,
//	    func(t ProductTranslation) string { return t.LanguagesCode },
//	)
//	enName := trMap["en-US"].Name
func TranslationMap[T any](translations []T, langFn func(T) string) map[string]T {
	m := make(map[string]T, len(translations))
	for _, tr := range translations {
		m[langFn(tr)] = tr
	}

	return m
}

// TranslatedCollection is a View that flattens a collection with translations
// into a per-language snapshot. It creates one View per language, each containing
// items paired with their matching translation.
//
// This is the high-level helper for working with Directus translated collections.
//
// Example:
//
//	type Product struct {
//	    ID           int                  `json:"id"`
//	    SKU          string               `json:"sku"`
//	    Translations []ProductTranslation `json:"translations"`
//	}
//
//	type LocalizedProduct struct {
//	    ID          int
//	    SKU         string
//	    Name        string
//	    Description string
//	}
//
//	enProducts := config.NewTranslatedView("products-en", products,
//	    func(p Product) LocalizedProduct {
//	        tr, _ := config.FindTranslation(p.Translations,
//	            func(t ProductTranslation) string { return t.LanguagesCode },
//	            "en-US",
//	        )
//	        return LocalizedProduct{
//	            ID: p.ID, SKU: p.SKU,
//	            Name: tr.Name, Description: tr.Description,
//	        }
//	    },
//	)
//
//	items := enProducts.All() // []LocalizedProduct

// TranslatedView is an auto-updating view that transforms each item in a Collection
// into a different type. Commonly used for flattening translations into a localized struct.
type TranslatedView[T any, R any] struct {
	name               string
	source             *Collection[T]
	transform          func(T) R
	unsub              func()
	persistence        ViewPersistence
	persistenceTimeout time.Duration
	onError            ErrorFunc
	persistSem         chan struct{}

	data *Collection[R]
}

// TranslatedViewOption configures optional TranslatedView behavior.
type TranslatedViewOption[T any, R any] func(*TranslatedView[T, R])

// WithTranslatedViewPersistence enables external persistence for the translated view.
func WithTranslatedViewPersistence[T any, R any](p ViewPersistence) TranslatedViewOption[T, R] {
	return func(v *TranslatedView[T, R]) {
		v.persistence = p
	}
}

// WithTranslatedViewErrorHandler sets an error callback for persistence failures.
func WithTranslatedViewErrorHandler[T any, R any](fn ErrorFunc) TranslatedViewOption[T, R] {
	return func(v *TranslatedView[T, R]) {
		v.onError = fn
	}
}

// WithTranslatedViewPersistenceTimeout sets the timeout for persistence operations.
// Default: 10 seconds.
func WithTranslatedViewPersistenceTimeout[T any, R any](d time.Duration) TranslatedViewOption[T, R] {
	return func(v *TranslatedView[T, R]) {
		v.persistenceTimeout = d
	}
}

// NewTranslatedView creates a view that transforms each item in the source collection.
//
// transform is called for each item on every update. The result is a Collection-like
// view of the transformed type R.
//
// Internally maintains a derived Collection[R] that auto-updates when the
// source changes. For filtering without transforming, use NewView instead.
func NewTranslatedView[T any, R any](name string, source *Collection[T], transform func(T) R, opts ...TranslatedViewOption[T, R]) *TranslatedView[T, R] {
	tv := &TranslatedView[T, R]{
		name:      name,
		source:    source,
		transform: transform,
		data:      NewCollection[R](name + ":derived"),
	}

	for _, opt := range opts {
		opt(tv)
	}

	if tv.persistence != nil {
		tv.persistSem = make(chan struct{}, defaultPersistenceMaxConcurrency)
		tv.loadFromPersistence()
	}

	// Compute initial state.
	items := transformSlice(source.All(), transform)
	_ = tv.data.Swap(source.Version(), items)

	// Re-derive on source change.
	tv.unsub = source.OnChange(func(_, newItems []T) {
		transformed := transformSlice(newItems, transform)
		_ = tv.data.Swap(source.Version(), transformed)

		tv.persistAsync()
	})

	// Persist initial state.
	tv.persistAsync()

	return tv
}

// All returns all transformed items.
func (tv *TranslatedView[T, R]) All() []R {
	return tv.data.All()
}

// Count returns the number of items.
func (tv *TranslatedView[T, R]) Count() int {
	return tv.data.Count()
}

// First returns the first transformed item.
func (tv *TranslatedView[T, R]) First() (R, bool) {
	return tv.data.First()
}

// Find returns the first transformed item matching the predicate.
//
// The predicate must not panic. If it does, the panic propagates to the caller.
func (tv *TranslatedView[T, R]) Find(pred func(R) bool) (R, bool) {
	return tv.data.Find(pred)
}

// FindMany returns all transformed items matching the predicate.
//
// The predicate must not panic. If it does, the panic propagates to the caller.
func (tv *TranslatedView[T, R]) FindMany(pred func(R) bool) []R {
	return tv.data.FindMany(pred)
}

// Filter applies filter options to the transformed items.
func (tv *TranslatedView[T, R]) Filter(opts ...FilterOption[R]) []R {
	return tv.data.Filter(opts...)
}

// OnChange registers a callback that fires after the view recomputes.
// Returns a function that removes the hook when called.
func (tv *TranslatedView[T, R]) OnChange(fn func(old, new []R)) func() {
	return tv.data.OnChange(fn)
}

// Name returns the view name.
func (tv *TranslatedView[T, R]) Name() string {
	return tv.name
}

// Version returns the current snapshot version.
func (tv *TranslatedView[T, R]) Version() Version {
	return tv.data.Version()
}

// Close unsubscribes the view from its source collection. After Close,
// the view stops recomputing on source changes. It is safe to call
// Close multiple times.
func (tv *TranslatedView[T, R]) Close() {
	if tv.unsub != nil {
		tv.unsub()
		tv.unsub = nil
	}
}

func (tv *TranslatedView[T, R]) persistCtx() (context.Context, context.CancelFunc) {
	timeout := tv.persistenceTimeout
	if timeout == 0 {
		timeout = defaultPersistenceTimeout
	}

	return context.WithTimeout(context.Background(), timeout)
}

func (tv *TranslatedView[T, R]) persistAsync() {
	if tv.persistence == nil {
		return
	}

	items := tv.data.data.Load().items

	select {
	case tv.persistSem <- struct{}{}:
		go func() {
			defer func() { <-tv.persistSem }()
			tv.saveToPersistence(items)
		}()
	default:
		// Semaphore full — skip this save.
	}
}

func (tv *TranslatedView[T, R]) saveToPersistence(items []R) {
	data, err := json.Marshal(items)
	if err != nil {
		if tv.onError != nil {
			tv.onError(tv.name, fmt.Errorf("marshal translated view: %w", err))
		}
		return
	}

	ctx, cancel := tv.persistCtx()
	defer cancel()

	if err := tv.persistence.Save(ctx, tv.name, data); err != nil {
		if tv.onError != nil {
			tv.onError(tv.name, fmt.Errorf("save translated view: %w", err))
		}
	}
}

func (tv *TranslatedView[T, R]) loadFromPersistence() {
	ctx, cancel := tv.persistCtx()
	defer cancel()

	data, err := tv.persistence.Load(ctx, tv.name)
	if err != nil || len(data) == 0 {
		return
	}

	var items []R
	if err := json.Unmarshal(data, &items); err != nil {
		return
	}

	_ = tv.data.Swap(Version{}, items)
}

func transformSlice[T any, R any](items []T, fn func(T) R) []R {
	result := make([]R, len(items))
	for i, item := range items {
		result[i] = fn(item)
	}

	return result
}
