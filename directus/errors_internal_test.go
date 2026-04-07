package directus

import (
	"errors"
	"testing"
)

func TestStatusToSentinel(t *testing.T) {
	tests := []struct {
		code   int
		target error
	}{
		{400, ErrBadRequest},
		{401, ErrUnauthorized},
		{403, ErrForbidden},
		{404, ErrNotFound},
		{409, ErrConflict},
		{500, ErrInternal},
		{502, ErrInternal},
		{503, ErrInternal},
		{302, nil},
		{200, nil},
		{204, nil},
	}

	for _, tt := range tests {
		got := statusToSentinel(tt.code)
		if !errors.Is(got, tt.target) {
			t.Errorf("statusToSentinel(%d) = %v, want %v", tt.code, got, tt.target)
		}
	}
}
