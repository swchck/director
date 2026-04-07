package directus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	dlog "github.com/swchck/director/log"
)

func TestUnwrapData_WithEnvelopeErrors(t *testing.T) {
	c := &Client{logger: dlog.Nop()}

	body := `{"data": null, "errors": [{"message": "validation failed"}]}`
	_, err := c.unwrapData([]byte(body))
	if err == nil {
		t.Fatal("expected error")
	}

	var re *ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("expected *ResponseError, got %T", err)
	}

	if len(re.Errors) != 1 || re.Errors[0].Message != "validation failed" {
		t.Errorf("errors = %+v", re.Errors)
	}
}

func TestUnwrapData_InvalidJSON(t *testing.T) {
	c := &Client{logger: dlog.Nop()}

	_, err := c.unwrapData([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUnwrapData_ValidData(t *testing.T) {
	c := &Client{logger: dlog.Nop()}

	raw, err := c.unwrapData([]byte(`{"data": {"id": 1}}`))
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(raw, &result)
	if result["id"] != float64(1) {
		t.Errorf("data = %v", result)
	}
}

func TestParseError_InvalidBody(t *testing.T) {
	c := &Client{logger: dlog.Nop()}

	err := c.parseError(500, []byte(`not json`))
	var re *ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("expected *ResponseError, got %T", err)
	}

	if re.StatusCode != 500 {
		t.Errorf("StatusCode = %d", re.StatusCode)
	}

	if len(re.Errors) != 0 {
		t.Errorf("expected no errors parsed from invalid body, got %+v", re.Errors)
	}
}

func TestDo_MarshalBodyError(t *testing.T) {
	c := &Client{
		baseURL: "http://localhost",
		logger:  dlog.Nop(),
		httpClient: &http.Client{
			Transport: &authTransport{token: "t", base: http.DefaultTransport},
		},
	}

	// json.Marshal can't serialize channels
	_, err := c.do(context.Background(), "POST", "/test", nil, make(chan int))
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestDo_ResponseWithEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		// Write empty body
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	raw, err := c.do(context.Background(), "GET", "/test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if raw != nil {
		t.Errorf("expected nil for empty 200 body, got %v", raw)
	}
}

func TestDo_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := c.do(ctx, "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
