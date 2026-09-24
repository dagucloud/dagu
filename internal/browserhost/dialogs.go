// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

const (
	targetTypePage   = "page"
	dialogTypePrompt = "prompt"
)

// Dialog is a JavaScript dialog a page opened and the watcher accepted.
type Dialog struct {
	// Type is alert, confirm, prompt, or beforeunload.
	Type    string
	Message string
}

// DialogWatcher accepts the JavaScript dialogs a browser's pages open. An
// open dialog blocks its page until it is answered.
type DialogWatcher struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	done   chan struct{}
	lastID atomic.Int64
	// autoAttachID is the request whose reply confirms that pages are
	// watched.
	autoAttachID int64

	mu       sync.Mutex
	accepted []Dialog
}

// WatchDialogs accepts every dialog in the pages of the browser at cdpURL,
// including pages opened later, until the watcher is closed. A prompt is
// answered with its default text.
func WatchDialogs(ctx context.Context, cdpURL string) (*DialogWatcher, error) {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w := &DialogWatcher{conn: conn, cancel: cancel, done: make(chan struct{})}
	w.autoAttachID = w.lastID.Add(1)
	acknowledged := make(chan error, 1)
	go w.read(readCtx, acknowledged)

	// Attaching to every page, existing and future, delivers their dialog
	// events on this connection.
	if err := writeCommand(ctx, w.conn, w.autoAttachID, "", "Target.setAutoAttach", autoAttachParams()); err != nil {
		_ = w.Close()
		return nil, err
	}
	select {
	case err := <-acknowledged:
		if err != nil {
			_ = w.Close()
			return nil, err
		}
	case <-ctx.Done():
		_ = w.Close()
		return nil, ctx.Err()
	}
	return w, nil
}

// Take returns the dialogs accepted since the previous call.
func (w *DialogWatcher) Take() []Dialog {
	w.mu.Lock()
	defer w.mu.Unlock()
	accepted := w.accepted
	w.accepted = nil
	return accepted
}

// Close stops watching. Dialogs opened afterwards stay open.
func (w *DialogWatcher) Close() error {
	w.cancel()
	err := w.conn.Close(websocket.StatusNormalClosure, "")
	<-w.done
	return err
}

func (w *DialogWatcher) read(ctx context.Context, acknowledged chan<- error) {
	defer close(w.done)
	for {
		_, data, err := w.conn.Read(ctx)
		if err != nil {
			select {
			case acknowledged <- err:
			default:
			}
			return
		}
		var message cdpMessage
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		switch {
		case message.ID == w.autoAttachID:
			var err error
			if message.Error != nil {
				err = errors.New("Target.setAutoAttach: " + message.Error.Message)
			}
			select {
			case acknowledged <- err:
			default:
			}
		case message.Method == "Target.attachedToTarget":
			var event struct {
				SessionID  string `json:"sessionId"`
				TargetInfo struct {
					Type string `json:"type"`
				} `json:"targetInfo"`
			}
			if json.Unmarshal(message.Params, &event) == nil && event.TargetInfo.Type == targetTypePage {
				_ = writeCommand(ctx, w.conn, w.lastID.Add(1), event.SessionID, "Page.enable", map[string]any{})
			}
		case message.Method == "Page.javascriptDialogOpening":
			var event struct {
				Type          string `json:"type"`
				Message       string `json:"message"`
				DefaultPrompt string `json:"defaultPrompt"`
			}
			if json.Unmarshal(message.Params, &event) != nil {
				continue
			}
			params := map[string]any{"accept": true}
			if event.Type == dialogTypePrompt {
				params["promptText"] = event.DefaultPrompt
			}
			if writeCommand(ctx, w.conn, w.lastID.Add(1), message.SessionID, "Page.handleJavaScriptDialog", params) == nil {
				w.mu.Lock()
				w.accepted = append(w.accepted, Dialog{Type: event.Type, Message: event.Message})
				w.mu.Unlock()
			}
		}
	}
}
