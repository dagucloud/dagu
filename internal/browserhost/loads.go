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

// FrameLoadWatcher tells whether a frame of a browser's pages is loading a
// document, as a frame a form posts into is until the answer has loaded.
type FrameLoadWatcher struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	done   chan struct{}
	lastID atomic.Int64
	// autoAttachID is the request whose reply confirms that pages are
	// watched.
	autoAttachID int64

	mu sync.Mutex
	// loading holds the IDs of the frames loading a document, by the
	// session of their page or cross-origin frame.
	loading map[string]map[string]bool
}

// WatchFrameLoads watches the frames of the pages of the browser at cdpURL,
// including pages opened later, until the watcher is closed. A load that
// began before the watcher started is not seen.
func WatchFrameLoads(ctx context.Context, cdpURL string) (*FrameLoadWatcher, error) {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w := &FrameLoadWatcher{conn: conn, cancel: cancel, done: make(chan struct{}), loading: map[string]map[string]bool{}}
	w.autoAttachID = w.lastID.Add(1)
	acknowledged := make(chan error, 1)
	go w.read(readCtx, acknowledged)

	if err := writeCommand(ctx, conn, w.autoAttachID, "", "Target.setAutoAttach", autoAttachParams()); err != nil {
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

// Loading reports whether a frame of any of the browser's pages, a page's
// main frame included, is loading a document.
func (w *FrameLoadWatcher) Loading() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, frames := range w.loading {
		if len(frames) > 0 {
			return true
		}
	}
	return false
}

// Close stops watching.
func (w *FrameLoadWatcher) Close() error {
	w.cancel()
	err := w.conn.Close(websocket.StatusNormalClosure, "")
	<-w.done
	return err
}

func (w *FrameLoadWatcher) read(ctx context.Context, acknowledged chan<- error) {
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
		switch message.Method {
		case "":
			if message.ID != w.autoAttachID {
				continue
			}
			var err error
			if message.Error != nil {
				err = errors.New("Target.setAutoAttach: " + message.Error.Message)
			}
			select {
			case acknowledged <- err:
			default:
			}
		case "Target.attachedToTarget":
			var event struct {
				SessionID  string `json:"sessionId"`
				TargetInfo struct {
					Type string `json:"type"`
				} `json:"targetInfo"`
			}
			if json.Unmarshal(message.Params, &event) != nil {
				continue
			}
			if event.TargetInfo.Type != targetTypePage && event.TargetInfo.Type != targetTypeIframe {
				continue
			}
			_ = writeCommand(ctx, w.conn, w.lastID.Add(1), event.SessionID, "Page.enable", map[string]any{})
			// Cross-origin frames are targets of their own under the page.
			_ = writeCommand(ctx, w.conn, w.lastID.Add(1), event.SessionID, "Target.setAutoAttach", autoAttachParams())
		case "Target.detachedFromTarget":
			var event struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(message.Params, &event) == nil {
				w.mu.Lock()
				delete(w.loading, event.SessionID)
				w.mu.Unlock()
			}
		case "Page.frameStartedLoading", "Page.frameStoppedLoading", "Page.frameDetached":
			var event struct {
				FrameID string `json:"frameId"`
			}
			if json.Unmarshal(message.Params, &event) != nil || message.SessionID == "" {
				continue
			}
			w.mu.Lock()
			frames := w.loading[message.SessionID]
			if message.Method == "Page.frameStartedLoading" {
				if frames == nil {
					frames = map[string]bool{}
					w.loading[message.SessionID] = frames
				}
				frames[event.FrameID] = true
			} else {
				delete(frames, event.FrameID)
			}
			w.mu.Unlock()
		}
	}
}
