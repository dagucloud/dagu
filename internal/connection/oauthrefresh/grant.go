// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package oauthrefresh refreshes OAuth access tokens with the refresh token
// grant and lets one caller at a time refresh a cached token.
package oauthrefresh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	tokenRequestTimeout = 15 * time.Second
	maxTokenResponse    = 1 << 20
)

var tokenHTTPClient = &http.Client{Timeout: tokenRequestTimeout}

// RefreshFunc returns a new token. cached is the token held so far, or nil
// when there is none.
type RefreshFunc func(ctx context.Context, cached *oauth2.Token) (*oauth2.Token, error)

// Grant is a refresh token grant against one token endpoint.
type Grant struct {
	TokenURL string
	ClientID string
	// ClientSecret is empty for a public client.
	ClientSecret string
	// RefreshToken is used until the endpoint returns a rotated one.
	RefreshToken string
	// Scopes are sent with the request when set. Microsoft requires them.
	Scopes []string
}

// TokenError is an OAuth error response from a token endpoint, such as
// invalid_grant for a revoked or expired refresh token.
type TokenError struct {
	Code        string
	Description string
}

func (e *TokenError) Error() string {
	if e.Description == "" {
		return "token endpoint returned " + e.Code
	}
	return fmt.Sprintf("token endpoint returned %s: %s", e.Code, e.Description)
}

// Refresh exchanges the cached token's refresh token, or g.RefreshToken when
// cached has none, for a new access token. The returned token keeps the
// refresh token it was exchanged with unless the endpoint rotated it. A JSON
// response body with an error field is a *TokenError. Any other non-200
// response, or a transport failure, is a plain error.
func (g Grant) Refresh(ctx context.Context, cached *oauth2.Token) (*oauth2.Token, error) {
	refreshToken := g.RefreshToken
	if cached != nil && cached.RefreshToken != "" {
		refreshToken = cached.RefreshToken
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {g.ClientID},
	}
	if g.ClientSecret != "" {
		form.Set("client_secret", g.ClientSecret)
	}
	if len(g.Scopes) > 0 {
		form.Set("scope", strings.Join(g.Scopes, " "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := tokenHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse))
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	var body struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}
	if body.Error != "" {
		description, _, _ := strings.Cut(strings.TrimSpace(body.ErrorDescription), "\n")
		return nil, &TokenError{Code: body.Error, Description: strings.TrimSpace(description)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}

	token := &oauth2.Token{
		AccessToken:  body.AccessToken,
		TokenType:    body.TokenType,
		RefreshToken: body.RefreshToken,
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	if body.ExpiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	return token, nil
}
