// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	targetTypePage    = "page"
	dialogTypeConfirm = "confirm"
	dialogTypePrompt  = "prompt"
	// answerTimeout bounds the answer to a dialog, which holds the lock that
	// Take waits for.
	answerTimeout = 5 * time.Second
)

// Dialog is a JavaScript dialog a page opened and the watcher answered.
type Dialog struct {
	// Type is alert, confirm, prompt, or beforeunload.
	Type    string
	Message string
	// Dismissed reports a confirm the watcher dismissed in place of
	// accepting it.
	Dismissed bool
}

// DialogOptions chooses how a DialogWatcher answers dialogs.
type DialogOptions struct {
	// DismissConfirm answers a confirm with cancel in place of OK. The
	// other dialogs are accepted either way.
	DismissConfirm bool
}

// DialogWatcher answers the JavaScript dialogs a browser's pages open. An
// open dialog blocks its page until it is answered.
type DialogWatcher struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	done   chan struct{}
	lastID atomic.Int64
	// autoAttachID is the request whose reply confirms that pages are
	// watched.
	autoAttachID int64
	opts         DialogOptions

	mu       sync.Mutex
	answered []Dialog
}

// WatchDialogs answers every dialog in the pages of the browser at cdpURL,
// including pages opened later, until the watcher is closed. A dialog is
// accepted, a prompt with its default text, except a confirm under
// opts.DismissConfirm, which is dismissed.
func WatchDialogs(ctx context.Context, cdpURL string, opts DialogOptions) (*DialogWatcher, error) {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w := &DialogWatcher{conn: conn, cancel: cancel, done: make(chan struct{}), opts: opts}
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

// Take returns the dialogs answered since the previous call.
func (w *DialogWatcher) Take() []Dialog {
	w.mu.Lock()
	defer w.mu.Unlock()
	answered := w.answered
	w.answered = nil
	return answered
}

// Close stops watching. Dialogs opened afterwards stay open.
func (w *DialogWatcher) Close() error {
	w.cancel()
	err := w.conn.Close(websocket.StatusNormalClosure, "")
	<-w.done
	return err
}

// answer answers a dialog and records it. The answer and its record share
// the lock, so a Take made once the page has gone on reports the dialog.
func (w *DialogWatcher) answer(ctx context.Context, sessionID string, params map[string]any, dialog Dialog) {
	ctx, cancel := context.WithTimeout(ctx, answerTimeout)
	defer cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	if writeCommand(ctx, w.conn, w.lastID.Add(1), sessionID, "Page.handleJavaScriptDialog", params) == nil {
		w.answered = append(w.answered, dialog)
	}
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
			dismiss := event.Type == dialogTypeConfirm && w.opts.DismissConfirm
			params := map[string]any{"accept": !dismiss}
			if event.Type == dialogTypePrompt {
				params["promptText"] = event.DefaultPrompt
			}
			w.answer(ctx, message.SessionID, params, Dialog{Type: event.Type, Message: event.Message, Dismissed: dismiss})
		}
	}
}
