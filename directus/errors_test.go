package directus_test

import (
	"errors"
	"testing"

	"github.com/swchck/director/directus"
)

func TestResponseError_Error_SingleMessage(t *testing.T) {
	re := &directus.ResponseError{
		StatusCode: 404,
		Errors:     []directus.APIError{{Message: "not found"}},
	}

	got := re.Error()
	if got != "directus: HTTP 404: not found" {
		t.Errorf("Error() = %q", got)
	}
}

func TestResponseError_Error_MultipleMessages(t *testing.T) {
	re := &directus.ResponseError{
		StatusCode: 400,
		Errors: []directus.APIError{
			{Message: "field required"},
			{Message: "invalid format"},
		},
	}

	got := re.Error()
	if got != "directus: HTTP 400: field required; invalid format" {
		t.Errorf("Error() = %q", got)
	}
}

func TestResponseError_Error_NoMessages(t *testing.T) {
	re := &directus.ResponseError{StatusCode: 500}
	if got := re.Error(); got != "directus: HTTP 500" {
		t.Errorf("Error() = %q", got)
	}
}

func TestResponseError_Unwrap(t *testing.T) {
	tests := []struct {
		code   int
		target error
	}{
		{400, directus.ErrBadRequest},
		{401, directus.ErrUnauthorized},
		{403, directus.ErrForbidden},
		{404, directus.ErrNotFound},
		{409, directus.ErrConflict},
		{500, directus.ErrInternal},
		{502, directus.ErrInternal},
	}

	for _, tt := range tests {
		re := &directus.ResponseError{StatusCode: tt.code}
		if !errors.Is(re, tt.target) {
			t.Errorf("HTTP %d: errors.Is = false, want true for %v", tt.code, tt.target)
		}
	}
}

func TestResponseError_Unwrap_UnknownCode(t *testing.T) {
	re := &directus.ResponseError{StatusCode: 302}
	if errors.Is(re, directus.ErrNotFound) {
		t.Error("302 should not match any sentinel")
	}
}
