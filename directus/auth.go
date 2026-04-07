package directus

import "net/http"

// authTransport is an http.RoundTripper that injects a static Bearer token
// into every outgoing request.
type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)

	return t.base.RoundTrip(req)
}
