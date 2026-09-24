// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cdpCommand is one command a fake browser received.
type cdpCommand struct {
	Method    string         `json:"method"`
	SessionID string         `json:"sessionId"`
	Params    map[string]any `json:"params"`
}

// dialogBrowser reports one page and one service worker when auto-attach is
// enabled, and opens a confirm and a prompt in the page once its Page domain
// is enabled.
type dialogBrowser struct {
	server   *httptest.Server
	mu       sync.Mutex
	commands []cdpCommand
}

func newDialogBrowser(t *testing.T) *dialogBrowser {
	t.Helper()
	fake := &dialogBrowser{}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *dialogBrowser) serve(w http.ResponseWriter, r *http.Request) {
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
	ctx := r.Context()
	send := func(message map[string]any) {
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
		switch command.Method {
		case "Target.setAutoAttach":
			for _, target := range []struct{ session, kind string }{{"page-1", "page"}, {"worker-1", "service_worker"}} {
				send(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{
					"sessionId": target.session, "targetInfo": map[string]any{"type": target.kind},
				}})
			}
		case "Page.enable":
			send(map[string]any{"method": "Page.javascriptDialogOpening", "sessionId": command.SessionID,
				"params": map[string]any{"type": "confirm", "message": "Delete the row?"}})
			send(map[string]any{"method": "Page.javascriptDialogOpening", "sessionId": command.SessionID,
				"params": map[string]any{"type": "prompt", "message": "Your name?", "defaultPrompt": "guest"}})
		}
	}
}

func (f *dialogBrowser) received(method string) []cdpCommand {
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

// Every dialog a page opens is accepted, a prompt with its default text, and
// reported once.
func TestDialogWatcherAcceptsDialogs(t *testing.T) {
	t.Parallel()

	fake := newDialogBrowser(t)
	watcher, err := browserhost.WatchDialogs(context.Background(), fake.server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })

	require.Eventually(t, func() bool {
		return len(fake.received("Page.handleJavaScriptDialog")) == 2
	}, 5*time.Second, 20*time.Millisecond)

	assert.Equal(t, []cdpCommand{{Method: "Page.enable", SessionID: "page-1", Params: map[string]any{}}},
		fake.received("Page.enable"), "only pages are enabled")
	assert.Equal(t, []cdpCommand{
		{Method: "Page.handleJavaScriptDialog", SessionID: "page-1", Params: map[string]any{"accept": true}},
		{Method: "Page.handleJavaScriptDialog", SessionID: "page-1", Params: map[string]any{"accept": true, "promptText": "guest"}},
	}, fake.received("Page.handleJavaScriptDialog"))
	assert.Equal(t, []browserhost.Dialog{
		{Type: "confirm", Message: "Delete the row?"},
		{Type: "prompt", Message: "Your name?"},
	}, watcher.Take())
	assert.Empty(t, watcher.Take())
}
