// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/stretchr/testify/require"
)

// TestMain installs an isolated HOME directory for socket integration tests.
func TestMain(m *testing.M) {
	testHomeDir, err := os.MkdirTemp("", "controller_test")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", testHomeDir)
	code := m.Run()
	_ = os.RemoveAll(testHomeDir)
	os.Exit(code)
}

// TestStartAndShutdownServer verifies the server accepts requests and shuts down cleanly.
func TestStartAndShutdownServer(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_server_start_shutdown")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	unixServer, err := sock.NewServer(
		tmpFile.Name(),
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		},
	)
	require.NoError(t, err)

	client := sock.NewClient(tmpFile.Name())
	listen := make(chan error, 1)
	done := make(chan error, 1)

	go func() {
		done <- unixServer.Serve(context.Background(), listen)
	}()

	// Wait for the server to signal it is ready.
	require.NoError(t, <-listen)

	ret, err := client.Request(http.MethodPost, "/")
	require.NoError(t, err)
	require.Equal(t, "OK", ret)

	_ = unixServer.Shutdown(context.Background())

	// Wait for the server to finish shutting down.
	require.Eventually(t, func() bool {
		_, err := client.Request(http.MethodPost, "/")
		return err != nil
	}, 5*time.Second, 10*time.Millisecond)
	require.True(t, errors.Is(<-done, sock.ErrServerRequestedShutdown))
}

// TestHeaderOnlyResponse verifies responses without a body are preserved over the unix socket transport.
func TestHeaderOnlyResponse(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_error_response")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	unixServer, err := sock.NewServer(
		tmpFile.Name(),
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
	)
	require.NoError(t, err)

	listen := make(chan error, 1)
	done := make(chan error, 1)

	go func() {
		done <- unixServer.Serve(context.Background(), listen)
	}()

	// Wait for the server to signal it is ready.
	require.NoError(t, <-listen)

	conn, err := net.DialTimeout("unix", tmpFile.Name(), 3*time.Second)
	require.NoError(t, err)
	defer func() {
		_ = conn.Close()
	}()
	require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))

	request, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	require.NoError(t, request.Write(conn))

	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	require.NoError(t, err)
	defer func() {
		_ = response.Body.Close()
	}()

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	require.Empty(t, body)
	require.NoError(t, unixServer.Shutdown(context.Background()))
	require.True(t, errors.Is(<-done, sock.ErrServerRequestedShutdown))
}

// TestEmptyResponse verifies empty HTTP responses round-trip without synthetic data.
func TestEmptyResponse(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_error_response")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	unixServer, err := sock.NewServer(
		tmpFile.Name(),
		func(_ http.ResponseWriter, _ *http.Request) {},
	)
	require.NoError(t, err)

	client := sock.NewClient(tmpFile.Name())
	listen := make(chan error, 1)
	done := make(chan error, 1)

	go func() {
		done <- unixServer.Serve(context.Background(), listen)
	}()

	// Wait for the server to signal it is ready.
	require.NoError(t, <-listen)

	body, err := client.Request(http.MethodGet, "/")
	require.NoError(t, err)
	require.Empty(t, body)
	require.NoError(t, unixServer.Shutdown(context.Background()))
	require.True(t, errors.Is(<-done, sock.ErrServerRequestedShutdown))
}

// TestShutdownWhileServerStarts verifies shutdown wins even if serving has only just started.
func TestShutdownWhileServerStarts(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_shutdown_while_server_starts")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	unixServer, err := sock.NewServer(
		tmpFile.Name(),
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- unixServer.Serve(context.Background(), nil)
	}()

	require.NoError(t, unixServer.Shutdown(context.Background()))

	select {
	case err := <-done:
		require.True(t, errors.Is(err, sock.ErrServerRequestedShutdown))
	case <-time.After(func() time.Duration {
		if runtime.GOOS == "windows" {
			return 5 * time.Second
		}
		return time.Second
	}()):
		t.Fatal("timed out waiting for socket server to stop")
	}
}

// TestMultipleWritesProduceSingleResponseBody verifies multiple handler writes are concatenated once.
func TestMultipleWritesProduceSingleResponseBody(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_server_multiple_writes")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	unixServer, err := sock.NewServer(
		tmpFile.Name(),
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("O"))
			_, _ = w.Write([]byte("K"))
		},
	)
	require.NoError(t, err)

	client := sock.NewClient(tmpFile.Name())
	listen := make(chan error, 1)
	done := make(chan error, 1)

	go func() {
		done <- unixServer.Serve(context.Background(), listen)
	}()

	require.NoError(t, <-listen)

	body, err := client.Request(http.MethodGet, "/")
	require.NoError(t, err)
	require.Equal(t, "OK", body)

	require.NoError(t, unixServer.Shutdown(context.Background()))
	require.True(t, errors.Is(<-done, sock.ErrServerRequestedShutdown))
}

// TestResponseHeadersAreReturnedToClient verifies response headers survive the unix socket transport.
func TestResponseHeadersAreReturnedToClient(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_server_response_headers")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	unixServer, err := sock.NewServer(
		tmpFile.Name(),
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Dagu-Test", "present")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("accepted"))
		},
	)
	require.NoError(t, err)

	listen := make(chan error, 1)
	done := make(chan error, 1)

	go func() {
		done <- unixServer.Serve(context.Background(), listen)
	}()

	require.NoError(t, <-listen)

	conn, err := net.DialTimeout("unix", tmpFile.Name(), 3*time.Second)
	require.NoError(t, err)
	defer func() {
		_ = conn.Close()
	}()
	require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))

	request, err := http.NewRequest(http.MethodGet, "/headers", nil)
	require.NoError(t, err)
	require.NoError(t, request.Write(conn))

	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	require.NoError(t, err)
	defer func() {
		_ = response.Body.Close()
	}()

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	require.Equal(t, "present", response.Header.Get("X-Dagu-Test"))
	require.Equal(t, "accepted", string(body))

	require.NoError(t, unixServer.Shutdown(context.Background()))
	require.True(t, errors.Is(<-done, sock.ErrServerRequestedShutdown))
}

// TestShutdownWaitsForActiveHandlers verifies graceful shutdown waits for in-flight handlers to finish.
func TestShutdownWaitsForActiveHandlers(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_server_shutdown_waits_for_handlers")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})

	unixServer, err := sock.NewServer(
		tmpFile.Name(),
		func(w http.ResponseWriter, _ *http.Request) {
			close(handlerStarted)
			<-releaseHandler
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		},
	)
	require.NoError(t, err)

	client := sock.NewClient(tmpFile.Name())
	listen := make(chan error, 1)
	done := make(chan error, 1)
	requestDone := make(chan error, 1)
	shutdownDone := make(chan error, 1)

	go func() {
		done <- unixServer.Serve(context.Background(), listen)
	}()

	require.NoError(t, <-listen)

	go func() {
		_, err := client.Request(http.MethodGet, "/")
		requestDone <- err
	}()

	<-handlerStarted

	go func() {
		shutdownDone <- unixServer.Shutdown(context.Background())
	}()

	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before the active handler finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseHandler)
	require.NoError(t, <-requestDone)
	require.NoError(t, <-shutdownDone)
	require.True(t, errors.Is(<-done, sock.ErrServerRequestedShutdown))
}
