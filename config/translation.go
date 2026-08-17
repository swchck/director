package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// FindTranslation returns the first translation whose langFn value equals targetLang. langFn
// is a parameter so translation structs need not implement any interface of ours.
func FindTranslation[T any](translations []T, langFn func(T) string, targetLang string) (T, bool) {
	for _, tr := range translations {
		if langFn(tr) == targetLang {
			return tr, true
		}
	}

	var zero T
	return zero, false
}

// FindTranslationWithFallback returns the translation for targetLang, else the first
// matching fallback in order, else false.
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

// TranslationMap keys a translations slice by language code.
func TranslationMap[T any](translations []T, langFn func(T) string) map[string]T {
	m := make(map[string]T, len(translations))
	for _, tr := range translations {
		m[langFn(tr)] = tr
	}

	return m
}

// TranslatedView transforms each Collection item into another type — one view per language when
// flattening translations. Unlike its siblings it is backed by a derived Collection[R].
type TranslatedView[T any, R any] struct {
	name               string
	source             *Collection[T]
	transform          func(T) R
	unsub              func()
	closeOnce          sync.Once
	persistence        ViewPersistence
	persistenceTimeout time.Duration
	onError            ErrorFunc
	persistSem         chan struct{}

	data *Collection[R]
}

// TranslatedViewOption configures optional TranslatedView behavior.
type TranslatedViewOption[T any, R any] func(*TranslatedView[T, R])

// WithTranslatedViewPersistence enables external persistence for the translated view.
// See ViewPersistence.
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

// NewTranslatedView creates a view calling transform for every source item on every update.
// To filter without transforming, use NewView instead.
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
	}

	// Swap errors are discarded here and below: they only report a panic in a consumer's
	// OnChange hook, which this view cannot surface — such panics never reach onError.
	if !keepWarmStart(tv.loadFromPersistence(), source.Version()) {
		_ = tv.data.Swap(source.Version(), transformSlice(source.All(), transform))

		tv.persistAsync()
	}

	tv.unsub = source.OnChange(func(_, newItems []T) {
		transformed := transformSlice(newItems, transform)
		_ = tv.data.Swap(source.Version(), transformed)

		tv.persistAsync()
	})

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
func (tv *TranslatedView[T, R]) Find(pred func(R) bool) (R, bool) {
	return tv.data.Find(pred)
}

// FindMany returns all transformed items matching the predicate.
func (tv *TranslatedView[T, R]) FindMany(pred func(R) bool) []R {
	return tv.data.FindMany(pred)
}

// Filter applies filter options to the transformed items.
func (tv *TranslatedView[T, R]) Filter(opts ...FilterOption[R]) []R {
	return tv.data.Filter(opts...)
}

// OnChange registers a callback fired after each recompute, on the derived Collection[R]; a
// panic in it is discarded, since this view has no Swap error to surface it through.
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

// Close unsubscribes the view from its source; idempotent. Reads stay valid but go stale.
func (tv *TranslatedView[T, R]) Close() {
	tv.closeOnce.Do(func() {
		if tv.unsub != nil {
			tv.unsub()
		}
	})
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

// loadFromPersistence installs a stored snapshot and reports whether it did.
func (tv *TranslatedView[T, R]) loadFromPersistence() bool {
	if tv.persistence == nil {
		return false
	}

	ctx, cancel := tv.persistCtx()
	defer cancel()

	data, err := tv.persistence.Load(ctx, tv.name)
	if err != nil || len(data) == 0 {
		return false
	}

	var items []R
	if err := json.Unmarshal(data, &items); err != nil || items == nil {
		return false
	}

	_ = tv.data.Swap(Version{}, items)

	return true
}

func transformSlice[T any, R any](items []T, fn func(T) R) []R {
	result := make([]R, len(items))
	for i, item := range items {
		result[i] = fn(item)
	}

	return result
}
