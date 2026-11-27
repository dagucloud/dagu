// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauthrefresh

import (
	"context"
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

func TestGateCoalescesConcurrentRefresh(t *testing.T) {
	t.Parallel()

	gate := NewGate()
	var refreshes atomic.Int32
	refresh := func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		refreshes.Add(1)
		time.Sleep(10 * time.Millisecond)
		return &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}, nil
	}

	const callers = 20
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			got, err := gate.Token(context.Background(), refresh)
			if err == nil && got.AccessToken != "token" {
				err = errors.New("unexpected token")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), refreshes.Load())
}

func TestGateReleasesTurnAfterCanceledRefresh(t *testing.T) {
	t.Parallel()

	gate := NewGate()
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := gate.Token(ctx, func(ctx context.Context, _ *oauth2.Token) (*oauth2.Token, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		firstDone <- err
	}()
	<-started
	cancel()
	assert.ErrorIs(t, <-firstDone, context.Canceled)

	token, err := gate.Token(context.Background(), func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "retry", Expiry: time.Now().Add(time.Hour)}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "retry", token.AccessToken)
}

func TestGateRefreshesExpiredToken(t *testing.T) {
	t.Parallel()

	gate := NewGate()
	var refreshes atomic.Int32
	refresh := func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		count := refreshes.Add(1)
		return &oauth2.Token{
			AccessToken: fmt.Sprintf("token-%d", count),
			Expiry:      time.Now().Add(time.Hour),
		}, nil
	}
	first, err := gate.Token(context.Background(), refresh)
	require.NoError(t, err)
	second, err := gate.Token(context.Background(), refresh)
	require.NoError(t, err)
	assert.Equal(t, first.AccessToken, second.AccessToken)
	assert.Equal(t, int32(1), refreshes.Load())

	gate.set(&oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)})
	third, err := gate.Token(context.Background(), refresh)
	require.NoError(t, err)
	assert.Equal(t, "token-2", third.AccessToken)
	assert.Equal(t, int32(2), refreshes.Load())
}

func TestGateWaiterCanCancel(t *testing.T) {
	t.Parallel()

	gate := NewGate()
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := gate.Token(context.Background(), func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
			close(started)
			<-release
			return &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}, nil
		})
		firstDone <- err
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := gate.Token(ctx, func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		return nil, errors.New("unexpected refresh")
	})
	assert.ErrorIs(t, err, context.Canceled)
	close(release)
	require.NoError(t, <-firstDone)
}
