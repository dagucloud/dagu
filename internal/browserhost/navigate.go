// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/websocket"
)

const (
	// loadAborted is the error of a page load that ended without a document,
	// as one that became a download or was answered with no content does.
	loadAborted = "net::ERR_ABORTED"
	// lifecycleParsed is the lifecycle event of a document the browser has
	// parsed.
	lifecycleParsed = "DOMContentLoaded"
)

// errPageClosed reports a page that closed while it loaded.
var errPageClosed = errors.New("the page closed while it loaded")

// Load is what a page load brought.
type Load struct {
	// Document reports a new document in the page, the browser's error page
	// for a load that failed included.
	Document bool
	// Download reports a load that became a download, which leaves the page
	// as it was.
	Download bool
}

// Navigate loads url in the page whose target ID is targetID, in the browser
// at cdpURL. With a new document, it returns once the page has parsed it; a
// page whose document is replaced before it is parsed is waited on until the
// document that replaced it is. A load that brings none, such as one that
// became a download, was answered with no content, or moved within the
// document to a fragment, returns as soon as the browser reports it.
func Navigate(ctx context.Context, cdpURL, targetID, url string) (Load, error) {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return Load{}, err
	}
	defer func() { _ = conn.CloseNow() }()
	n := &navigation{conn: conn, committed: map[string]bool{}, parsed: map[string]bool{}}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := n.command(ctx, "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, &attached); err != nil {
		return Load{}, err
	}
	n.session = attached.SessionID
	if err := n.command(ctx, "Page.enable", nil, nil); err != nil {
		return Load{}, err
	}
	if err := n.command(ctx, "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}, nil); err != nil {
		return Load{}, err
	}
	var started struct {
		LoaderID   string `json:"loaderId"`
		ErrorText  string `json:"errorText"`
		IsDownload bool   `json:"isDownload"`
	}
	if err := n.command(ctx, "Page.navigate", map[string]any{"url": url}, &started); err != nil {
		return Load{}, err
	}
	// A load within the document has no loader of its own; neither it nor
	// an aborted load leaves a document to wait for.
	if started.ErrorText == loadAborted || started.LoaderID == "" {
		return Load{Download: started.IsDownload}, nil
	}
	for !n.committed[started.LoaderID] || !n.parsed[n.current] {
		if err := n.next(ctx); err != nil {
			return Load{}, err
		}
	}
	return Load{Document: true}, nil
}

// navigation is a DevTools connection attached to the page Navigate loads.
// It is read only while a command waits for its reply or the load waits for
// its document, so every event is seen, in order.
type navigation struct {
	conn    *websocket.Conn
	session string
	lastID  int64
	// committed holds the loaders whose documents the page's main frame
	// showed, current the last of them, and parsed those the page parsed.
	committed map[string]bool
	current   string
	parsed    map[string]bool
	// waiting is the command whose reply is awaited; replied reports that
	// it came, with result or failure.
	waiting int64
	replied bool
	result  json.RawMessage
	failure error
}

// command sends method to the page, or to the browser before the page is
// attached, and decodes its result into result unless that is nil.
func (n *navigation) command(ctx context.Context, method string, params map[string]any, result any) error {
	if params == nil {
		params = map[string]any{}
	}
	n.lastID++
	n.waiting, n.replied = n.lastID, false
	if err := writeCommand(ctx, n.conn, n.waiting, n.session, method, params); err != nil {
		return err
	}
	for !n.replied {
		if err := n.next(ctx); err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
	}
	if n.failure != nil {
		return fmt.Errorf("%s: %w", method, n.failure)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(n.result, result)
}

// next reads one message and keeps what it tells.
func (n *navigation) next(ctx context.Context) error {
	_, data, err := n.conn.Read(ctx)
	if err != nil {
		return err
	}
	var message cdpMessage
	if json.Unmarshal(data, &message) != nil {
		return nil
	}
	switch {
	case message.ID != 0 && message.ID == n.waiting:
		var reply struct {
			Result json.RawMessage `json:"result"`
		}
		_ = json.Unmarshal(data, &reply)
		n.result, n.failure, n.replied = reply.Result, nil, true
		if message.Error != nil {
			n.failure = errors.New(message.Error.Message)
		}
	case message.Method == "Target.detachedFromTarget":
		var event struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(message.Params, &event) == nil && n.session != "" && event.SessionID == n.session {
			return errPageClosed
		}
	case message.SessionID != n.session:
	case message.Method == "Page.frameNavigated":
		var event struct {
			Frame struct {
				ParentID string `json:"parentId"`
				LoaderID string `json:"loaderId"`
			} `json:"frame"`
		}
		if json.Unmarshal(message.Params, &event) == nil && event.Frame.ParentID == "" {
			n.committed[event.Frame.LoaderID], n.current = true, event.Frame.LoaderID
		}
	case message.Method == "Page.lifecycleEvent":
		var event struct {
			LoaderID string `json:"loaderId"`
			Name     string `json:"name"`
		}
		if json.Unmarshal(message.Params, &event) == nil && event.Name == lifecycleParsed {
			n.parsed[event.LoaderID] = true
		}
	}
	return nil
}
