package directus_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	dlog "github.com/swchck/director/log"

	"github.com/swchck/director/directus"
)

func TestWithHTTPClient(t *testing.T) {
	customClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token", directus.WithHTTPClient(customClient))
	err := client.Delete(context.Background(), "/test")
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithLogger(t *testing.T) {
	logger := dlog.Nop()

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token", directus.WithLogger(logger))
	err := client.Delete(context.Background(), "/test")
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithHTTPClient_CustomTransport(t *testing.T) {
	var transportCalled bool

	customTransport := &roundTripperFunc{
		fn: func(req *http.Request) (*http.Response, error) {
			transportCalled = true
			return http.DefaultTransport.RoundTrip(req)
		},
	}

	customClient := &http.Client{
		Transport: customTransport,
	}

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	client := directus.NewClient(srv.URL, "token", directus.WithHTTPClient(customClient))
	_ = client.Delete(context.Background(), "/test")

	if !transportCalled {
		t.Error("custom transport was not used")
	}
}

type roundTripperFunc struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (r *roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.fn(req)
}
