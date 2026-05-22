// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import "context"

type userContextKey struct{}
type apiPrincipalContextKey struct{}
type clientIPContextKey struct{}

// WithUser returns a new context that carries the provided user value.
func WithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

// UserFromContext retrieves the authenticated user from the context.
// It returns the user and true if a *User value is present for the package's userContextKey, or nil and false otherwise.
func UserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userContextKey{}).(*User)
	return user, ok
}

// WithAPIKey returns a new context that carries the provided API key value.
func WithAPIKey(ctx context.Context, key *APIKey) context.Context {
	return context.WithValue(ctx, apiPrincipalContextKey{}, key)
}

// APIKeyFromContext retrieves the API key used for authentication from context.
func APIKeyFromContext(ctx context.Context) (*APIKey, bool) {
	key, ok := ctx.Value(apiPrincipalContextKey{}).(*APIKey)
	return key, ok
}

// WithClientIP returns a new context that carries the client IP address.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPContextKey{}, ip)
}

// ClientIPFromContext retrieves the client IP address from the context.
// It returns the IP address and true if present, or empty string and false otherwise.
func ClientIPFromContext(ctx context.Context) (string, bool) {
	ip, ok := ctx.Value(clientIPContextKey{}).(string)
	return ip, ok
}
