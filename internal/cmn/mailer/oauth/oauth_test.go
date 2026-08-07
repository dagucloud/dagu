// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestValidateStructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "Microsoft",
			config: Config{
				Provider: ProviderMicrosoft, TenantID: "tenant", ClientID: "client", ClientSecret: "secret",
			},
		},
		{
			name: "GoogleServiceAccount",
			config: Config{
				Provider: ProviderGoogleServiceAccount, ServiceAccountJSON: "{}",
			},
		},
		{
			name: "GoogleRefresh",
			config: Config{
				Provider: ProviderGoogleRefresh, ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, ValidateStructure(&tt.config))
		})
	}

	assert.Error(t, ValidateStructure(&Config{Provider: ProviderMicrosoft}))
	assert.Error(t, ValidateStructure(&Config{
		Provider: ProviderMicrosoft, TenantID: "tenant", ClientID: "client", ClientSecret: "secret", RefreshToken: "mixed",
	}))
	assert.Error(t, ValidateStructure(&Config{Provider: "other"}))
}

func TestCachedTokenFuncCoalescesConcurrentRefresh(t *testing.T) {
	var refreshes atomic.Int32
	refresh := func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		refreshes.Add(1)
		time.Sleep(10 * time.Millisecond)
		return &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}, nil
	}
	token := cachedTokenFunc(sha256.Sum256([]byte(t.Name())), refresh)

	const callers = 20
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := token(context.Background())
			if err == nil && got.AccessToken != "token" {
				err = errors.New("unexpected token")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), refreshes.Load())
}

func TestTokenStateReleasesGateAfterCanceledRefresh(t *testing.T) {
	t.Parallel()

	state := newTokenState()
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := state.get(ctx, func(ctx context.Context, _ *oauth2.Token) (*oauth2.Token, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		firstDone <- err
	}()
	<-started
	cancel()
	assert.ErrorIs(t, <-firstDone, context.Canceled)

	token, err := state.get(context.Background(), func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "retry", Expiry: time.Now().Add(time.Hour)}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "retry", token.AccessToken)
}

func TestTokenStateRefreshesExpiredToken(t *testing.T) {
	t.Parallel()

	state := newTokenState()
	var refreshes atomic.Int32
	refresh := func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		count := refreshes.Add(1)
		return &oauth2.Token{
			AccessToken: fmt.Sprintf("token-%d", count),
			Expiry:      time.Now().Add(time.Hour),
		}, nil
	}
	first, err := state.get(context.Background(), refresh)
	require.NoError(t, err)
	second, err := state.get(context.Background(), refresh)
	require.NoError(t, err)
	assert.Equal(t, first.AccessToken, second.AccessToken)
	assert.Equal(t, int32(1), refreshes.Load())

	state.set(&oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)})
	third, err := state.get(context.Background(), refresh)
	require.NoError(t, err)
	assert.Equal(t, "token-2", third.AccessToken)
	assert.Equal(t, int32(2), refreshes.Load())
}

func TestTokenStateWaiterCanCancel(t *testing.T) {
	t.Parallel()

	state := newTokenState()
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := state.get(context.Background(), func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
			close(started)
			<-release
			return &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}, nil
		})
		firstDone <- err
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := state.get(ctx, func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		return nil, errors.New("unexpected refresh")
	})
	assert.ErrorIs(t, err, context.Canceled)
	close(release)
	require.NoError(t, <-firstDone)
}
