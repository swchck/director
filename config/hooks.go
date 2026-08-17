package config

import (
	"errors"
	"fmt"
)

func safeCallHook(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("config: OnChange hook panicked: %v", r)
		}
	}()

	fn()

	return nil
}

// safeCallHooks runs every hook even if an earlier one panicked, so one bad consumer
// callback cannot stop the rest of the fan-out. Panics come back joined.
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
