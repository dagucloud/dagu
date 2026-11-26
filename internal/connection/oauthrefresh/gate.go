// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauthrefresh

import (
	"context"
	"errors"
	"strings"
	"sync"

	"golang.org/x/oauth2"
)

// Gate holds one cached token and lets one caller at a time refresh it.
// Use NewGate; the zero value is not usable.
type Gate struct {
	mu    sync.Mutex
	token *oauth2.Token
	turn  chan struct{}
}

// NewGate returns a Gate with no cached token.
func NewGate() *Gate {
	turn := make(chan struct{}, 1)
	turn <- struct{}{}
	return &Gate{turn: turn}
}

// Token returns the cached token while it is valid. Otherwise it waits its
// turn, calls refresh unless another caller cached a valid token meanwhile,
// and caches the result. It returns ctx's error if ctx ends while it waits.
func (g *Gate) Token(ctx context.Context, refresh RefreshFunc) (*oauth2.Token, error) {
	return g.TokenIf(ctx, (*oauth2.Token).Valid, refresh)
}

// TokenIf is Token with usable in place of the token's validity: it returns
// the cached token while usable reports true for it. usable receives nil
// when no token is cached.
func (g *Gate) TokenIf(ctx context.Context, usable func(*oauth2.Token) bool, refresh RefreshFunc) (*oauth2.Token, error) {
	if token := g.current(); usable(token) {
		return token, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.turn:
	}
	defer func() { g.turn <- struct{}{} }()

	cached := g.current()
	if usable(cached) {
		return cached, nil
	}
	token, err := refresh(ctx, cached)
	if err != nil {
		return nil, err
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.New("OAuth provider returned an empty access token")
	}
	g.set(token)
	return token, nil
}

// Cached returns the cached token, or nil, without waiting for a refresh
// in flight.
func (g *Gate) Cached() *oauth2.Token {
	return g.current()
}

func (g *Gate) current() *oauth2.Token {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.token
}

func (g *Gate) set(token *oauth2.Token) {
	g.mu.Lock()
	g.token = token
	g.mu.Unlock()
}
