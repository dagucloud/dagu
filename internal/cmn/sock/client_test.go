// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
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
	require.ErrorIs(t, err, sock.ErrTransport)
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

func TestRequestRejectsNonSuccessfulResponse(t *testing.T) {
	f, err := os.CreateTemp("", "sock_client_http_error")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	defer func() {
		_ = os.Remove(f.Name())
	}()

	srv, err := sock.NewServer(
		f.Name(),
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("not accepted" + strings.Repeat("x", 1024)))
		},
	)
	require.NoError(t, err)

	listen := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(context.Background(), listen)
	}()
	require.NoError(t, <-listen)

	body, err := sock.NewClient(f.Name()).Request(http.MethodPost, "/stop")
	require.ErrorIs(t, err, sock.ErrUnexpectedStatus)
	require.ErrorContains(t, err, "418 I'm a teapot")
	require.ErrorContains(t, err, "not accepted")
	require.Less(t, len(err.Error()), 512)
	require.Empty(t, body)

	require.NoError(t, srv.Shutdown(context.Background()))
	require.True(t, errors.Is(<-done, sock.ErrServerRequestedShutdown))
}
