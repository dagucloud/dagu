// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// cdpCommand is one command a fake browser received.
type cdpCommand struct {
	Method    string         `json:"method"`
	SessionID string         `json:"sessionId"`
	Params    map[string]any `json:"params"`
}

// cdpEvent is an event a fake browser sends, on a target session when
// SessionID is set.
type cdpEvent struct {
	Method    string         `json:"method"`
	SessionID string         `json:"sessionId,omitempty"`
	Params    map[string]any `json:"params"`
}

// scriptedBrowser answers every DevTools command and then sends the events
// its script returns for that command.
type scriptedBrowser struct {
	server   *httptest.Server
	script   func(cdpCommand) []cdpEvent
	mu       sync.Mutex
	commands []cdpCommand
	conns    []*websocket.Conn
}

func newScriptedBrowser(t *testing.T, script func(cdpCommand) []cdpEvent) *scriptedBrowser {
	t.Helper()
	fake := &scriptedBrowser{script: script}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *scriptedBrowser) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/json/version" {
		wsURL := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/devtools/browser/test"
		_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": wsURL})
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	f.mu.Lock()
	f.conns = append(f.conns, conn)
	f.mu.Unlock()
	ctx := r.Context()
	send := func(message any) {
		data, _ := json.Marshal(message)
		_ = conn.Write(ctx, websocket.MessageText, data)
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var command struct {
			cdpCommand
			ID int `json:"id"`
		}
		if json.Unmarshal(data, &command) != nil {
			continue
		}
		f.mu.Lock()
		f.commands = append(f.commands, command.cdpCommand)
		f.mu.Unlock()
		send(map[string]any{"id": command.ID, "sessionId": command.SessionID, "result": map[string]any{}})
		for _, event := range f.script(command.cdpCommand) {
			send(event)
		}
	}
}

// disconnect drops every DevTools connection, as a browser that exits does.
func (f *scriptedBrowser) disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, conn := range f.conns {
		_ = conn.CloseNow()
	}
}

func (f *scriptedBrowser) received(method string) []cdpCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []cdpCommand
	for _, command := range f.commands {
		if command.Method == method {
			matched = append(matched, command)
		}
	}
	return matched
}

// attachedTo reports a target of kind attached as session, under the target
// of parent when parent is set.
func attachedTo(parent, session, kind string) cdpEvent {
	return cdpEvent{Method: "Target.attachedToTarget", SessionID: parent, Params: map[string]any{
		"sessionId": session, "targetInfo": map[string]any{"type": kind},
	}}
}
