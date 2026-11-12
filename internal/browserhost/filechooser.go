// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// fileChooserSingle is the mode of a file picker that takes one file.
const fileChooserSingle = "selectSingle"

// closedTargetMarkers are the texts by which the browser refuses a command
// sent to a target that closed, which then opens no picker.
var closedTargetMarkers = []string{"Session with given id not found", "No target with given id", "Target closed"}

// ErrNoFileChooser reports that no file picker opened.
var ErrNoFileChooser = errors.New("no file picker opened")

// ErrSingleFileChooser reports a file picker that takes one file when it was
// to be given several.
var ErrSingleFileChooser = errors.New("the file picker takes one file")

// fileChooser is a file picker a page or frame opened.
type fileChooser struct {
	session       string
	mode          string
	backendNodeID int64
}

// ChooseFiles gives the files at paths, which must be absolute, to the first
// file picker a page or frame of the browser at cdpURL opens while open runs
// or within wait after it returns. Pickers are intercepted, so none is shown.
// It returns ErrNoFileChooser when no picker opens, and ErrSingleFileChooser,
// giving nothing, when the picker takes one file and paths holds several.
func ChooseFiles(ctx context.Context, cdpURL string, paths []string, wait time.Duration, open func(context.Context) error) error {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return err
	}
	readCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c := &chooserConn{conn: conn, ready: make(chan error, 1), chosen: make(chan fileChooser, 1), replies: map[int64]chan error{}}
	c.rootID = c.lastID.Add(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.read(readCtx)
	}()
	// Closing the connection detaches its sessions, which ends the
	// interception.
	defer func() {
		cancel()
		_ = conn.CloseNow()
		<-done
	}()

	if err := writeCommand(ctx, conn, c.rootID, "", "Target.setAutoAttach", autoAttachParams()); err != nil {
		return err
	}
	select {
	case err := <-c.ready:
		if err != nil {
			return fmt.Errorf("intercept file pickers: %w", err)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := open(ctx); err != nil {
		return err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var chooser fileChooser
	select {
	case chooser = <-c.chosen:
	case <-timer.C:
		return ErrNoFileChooser
	case <-ctx.Done():
		return ctx.Err()
	}
	if chooser.mode == fileChooserSingle && len(paths) > 1 {
		return ErrSingleFileChooser
	}
	if chooser.backendNodeID == 0 {
		return errors.New("the file picker opened without a file input to give the files to")
	}
	return c.command(ctx, chooser.session, "DOM.setFileInputFiles", map[string]any{"files": paths, "backendNodeId": chooser.backendNodeID})
}

// chooserConn is a DevTools connection that intercepts the file pickers of
// every page and frame it attaches to.
type chooserConn struct {
	conn   *websocket.Conn
	lastID atomic.Int64
	// rootID is the request that attaches the connection to the browser's
	// pages.
	rootID int64
	// ready receives nil once every page and frame attached so far
	// intercepts its file pickers, or why they could not.
	ready chan error
	// chosen receives the first file picker opened.
	chosen chan fileChooser

	mu      sync.Mutex
	replies map[int64]chan error
	readErr error
}

// command sends one command to a target session and waits for its reply.
func (c *chooserConn) command(ctx context.Context, sessionID, method string, params map[string]any) error {
	id := c.lastID.Add(1)
	reply := make(chan error, 1)
	c.mu.Lock()
	if c.readErr != nil {
		c.mu.Unlock()
		return c.readErr
	}
	c.replies[id] = reply
	c.mu.Unlock()
	if err := writeCommand(ctx, c.conn, id, sessionID, method, params); err != nil {
		return err
	}
	select {
	case err := <-reply:
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// read handles the connection's replies and events until it closes. The
// browser reports the targets an auto-attach request attaches to before it
// answers the request, so once that answer and those of the commands arming
// each target are in, every page and frame open then intercepts its pickers,
// or ready says why one does not.
func (c *chooserConn) read(ctx context.Context) {
	// arming holds the method of each unanswered command that arms an
	// attached target, and armErr what kept a target from being armed.
	arming := map[int64]string{}
	var armErr error
	rootAnswered, readySent := false, false
	signal := func(err error) {
		if !readySent {
			readySent = true
			c.ready <- err
		}
	}
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.mu.Lock()
			c.readErr = err
			for id, reply := range c.replies {
				reply <- err
				delete(c.replies, id)
			}
			c.mu.Unlock()
			signal(err)
			return
		}
		var message cdpMessage
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		switch {
		case message.ID != 0:
			var replyErr error
			if message.Error != nil {
				replyErr = errors.New(message.Error.Message)
			}
			switch {
			case message.ID == c.rootID:
				if replyErr != nil {
					signal(fmt.Errorf("Target.setAutoAttach: %w", replyErr))
				}
				rootAnswered = true
			case arming[message.ID] != "":
				// A target that closed meanwhile cannot be armed, and opens
				// no picker either.
				if replyErr != nil && !targetClosed(replyErr) {
					armErr = errors.Join(armErr, fmt.Errorf("%s: %w", arming[message.ID], replyErr))
				}
				delete(arming, message.ID)
			default:
				c.mu.Lock()
				if reply, ok := c.replies[message.ID]; ok {
					reply <- replyErr
					delete(c.replies, message.ID)
				}
				c.mu.Unlock()
			}
		case message.Method == "Target.attachedToTarget":
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
			// The Page domain must be enabled for the picker to be reported,
			// and cross-origin frames are targets of their own under the
			// page.
			for _, command := range []struct {
				method string
				params map[string]any
			}{
				{"Page.enable", map[string]any{}},
				{"Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}},
				{"Target.setAutoAttach", autoAttachParams()},
			} {
				id := c.lastID.Add(1)
				arming[id] = command.method
				_ = writeCommand(ctx, c.conn, id, event.SessionID, command.method, command.params)
			}
		case message.Method == "Page.fileChooserOpened":
			var event struct {
				Mode          string `json:"mode"`
				BackendNodeID int64  `json:"backendNodeId"`
			}
			if json.Unmarshal(message.Params, &event) != nil {
				continue
			}
			select {
			case c.chosen <- fileChooser{session: message.SessionID, mode: event.Mode, backendNodeID: event.BackendNodeID}:
			default:
			}
		}
		if rootAnswered && len(arming) == 0 {
			signal(armErr)
		}
	}
}

// targetClosed reports whether err refuses a command because its target
// closed.
func targetClosed(err error) bool {
	return slices.ContainsFunc(closedTargetMarkers, func(marker string) bool { return strings.Contains(err.Error(), marker) })
}
