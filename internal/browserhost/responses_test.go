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

// pageAttached reports a page target attached as session, whose main frame
// has the page's target ID.
func pageAttached(session, target string) cdpEvent {
	return cdpEvent{Method: "Target.attachedToTarget", Params: map[string]any{
		"sessionId": session, "targetInfo": map[string]any{"type": "page", "targetId": target},
	}}
}

// documentAnswered reports the answer to a document request of frame.
func documentAnswered(session, frame, url string, status int, headers map[string]any) cdpEvent {
	return cdpEvent{Method: "Network.responseReceived", SessionID: session, Params: map[string]any{
		"type": "Document", "frameId": frame,
		"response": map[string]any{"url": url, "status": status, "headers": headers},
	}}
}

// A site's answer to the document a page loaded is kept by host, the last
// one standing, with the bot check it is, if any. Frames, scripts, and
// targets other than pages are not pages' documents.
func TestPageResponseWatcherKeepsPageDocuments(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, func(command cdpCommand) []cdpEvent {
		switch {
		case command.Method == "Target.setAutoAttach":
			return []cdpEvent{pageAttached("page-1", "frame-main"), attachedTo("", "frame-1", "iframe")}
		case command.Method == "Network.enable" && command.SessionID == "page-1":
			return []cdpEvent{
				documentAnswered("page-1", "frame-main", "https://Shop.example.com/", 403, map[string]any{"CF-Mitigated": "challenge"}),
				documentAnswered("page-1", "frame-main", "https://portal.example.com/", 403, map[string]any{"content-type": "text/html"}),
				documentAnswered("page-1", "frame-main", "https://portal.example.com/home", 200, nil),
				documentAnswered("page-1", "frame-ad", "https://ads.example.net/", 403, nil),
				{Method: "Network.responseReceived", SessionID: "page-1", Params: map[string]any{
					"type": "Script", "frameId": "frame-main", "response": map[string]any{"url": "https://cdn.example.net/app.js", "status": 403},
				}},
			}
		}
		return nil
	})
	watcher, err := browserhost.WatchPageResponses(context.Background(), fake.server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		response, ok := watcher.Response("portal.example.com")
		assert.True(c, ok)
		assert.Equal(c, browserhost.PageResponse{Status: 200}, response)
	}, 5*time.Second, 20*time.Millisecond)
	response, ok := watcher.Response("shop.example.com")
	require.True(t, ok)
	assert.Equal(t, browserhost.PageResponse{Status: 403, BotCheck: browserhost.BotCheckCloudflare}, response)
	_, ok = watcher.Response("ads.example.net")
	assert.False(t, ok, "a frame's document is not the page's")
	_, ok = watcher.Response("cdn.example.net")
	assert.False(t, ok, "a script is not a document")
	enabled := fake.received("Network.enable")
	require.Len(t, enabled, 1, "only pages are watched")
	assert.Equal(t, "page-1", enabled[0].SessionID)
}
