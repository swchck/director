package directus

import (
	"net/http"

	dlog "github.com/swchck/director/log"
)

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom http.Client.
// The auth transport will wrap the client's existing transport.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = hc
	}
}

func WithLogger(logger dlog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}
