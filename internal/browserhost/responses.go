// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// BotCheckCloudflare names Cloudflare's check of whether a browser is run
// by a person, which a site behind Cloudflare answers with in place of the
// page.
const BotCheckCloudflare = "cloudflare"

const (
	resourceTypeDocument = "Document"
	// cloudflareMitigated is the header Cloudflare sets to
	// cloudflareChallenge on every answer that is one of its checks.
	cloudflareMitigated = "cf-mitigated"
	cloudflareChallenge = "challenge"
)

// PageResponse is how a site answered the document a page loaded.
type PageResponse struct {
	Status int
	// BotCheck names the check the site answered with in place of the page,
	// such as BotCheckCloudflare, or is empty.
	BotCheck string
}

// PageResponseWatcher keeps how sites answered the documents a browser's
// pages loaded, by host.
type PageResponseWatcher struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	done   chan struct{}
	lastID atomic.Int64
	// autoAttachID is the request whose reply confirms that pages are
	// watched.
	autoAttachID int64

	mu        sync.Mutex
	responses map[string]PageResponse
}

// WatchPageResponses keeps how sites answer the documents the pages of the
// browser at cdpURL load, including pages opened later, until the watcher
// is closed. A document a frame loads is not a page's.
func WatchPageResponses(ctx context.Context, cdpURL string) (*PageResponseWatcher, error) {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w := &PageResponseWatcher{conn: conn, cancel: cancel, done: make(chan struct{}), responses: map[string]PageResponse{}}
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

// Response returns how host last answered a document a page loaded.
func (w *PageResponseWatcher) Response(host string) (PageResponse, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	response, ok := w.responses[strings.ToLower(host)]
	return response, ok
}

// Close stops watching.
func (w *PageResponseWatcher) Close() error {
	w.cancel()
	err := w.conn.Close(websocket.StatusNormalClosure, "")
	<-w.done
	return err
}

func (w *PageResponseWatcher) read(ctx context.Context, acknowledged chan<- error) {
	defer close(w.done)
	// A page's main frame has the page's target ID, which tells its
	// documents from those of the frames it holds.
	mainFrames := map[string]string{}
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
					TargetID string `json:"targetId"`
					Type     string `json:"type"`
				} `json:"targetInfo"`
			}
			if json.Unmarshal(message.Params, &event) != nil || event.TargetInfo.Type != targetTypePage {
				continue
			}
			mainFrames[event.SessionID] = event.TargetInfo.TargetID
			// Only response headers are read, so bodies are neither kept nor
			// sent.
			_ = writeCommand(ctx, w.conn, w.lastID.Add(1), event.SessionID, "Network.enable",
				map[string]any{"maxTotalBufferSize": 0, "maxResourceBufferSize": 0, "maxPostDataSize": 0})
		case "Target.detachedFromTarget":
			var event struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(message.Params, &event) == nil {
				delete(mainFrames, event.SessionID)
			}
		case "Network.responseReceived":
			var event struct {
				Type     string `json:"type"`
				FrameID  string `json:"frameId"`
				Response struct {
					URL     string            `json:"url"`
					Status  int               `json:"status"`
					Headers map[string]string `json:"headers"`
				} `json:"response"`
			}
			if json.Unmarshal(message.Params, &event) != nil || event.Type != resourceTypeDocument {
				continue
			}
			mainFrame, ok := mainFrames[message.SessionID]
			host := requestHost(event.Response.URL)
			if !ok || event.FrameID != mainFrame || host == "" {
				continue
			}
			w.mu.Lock()
			w.responses[host] = PageResponse{Status: event.Response.Status, BotCheck: botCheck(event.Response.Headers)}
			w.mu.Unlock()
		}
	}
}

// botCheck names the check a response with headers is, or returns "" for
// a response that is not one.
func botCheck(headers map[string]string) string {
	for name, value := range headers {
		if strings.EqualFold(name, cloudflareMitigated) && strings.EqualFold(strings.TrimSpace(value), cloudflareChallenge) {
			return BotCheckCloudflare
		}
	}
	return ""
}
