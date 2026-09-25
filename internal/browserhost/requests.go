// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

const (
	targetTypeIframe = "iframe"
	// blockedByDevTools is the reason the browser gives for a request a
	// DevTools client failed on purpose, as the browser runtime does for
	// hosts its domain policy excludes.
	blockedByDevTools = "inspector"
)

// BlockedRequestWatcher counts the requests of a browser's pages and frames
// that a DevTools client blocked, by host.
type BlockedRequestWatcher struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	done   chan struct{}
	lastID atomic.Int64
	// autoAttachID is the request whose reply confirms that pages are
	// watched.
	autoAttachID int64

	mu      sync.Mutex
	blocked map[string]int
	// readErr ends the counting; nil while the connection is read.
	readErr error
}

// WatchBlockedRequests counts blocked requests in the pages of the browser
// at cdpURL, including pages and frames opened later, until the watcher is
// closed.
func WatchBlockedRequests(ctx context.Context, cdpURL string) (*BlockedRequestWatcher, error) {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w := &BlockedRequestWatcher{conn: conn, cancel: cancel, done: make(chan struct{}), blocked: map[string]int{}}
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

// Take returns the number of requests blocked per host since the previous
// call, or nil when none were. Its error reports that the watcher lost its
// connection and counts no further requests.
func (w *BlockedRequestWatcher) Take() (map[string]int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.blocked) == 0 {
		return nil, w.readErr
	}
	blocked := w.blocked
	w.blocked = map[string]int{}
	return blocked, w.readErr
}

// Close stops watching.
func (w *BlockedRequestWatcher) Close() error {
	w.cancel()
	err := w.conn.Close(websocket.StatusNormalClosure, "")
	<-w.done
	return err
}

func (w *BlockedRequestWatcher) read(ctx context.Context, acknowledged chan<- error) {
	defer close(w.done)
	// The failure event carries no URL, so each target's requests in flight
	// keep the host they were last sent to, by request ID.
	inFlight := map[string]map[string]string{}
	for {
		_, data, err := w.conn.Read(ctx)
		if err != nil {
			w.mu.Lock()
			w.readErr = err
			w.mu.Unlock()
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
			// Only request URLs are read, so bodies are neither kept nor sent.
			_ = writeCommand(ctx, w.conn, w.lastID.Add(1), event.SessionID, "Network.enable",
				map[string]any{"maxTotalBufferSize": 0, "maxResourceBufferSize": 0, "maxPostDataSize": 0})
			// Cross-origin frames are targets of their own under the page.
			_ = writeCommand(ctx, w.conn, w.lastID.Add(1), event.SessionID, "Target.setAutoAttach", autoAttachParams())
		case "Target.detachedFromTarget":
			var event struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(message.Params, &event) == nil {
				delete(inFlight, event.SessionID)
			}
		case "Network.requestWillBeSent":
			var event struct {
				RequestID string `json:"requestId"`
				Request   struct {
					URL string `json:"url"`
				} `json:"request"`
			}
			if json.Unmarshal(message.Params, &event) != nil {
				continue
			}
			requests := inFlight[message.SessionID]
			if requests == nil {
				requests = map[string]string{}
				inFlight[message.SessionID] = requests
			}
			if host := requestHost(event.Request.URL); host != "" {
				requests[event.RequestID] = host
			} else {
				delete(requests, event.RequestID)
			}
		case "Network.loadingFinished":
			var event struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(message.Params, &event) == nil {
				delete(inFlight[message.SessionID], event.RequestID)
			}
		case "Network.loadingFailed":
			var event struct {
				RequestID     string `json:"requestId"`
				BlockedReason string `json:"blockedReason"`
			}
			if json.Unmarshal(message.Params, &event) != nil {
				continue
			}
			host, ok := inFlight[message.SessionID][event.RequestID]
			delete(inFlight[message.SessionID], event.RequestID)
			if ok && event.BlockedReason == blockedByDevTools {
				w.mu.Lock()
				w.blocked[host]++
				w.mu.Unlock()
			}
		}
	}
}

// requestHost returns the lowercase host of an HTTP(S) URL, or "" for other
// URLs.
func requestHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
