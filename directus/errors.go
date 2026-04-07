package directus

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for Directus API responses.
var (
	ErrNotFound     = errors.New("directus: not found")
	ErrUnauthorized = errors.New("directus: unauthorized")
	ErrForbidden    = errors.New("directus: forbidden")
	ErrBadRequest   = errors.New("directus: bad request")
	ErrConflict     = errors.New("directus: conflict")
	ErrInternal     = errors.New("directus: internal server error")
)

// APIError represents a single error entry from a Directus response.
type APIError struct {
	Message    string         `json:"message"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// ResponseError is returned when the Directus API responds with a non-2xx status.
type ResponseError struct {
	StatusCode int
	Errors     []APIError
}

func (e *ResponseError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("directus: HTTP %d", e.StatusCode)
	}

	if len(e.Errors) == 1 {
		return fmt.Sprintf("directus: HTTP %d: %s", e.StatusCode, e.Errors[0].Message)
	}

	msgs := make([]string, len(e.Errors))
	for i, ae := range e.Errors {
		msgs[i] = ae.Message
	}

	return fmt.Sprintf("directus: HTTP %d: %s", e.StatusCode, strings.Join(msgs, "; "))
}

// Unwrap returns the corresponding sentinel error based on HTTP status code.
func (e *ResponseError) Unwrap() error {
	switch e.StatusCode {
	case 400:
		return ErrBadRequest
	case 401:
		return ErrUnauthorized
	case 403:
		return ErrForbidden
	case 404:
		return ErrNotFound
	case 409:
		return ErrConflict
	default:
		if e.StatusCode >= 500 {
			return ErrInternal
		}
		return nil
	}
}

// statusToSentinel maps an HTTP status code to a sentinel error.
// Returns nil for unrecognized codes.
func statusToSentinel(code int) error {
	switch code {
	case 400:
		return ErrBadRequest
	case 401:
		return ErrUnauthorized
	case 403:
		return ErrForbidden
	case 404:
		return ErrNotFound
	case 409:
		return ErrConflict
	default:
		if code >= 500 {
			return ErrInternal
		}
		return nil
	}
}
