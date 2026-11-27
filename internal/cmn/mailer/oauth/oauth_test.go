// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestCachedTokenFuncCoalescesConcurrentRefresh(t *testing.T) {
	t.Parallel()

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
		wg.Go(func() {
			got, err := token(context.Background())
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
