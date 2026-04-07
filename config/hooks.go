package config

import (
	"errors"
	"fmt"
)

// safeCallHook executes a single function and recovers any panic, returning it as an error.
func safeCallHook(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("config: OnChange hook panicked: %v", r)
		}
	}()

	fn()

	return nil
}

// safeCallHooks executes all provided functions, recovering panics from each individually.
// All hooks run regardless of whether earlier hooks panic. Returns a joined error
// containing all panic errors, or nil if no hooks panicked.
func safeCallHooks(hooks ...func()) error {
	var errs []error
	for _, fn := range hooks {
		if fn == nil {
			continue
		}

		if err := safeCallHook(fn); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
