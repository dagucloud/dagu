// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package terminal_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	terminalpkg "github.com/dagucloud/dagu/v2/internal/service/frontend/terminal"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminal_SessionLimitReturnsHTTP429(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY-backed terminal sessions are unsupported on Windows")
	}
	server, token := setupTerminalServer(t, 1)
	conn := mustDialTerminal(t, server, token)
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test complete") })
	waitForTerminalSessionReady(t, conn)

	require.Eventually(t, func() bool {
		secondConn, resp, err := dialTerminal(server, token)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if secondConn != nil {
			_ = secondConn.CloseNow()
		}
		if err == nil || resp == nil {
			return false
		}
		return resp.StatusCode == http.StatusTooManyRequests
	}, terminalTestTimeout(5*time.Second), 100*time.Millisecond, "terminal session limit was not enforced")
}

func TestTerminal_CleanClientCloseReleasesSession(t *testing.T) {
	server, token := setupTerminalServer(t, 1)
	conn := mustDialTerminal(t, server, token)
	closeTerminalConn(t, conn)

	waitForTerminalSlot(t, server, token)
}

func TestTerminal_AbruptDisconnectReleasesSession(t *testing.T) {
	server, token := setupTerminalServer(t, 1)
	conn := mustDialTerminal(t, server, token)
	require.NoError(t, conn.CloseNow())

	waitForTerminalSlot(t, server, token)
}

func TestTerminal_ShellExitClosesCleanly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY/WebSocket close timing is unreliable on Windows runners")
	}
	server, token := setupTerminalServer(t, 1)
	conn := mustDialTerminal(t, server, token)

	sendInput(t, conn, "exit\r\n")
	output, errorMessages := readTerminalUntilClose(t, conn)

	assert.Contains(t, output, "Shell closed.")
	for _, msg := range errorMessages {
		assert.NotContains(t, strings.ToLower(msg), "i/o timeout")
	}
}

func TestTerminal_ServerShutdownDoesNotEmitTimeoutError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY/WebSocket close timing is unreliable on Windows runners")
	}
	server, token := setupTerminalServer(t, 1)
	conn := mustDialTerminal(t, server, token)

	server.Cancel()
	_, errorMessages := readTerminalUntilClose(t, conn)

	for _, msg := range errorMessages {
		assert.NotContains(t, strings.ToLower(msg), "i/o timeout")
	}
}

func setupTerminalServer(t *testing.T, maxSessions int) (test.Server, string) {
	t.Helper()

	shellPath := ""
	if runtime.GOOS == "windows" {
		shellPath = windowsTerminalShell(t)
		t.Setenv("SHELL", shellPath)
	}

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		if shellPath != "" {
			cfg.Core.DefaultShell = shellPath
		}
		cfg.Server.Auth.Mode = config.AuthModeBuiltin
		cfg.Server.Auth.Builtin.Token.Secret = "test-jwt-secret-key-terminal"
		cfg.Server.Auth.Builtin.Token.TTL = time.Hour
		cfg.Server.Terminal.Enabled = true
		cfg.Server.Terminal.MaxSessions = maxSessions
	}))

	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	resp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	var result api.LoginResponse
	resp.Unmarshal(t, &result)
	require.NotEmpty(t, result.Token)
	return server, result.Token
}

func windowsTerminalShell(t *testing.T) string {
	t.Helper()

	cmdPath, err := exec.LookPath("cmd")
	require.NoError(t, err)

	wrapperPath := filepath.Join(t.TempDir(), "terminal-shell.cmd")
	wrapper := fmt.Sprintf("@echo off\r\n\"%s\" /Q /K\r\n", cmdPath)
	require.NoError(t, os.WriteFile(wrapperPath, []byte(wrapper), 0600))
	return wrapperPath
}

func mustDialTerminal(t *testing.T, server test.Server, token string) *websocket.Conn {
	t.Helper()

	conn, resp, err := dialTerminal(server, token)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.NoError(t, err)
	return conn
}

func dialTerminal(server test.Server, token string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf(
		"ws://%s:%d/api/v1/terminal/ws?token=%s",
		server.Config.Server.Host,
		server.Config.Server.Port,
		url.QueryEscape(token),
	)
	return websocket.Dial(ctx, wsURL, nil)
}

func waitForTerminalSlot(t *testing.T, server test.Server, token string) {
	t.Helper()

	require.Eventually(t, func() bool {
		conn, resp, err := dialTerminal(server, token)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			_ = conn.Close(websocket.StatusNormalClosure, "verification complete")
			return true
		}
		if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("unexpected dial failure while waiting for slot release: %v", err)
			return true
		}
		return false
	}, terminalTestTimeout(5*time.Second), 50*time.Millisecond, "terminal slot was not released within timeout")
}

func waitForTerminalSessionReady(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	sendInput(t, conn, "\r\n")

	ctx, cancel := context.WithTimeout(context.Background(), terminalTestTimeout(5*time.Second))
	defer cancel()

	for {
		_, data, err := conn.Read(ctx)
		require.NoError(t, err, "terminal session did not become ready")

		var msg terminalpkg.Message
		require.NoError(t, json.Unmarshal(data, &msg))

		switch msg.Type {
		case terminalpkg.MessageTypeOutput:
			decoded, err := msg.DecodeData()
			require.NoError(t, err)
			if len(decoded) > 0 {
				return
			}
		case terminalpkg.MessageTypeError:
			require.Failf(t, "terminal session emitted error while starting", "%s", msg.Data)
		case terminalpkg.MessageTypeInput, terminalpkg.MessageTypeResize, terminalpkg.MessageTypeClose:
			// Ignore client-originated frames if they appear in the stream unexpectedly.
		}
	}
}

func sendInput(t *testing.T, conn *websocket.Conn, input string) {
	t.Helper()

	payload, err := json.Marshal(terminalpkg.Message{
		Type: terminalpkg.MessageTypeInput,
		Data: base64.StdEncoding.EncodeToString([]byte(input)),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, payload))
}

func readTerminalUntilClose(t *testing.T, conn *websocket.Conn) (string, []string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), terminalReadTimeout())
	defer cancel()

	var (
		output        strings.Builder
		errorMessages []string
	)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			require.False(t, errors.Is(ctx.Err(), context.DeadlineExceeded), "terminal connection did not close before timeout")
			return output.String(), errorMessages
		}

		var msg terminalpkg.Message
		require.NoError(t, json.Unmarshal(data, &msg))

		switch msg.Type {
		case terminalpkg.MessageTypeOutput:
			decoded, err := msg.DecodeData()
			require.NoError(t, err)
			output.Write(decoded)
		case terminalpkg.MessageTypeError:
			errorMessages = append(errorMessages, msg.Data)
		case terminalpkg.MessageTypeInput, terminalpkg.MessageTypeResize, terminalpkg.MessageTypeClose:
			t.Fatalf("unexpected server message type: %s", msg.Type)
		}
	}
}

func terminalTestTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 6
	}
	return base
}

func terminalReadTimeout() time.Duration {
	return terminalTestTimeout(10 * time.Second)
}

func closeTerminalConn(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	err := conn.Close(websocket.StatusNormalClosure, "client closed")
	if runtime.GOOS == "windows" && errors.Is(err, context.DeadlineExceeded) {
		return
	}
	require.NoError(t, err)
}
