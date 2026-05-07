// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/stretchr/testify/require"
)

// TestHTTPHandlerRecoversFromHandlerPanic verifies panic recovery logs context and returns HTTP 500.
func TestHTTPHandlerRecoversFromHandlerPanic(t *testing.T) {
	t.Parallel()

	srv, err := NewServer(
		"ignored",
		func(http.ResponseWriter, *http.Request) {
			panic("boom")
		},
	)
	require.NoError(t, err)

	var logs bytes.Buffer
	ctx := logger.WithLogger(
		context.Background(),
		logger.NewLogger(
			logger.WithQuiet(),
			logger.WithFormat("text"),
			logger.WithWriter(&logs),
		),
	)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	require.NotPanics(t, func() {
		srv.httpHandler(ctx).ServeHTTP(recorder, request)
	})
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Contains(t, logs.String(), "panic=boom")
	require.Contains(t, logs.String(), "stack=")
}

// TestNewHTTPServerConfiguresTimeouts verifies the unix socket server installs defensive timeouts.
func TestNewHTTPServerConfiguresTimeouts(t *testing.T) {
	t.Parallel()

	srv, err := NewServer(
		"ignored",
		func(http.ResponseWriter, *http.Request) {},
	)
	require.NoError(t, err)

	httpServer := srv.newHTTPServer(context.Background())
	require.Equal(t, defaultTimeout, httpServer.ReadHeaderTimeout)
	require.Equal(t, idleTimeout, httpServer.IdleTimeout)
}

// TestWrapListenErrorMarksUnsupportedTransport verifies capability failures are
// exposed through ErrUnsupported.
func TestWrapListenErrorMarksUnsupportedTransport(t *testing.T) {
	t.Parallel()

	err := wrapListenError(unsupportedListenErrorForTest())

	require.ErrorIs(t, err, ErrUnsupported)
}

// TestWrapListenErrorPreservesOtherErrors verifies bind/path errors keep their
// original classification.
func TestWrapListenErrorPreservesOtherErrors(t *testing.T) {
	t.Parallel()

	original := errors.New("bind permission denied")
	err := wrapListenError(original)

	require.ErrorIs(t, err, original)
	require.NotErrorIs(t, err, ErrUnsupported)
}
