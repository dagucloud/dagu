// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dialogScript reports one page and one service worker when auto-attach is
// enabled, and opens a confirm and a prompt in the page once its Page domain
// is enabled.
func dialogScript(command cdpCommand) []cdpEvent {
	switch command.Method {
	case "Target.setAutoAttach":
		return []cdpEvent{attachedTo("", "page-1", "page"), attachedTo("", "worker-1", "service_worker")}
	case "Page.enable":
		return []cdpEvent{
			{Method: "Page.javascriptDialogOpening", SessionID: command.SessionID,
				Params: map[string]any{"type": "confirm", "message": "Delete the row?"}},
			{Method: "Page.javascriptDialogOpening", SessionID: command.SessionID,
				Params: map[string]any{"type": "prompt", "message": "Your name?", "defaultPrompt": "guest"}},
		}
	}
	return nil
}

// Every dialog a page opens is accepted, a prompt with its default text, and
// reported once.
func TestDialogWatcherAcceptsDialogs(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, dialogScript)
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
