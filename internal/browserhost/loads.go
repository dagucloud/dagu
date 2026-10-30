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

// FrameLoadWatcher tells whether a frame of a browser's pages is loading a
// document, as a frame a form posts into is until the answer has loaded,
// and which page opened each window.
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
	// pages holds the open pages by their session, and parents the session
	// each cross-origin frame is attached under.
	pages   map[string]*watchedPage
	parents map[string]string
}

// watchedPage is an open page and what the watcher saw of its loads.
type watchedPage struct {
	targetID string
	// opener is the target ID of the page that opened it, if any.
	opener string
	opened time.Time
	// began is when a frame of the page last began loading a document, and
	// ended when the page last stopped loading.
	began, ended time.Time
}

// Window is a page that another page opened, as window.open does.
type Window struct {
	// TargetID is the window's target ID, and Opener that of the page that
	// opened it.
	TargetID, Opener string
	// Opened is when the watcher saw the window open.
	Opened time.Time
}

// PageLoad is what the watcher saw of the documents a page's frames load.
type PageLoad struct {
	// Loading is set while a frame of the page, its main frame and frames
	// from other origins included, loads a document.
	Loading bool
	// Began is when such a load last began, and Ended when the page last
	// stopped loading; each is zero until seen.
	Began, Ended time.Time
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
	w := &FrameLoadWatcher{
		conn: conn, cancel: cancel, done: make(chan struct{}),
		loading: map[string]map[string]bool{}, pages: map[string]*watchedPage{}, parents: map[string]string{},
	}
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

// Windows returns the open windows whose opener is open too.
func (w *FrameLoadWatcher) Windows() []Window {
	w.mu.Lock()
	defer w.mu.Unlock()
	var windows []Window
	for _, page := range w.pages {
		if page.opener != "" && w.pageWithTarget(page.opener) != nil {
			windows = append(windows, Window{TargetID: page.targetID, Opener: page.opener, Opened: page.opened})
		}
	}
	return windows
}

// PageLoad reports the loads of the open page with targetID; a page that is
// not open loads nothing.
func (w *FrameLoadWatcher) PageLoad(targetID string) PageLoad {
	w.mu.Lock()
	defer w.mu.Unlock()
	page := w.pageWithTarget(targetID)
	if page == nil {
		return PageLoad{}
	}
	return PageLoad{Loading: w.pageLoading(page), Began: page.began, Ended: page.ended}
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
					Type     string `json:"type"`
					TargetID string `json:"targetId"`
					OpenerID string `json:"openerId"`
				} `json:"targetInfo"`
			}
			if json.Unmarshal(message.Params, &event) != nil {
				continue
			}
			w.mu.Lock()
			switch event.TargetInfo.Type {
			case targetTypePage:
				w.pages[event.SessionID] = &watchedPage{targetID: event.TargetInfo.TargetID, opener: event.TargetInfo.OpenerID, opened: time.Now()}
			case targetTypeIframe:
				w.parents[event.SessionID] = message.SessionID
			default:
				w.mu.Unlock()
				continue
			}
			w.mu.Unlock()
			_ = writeCommand(ctx, w.conn, w.lastID.Add(1), event.SessionID, "Page.enable", map[string]any{})
			// Cross-origin frames are targets of their own under the page.
			_ = writeCommand(ctx, w.conn, w.lastID.Add(1), event.SessionID, "Target.setAutoAttach", autoAttachParams())
		case "Target.detachedFromTarget":
			var event struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(message.Params, &event) == nil {
				w.mu.Lock()
				w.update(event.SessionID, func() {
					delete(w.loading, event.SessionID)
					delete(w.pages, event.SessionID)
					delete(w.parents, event.SessionID)
				})
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
			w.update(message.SessionID, func() {
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
			})
			w.mu.Unlock()
		}
	}
}

// update applies change, which may start or end loads in the page or
// cross-origin frame attached as session, and notes when the page that
// session belongs to began loading or stopped. Callers hold w.mu.
func (w *FrameLoadWatcher) update(session string, change func()) {
	page := w.pageOf(session)
	if page == nil {
		change()
		return
	}
	wasLoading := w.pageLoading(page)
	before := len(w.loading[session])
	change()
	now := time.Now()
	if len(w.loading[session]) > before {
		page.began = now
	}
	if wasLoading && !w.pageLoading(page) {
		page.ended = now
	}
}

// pageOf returns the page that the page or cross-origin frame attached as
// session belongs to, or nil. Callers hold w.mu.
func (w *FrameLoadWatcher) pageOf(session string) *watchedPage {
	// A frame nests in at most as many others as there are.
	for range len(w.parents) + 1 {
		if page := w.pages[session]; page != nil {
			return page
		}
		parent, ok := w.parents[session]
		if !ok {
			return nil
		}
		session = parent
	}
	return nil
}

// pageWithTarget returns the open page with targetID, or nil. Callers hold
// w.mu.
func (w *FrameLoadWatcher) pageWithTarget(targetID string) *watchedPage {
	for _, page := range w.pages {
		if page.targetID == targetID {
			return page
		}
	}
	return nil
}

// pageLoading reports whether a frame of page loads a document. Callers
// hold w.mu.
func (w *FrameLoadWatcher) pageLoading(page *watchedPage) bool {
	for session, frames := range w.loading {
		if len(frames) > 0 && w.pageOf(session) == page {
			return true
		}
	}
	return false
}
