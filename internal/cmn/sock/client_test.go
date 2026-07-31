// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/stretchr/testify/require"
)

func TestDialFail(t *testing.T) {
	f, err := os.CreateTemp("", "sock_client_dial_failure")
	require.NoError(t, err)
	defer func() {
		_ = os.Remove(f.Name())
	}()

	client := sock.NewClient(f.Name())
	_, err = client.Request("GET", "/status")
	require.Error(t, err)
}

func TestDialTimeout(t *testing.T) {
	f, err := os.CreateTemp("", "sock_client_test")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	defer func() {
		_ = os.Remove(f.Name())
	}()

	srv, err := sock.NewServer(
		f.Name(),
		func(w http.ResponseWriter, _ *http.Request) {
			// Simulate a very slow handler to trigger client timeout.
			time.Sleep(time.Second * 3100)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		},
	)
	require.NoError(t, err)

	listen := make(chan error, 1)
	go func() {
		_ = srv.Serve(context.Background(), listen)
	}()

	// Wait for the server to signal it is ready.
	require.NoError(t, <-listen)

	client := sock.NewClient(f.Name())
	_, err = client.Request("GET", "/status")
	require.Error(t, err)
	require.True(t, errors.Is(err, sock.ErrTimeout))
}
