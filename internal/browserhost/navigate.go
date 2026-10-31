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
	// loadAborted is the error of a page load that ended without a
	// document: one that became a download, was answered with no content,
	// or was cancelled before an answer came.
	loadAborted = "net::ERR_ABORTED"
	// lifecycleParsed is the lifecycle event of a document the browser has
	// parsed.
	lifecycleParsed = "DOMContentLoaded"
)

var (
	// errPageClosed reports a page that closed while it loaded.
	errPageClosed = errors.New("the page closed while it loaded")
	// errLoadCancelled reports a load the browser cancelled before the site
	// answered, as it does when another load replaces it or a DevTools
	// client fails its request.
	errLoadCancelled = errors.New("the browser cancelled the load before the site answered")
)

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
// document to a fragment, returns as soon as the browser reports it. A load
// the browser cancelled before the site answered fails, leaving the page as
// it was.
func Navigate(ctx context.Context, cdpURL, targetID, url string) (Load, error) {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return Load{}, err
	}
	defer func() { _ = conn.CloseNow() }()
	n := &navigation{conn: conn, committed: map[string]bool{}, parsed: map[string]bool{}, answered: map[string]bool{}}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := n.command(ctx, "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, &attached); err != nil {
		return Load{}, err
	}
	n.session = attached.SessionID
	// Commands the page's document answers wait while another load is in
	// flight, so these are sent without waiting for their replies: a load
	// that never ends would otherwise hold the goto that replaces it. The
	// document the load brings takes them in before it is shown. Network
	// events tell only whether the site answered, so no body is kept.
	for _, enable := range []struct {
		method string
		params map[string]any
	}{
		{"Page.enable", nil},
		{"Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
		{"Network.enable", map[string]any{"maxTotalBufferSize": 0, "maxResourceBufferSize": 0, "maxPostDataSize": 0}},
	} {
		if err := n.send(ctx, enable.method, enable.params); err != nil {
			return Load{}, err
		}
	}
	var started struct {
		LoaderID   string `json:"loaderId"`
		ErrorText  string `json:"errorText"`
		IsDownload bool   `json:"isDownload"`
	}
	if err := n.command(ctx, "Page.navigate", map[string]any{"url": url}, &started); err != nil {
		return Load{}, err
	}
	// The browser reports the site's answer to a load before it reports the
	// load aborted, and a load within the document has no loader of its own.
	switch {
	case started.ErrorText == loadAborted && !started.IsDownload && !n.answered[started.LoaderID]:
		return Load{}, errLoadCancelled
	case started.ErrorText == loadAborted, started.LoaderID == "":
		return Load{Download: started.IsDownload}, nil
	}
	if err := n.readState(ctx); err != nil {
		return Load{}, err
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
	// answered holds the documents' requests the site answered.
	committed map[string]bool
	current   string
	parsed    map[string]bool
	answered  map[string]bool
	// waiting is the command whose reply is awaited; replied reports that
	// it came, with result or failure.
	waiting int64
	replied bool
	result  json.RawMessage
	failure error
}

// readState reads from the page the document its main frame shows and
// whether the page parsed it. While another load is in flight, the browser
// holds the commands Navigate sends without waiting until the document of
// the load that replaces it commits, and a small document can be shown and
// parsed before the page takes them in, so its events never come. The
// page answers this read only after it took them in, so the events that
// follow are seen and its answer stands in for those it missed.
func (n *navigation) readState(ctx context.Context) error {
	var tree struct {
		FrameTree struct {
			Frame struct {
				LoaderID string `json:"loaderId"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := n.command(ctx, "Page.getFrameTree", nil, &tree); err != nil {
		return err
	}
	loader := tree.FrameTree.Frame.LoaderID
	if loader == "" {
		return nil
	}
	if !n.committed[loader] {
		n.committed[loader], n.current = true, loader
	}
	var state struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := n.command(ctx, "Runtime.evaluate", map[string]any{"expression": "document.readyState", "returnByValue": true}, &state); err != nil {
		return err
	}
	// A document that replaced this one meanwhile is waited on as its
	// events tell.
	if n.current == loader && (state.Result.Value == "interactive" || state.Result.Value == "complete") {
		n.parsed[loader] = true
	}
	return nil
}

// send sends method to the page, or to the browser before the page is
// attached, without waiting for its reply.
func (n *navigation) send(ctx context.Context, method string, params map[string]any) error {
	if params == nil {
		params = map[string]any{}
	}
	n.lastID++
	return writeCommand(ctx, n.conn, n.lastID, n.session, method, params)
}

// command sends method as send does and decodes its result into result
// unless that is nil.
func (n *navigation) command(ctx context.Context, method string, params map[string]any, result any) error {
	if err := n.send(ctx, method, params); err != nil {
		return err
	}
	n.waiting, n.replied = n.lastID, false
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
	case message.Method == "Network.responseReceived":
		var event struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
		}
		if json.Unmarshal(message.Params, &event) == nil && event.Type == resourceTypeDocument {
			n.answered[event.RequestID] = true
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
