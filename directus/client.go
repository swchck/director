package directus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	dlog "github.com/swchck/director/log"
)

// Client is a low-level HTTP client for the Directus REST API.
// It handles authentication, request building, response unwrapping, and error mapping.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     dlog.Logger
}

// NewClient creates a new Directus REST client.
//
// baseURL is the root URL of the Directus instance (e.g. "https://directus.example.com").
// token is a static access token used for authentication.
func NewClient(baseURL, token string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: http.DefaultClient,
		logger:     dlog.Nop(),
	}

	for _, opt := range opts {
		opt(c)
	}

	// Wrap the transport with auth.
	transport := c.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	c.httpClient = &http.Client{
		Transport:     &authTransport{token: c.token, base: transport},
		Timeout:       c.httpClient.Timeout,
		CheckRedirect: c.httpClient.CheckRedirect,
		Jar:           c.httpClient.Jar,
	}

	return c
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

// Get performs a GET request and returns the unwrapped "data" field.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, path, query, nil)
}

// Post performs a POST request and returns the unwrapped "data" field.
func (c *Client) Post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPost, path, nil, body)
}

// Patch performs a PATCH request and returns the unwrapped "data" field.
func (c *Client) Patch(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPatch, path, nil, body)
}

func (c *Client) Delete(ctx context.Context, path string) error {
	_, err := c.do(ctx, http.MethodDelete, path, nil, nil)
	return err
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	u := c.baseURL + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("directus: marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("directus: create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.logger.Debug("directus request", dlog.String("method", method), dlog.String("url", u))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("directus: execute request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("directus: read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.parseError(resp.StatusCode, respBody)
	}

	// DELETE with 204 has no body.
	if resp.StatusCode == http.StatusNoContent || len(respBody) == 0 {
		return nil, nil
	}

	return c.unwrapData(respBody)
}

// envelope represents the standard Directus response wrapper.
type envelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []APIError      `json:"errors,omitempty"`
}

func (c *Client) unwrapData(body []byte) (json.RawMessage, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("directus: unmarshal response: %w", err)
	}

	if len(env.Errors) > 0 {
		return nil, &ResponseError{StatusCode: http.StatusOK, Errors: env.Errors}
	}

	return env.Data, nil
}

func (c *Client) parseError(statusCode int, body []byte) error {
	re := &ResponseError{StatusCode: statusCode}

	var env struct {
		Errors []APIError `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err == nil && len(env.Errors) > 0 {
		re.Errors = env.Errors
	}

	return re
}
