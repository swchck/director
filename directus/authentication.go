package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// AuthResponse is returned by login and refresh operations.
type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	Expires      int    `json:"expires"`
	RefreshToken string `json:"refresh_token"`
}

// Login authenticates with email and password, returning tokens.
func (c *Client) Login(ctx context.Context, email, password string) (*AuthResponse, error) {
	raw, err := c.Post(ctx, "auth/login", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return nil, fmt.Errorf("directus: login: %w", err)
	}

	var resp AuthResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("directus: unmarshal login: %w", err)
	}

	return &resp, nil
}

// RefreshToken refreshes an access token using a refresh token.
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*AuthResponse, error) {
	raw, err := c.Post(ctx, "auth/refresh", map[string]string{
		"refresh_token": refreshToken,
		"mode":          "json",
	})
	if err != nil {
		return nil, fmt.Errorf("directus: refresh token: %w", err)
	}

	var resp AuthResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("directus: unmarshal refresh: %w", err)
	}

	return &resp, nil
}

// Logout invalidates the current refresh token.
func (c *Client) Logout(ctx context.Context, refreshToken string) error {
	_, err := c.Post(ctx, "auth/logout", map[string]string{
		"refresh_token": refreshToken,
	})
	if err != nil {
		return fmt.Errorf("directus: logout: %w", err)
	}

	return nil
}

// RequestPasswordReset sends a password reset email.
func (c *Client) RequestPasswordReset(ctx context.Context, email string) error {
	_, err := c.Post(ctx, "auth/password/request", map[string]string{
		"email": email,
	})
	if err != nil {
		return fmt.Errorf("directus: request password reset: %w", err)
	}

	return nil
}

// ResetPassword resets a password using a reset token.
func (c *Client) ResetPassword(ctx context.Context, token, password string) error {
	_, err := c.Post(ctx, "auth/password/reset", map[string]string{
		"token":    token,
		"password": password,
	})
	if err != nil {
		return fmt.Errorf("directus: reset password: %w", err)
	}

	return nil
}
