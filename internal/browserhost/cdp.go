// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package browserhost manages Chrome processes that browser steps keep alive
// between step executions: their durable records, reachability probes, and
// cleanup.
package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// StagehandExtensionName is the name the Stagehand runtime extension reports
// to Chrome.
const StagehandExtensionName = "Stagehand Runtime"

const (
	probeTimeout      = 3 * time.Second
	exitTimeout       = 5 * time.Second
	exitPollInterval  = 100 * time.Millisecond
	cdpReadLimitBytes = 16 << 20
)

// ErrExtensionNotFound reports that the connected browser has no enabled
// Stagehand runtime extension.
var ErrExtensionNotFound = errors.New("stagehand runtime extension is not loaded")

// Extension describes an unpacked extension loaded into a browser.
type Extension struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

// Probe reports whether a browser answers on its DevTools endpoint.
func Probe(ctx context.Context, cdpURL string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, err := browserWebSocketURL(ctx, cdpURL)
	return err
}

// StagehandExtension returns the enabled Stagehand runtime extension loaded
// into the browser at cdpURL.
func StagehandExtension(ctx context.Context, cdpURL string) (Extension, error) {
	var response struct {
		Extensions []Extension `json:"extensions"`
	}
	if err := call(ctx, cdpURL, "Extensions.getExtensions", map[string]any{}, &response); err != nil {
		return Extension{}, err
	}
	for _, extension := range response.Extensions {
		if extension.Name == StagehandExtensionName && extension.Enabled {
			return extension, nil
		}
	}
	return Extension{}, ErrExtensionNotFound
}

// BrowserProcessID returns the process ID of the browser at cdpURL.
func BrowserProcessID(ctx context.Context, cdpURL string) (int, error) {
	var response struct {
		ProcessInfo []struct {
			Type string `json:"type"`
			ID   int    `json:"id"`
		} `json:"processInfo"`
	}
	if err := call(ctx, cdpURL, "SystemInfo.getProcessInfo", map[string]any{}, &response); err != nil {
		return 0, err
	}
	for _, process := range response.ProcessInfo {
		if process.Type == "browser" && process.ID > 0 {
			return process.ID, nil
		}
	}
	return 0, errors.New("browser process not reported")
}

// CloseBrowser asks the browser at cdpURL to exit. A browser that no longer
// answers is treated as already closed.
func CloseBrowser(ctx context.Context, cdpURL string) error {
	err := call(ctx, cdpURL, "Browser.close", map[string]any{}, nil)
	if errors.Is(err, ErrUnreachable) {
		return nil
	}
	// The browser often exits before it acknowledges the command.
	if waitForExit(ctx, cdpURL) {
		return nil
	}
	if err == nil {
		err = errors.New("browser did not exit after Browser.close")
	}
	return err
}

// waitForExit reports whether the browser at cdpURL stops accepting
// connections within exitTimeout. It outlives ctx, because a caller that
// stopped waiting for the close still needs its outcome.
func waitForExit(ctx context.Context, cdpURL string) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), exitTimeout)
	defer cancel()
	for {
		if errors.Is(Probe(ctx, cdpURL), ErrUnreachable) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(exitPollInterval):
		}
	}
}

// ErrUnreachable reports that nothing accepts connections at a browser's
// DevTools address, so the browser is no longer running.
var ErrUnreachable = errors.New("browser is not running")

// classifyDialError marks a refused connection as ErrUnreachable. Timeouts
// and other failures leave the browser's state unknown.
func classifyDialError(err error) error {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" && !opErr.Timeout() {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return err
}

func browserWebSocketURL(ctx context.Context, cdpURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cdpURL, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", classifyDialError(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("browser DevTools endpoint returned %s", response.Status)
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&version); err != nil {
		return "", fmt.Errorf("decode browser DevTools version: %w", err)
	}
	if version.WebSocketDebuggerURL == "" {
		return "", errors.New("browser DevTools endpoint did not report a WebSocket URL")
	}
	return version.WebSocketDebuggerURL, nil
}

// dialBrowser opens a browser-level DevTools connection.
func dialBrowser(ctx context.Context, cdpURL string) (*websocket.Conn, error) {
	webSocketURL, err := browserWebSocketURL(ctx, cdpURL)
	if err != nil {
		return nil, err
	}
	conn, response, err := websocket.Dial(ctx, webSocketURL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, classifyDialError(err)
	}
	conn.SetReadLimit(cdpReadLimitBytes)
	return conn, nil
}

// call sends one browser-level DevTools command and decodes its result.
func call(ctx context.Context, cdpURL, method string, params, result any) error {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseNow() }()

	const requestID = 1
	message, err := json.Marshal(map[string]any{"id": requestID, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, message); err != nil {
		return err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &response); err != nil || response.ID != requestID {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("%s: %s", method, response.Error.Message)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(response.Result, result)
	}
}
