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

// SetDownloadDir makes the browser save downloads into dir without prompting.
func SetDownloadDir(ctx context.Context, cdpURL, dir string) error {
	return call(ctx, cdpURL, "Browser.setDownloadBehavior", map[string]any{
		"behavior":     "allow",
		"downloadPath": dir,
	}, nil)
}

// CloseBrowser asks the browser at cdpURL to exit. A browser that no longer
// answers is treated as already closed.
func CloseBrowser(ctx context.Context, cdpURL string) error {
	err := call(ctx, cdpURL, "Browser.close", map[string]any{}, nil)
	if err == nil || isUnreachable(err) {
		return nil
	}
	// The browser may exit before it acknowledges the command.
	if Probe(ctx, cdpURL) != nil {
		return nil
	}
	return err
}

type unreachableError struct {
	err error
}

func (e *unreachableError) Error() string { return e.err.Error() }
func (e *unreachableError) Unwrap() error { return e.err }

func isUnreachable(err error) bool {
	var target *unreachableError
	return errors.As(err, &target)
}

func browserWebSocketURL(ctx context.Context, cdpURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cdpURL, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", &unreachableError{err: err}
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

// call sends one browser-level DevTools command and decodes its result.
func call(ctx context.Context, cdpURL, method string, params, result any) error {
	webSocketURL, err := browserWebSocketURL(ctx, cdpURL)
	if err != nil {
		return err
	}
	conn, response, err := websocket.Dial(ctx, webSocketURL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return &unreachableError{err: err}
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(cdpReadLimitBytes)

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
