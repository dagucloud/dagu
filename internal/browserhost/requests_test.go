// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requestSent(session, id, url string) cdpEvent {
	return cdpEvent{Method: "Network.requestWillBeSent", SessionID: session, Params: map[string]any{
		"requestId": id, "request": map[string]any{"url": url},
	}}
}

// blockedByDevTools fails a request as the browser does when a DevTools
// client, such as the browser runtime's domain policy, blocks it.
func blockedByDevTools(session, id string) cdpEvent {
	return cdpEvent{Method: "Network.loadingFailed", SessionID: session, Params: map[string]any{
		"requestId": id, "errorText": "net::ERR_BLOCKED_BY_CLIENT.Inspector", "blockedReason": "inspector",
	}}
}

// requestScript attaches a page and a service worker, and a cross-origin
// frame once the page auto-attaches. The page and the frame send requests
// once their Network domain is enabled.
func requestScript(command cdpCommand) []cdpEvent {
	switch {
	case command.Method == "Target.setAutoAttach" && command.SessionID == "":
		return []cdpEvent{attachedTo("", "page-1", "page"), attachedTo("", "worker-1", "service_worker")}
	case command.Method == "Target.setAutoAttach" && command.SessionID == "page-1":
		return []cdpEvent{attachedTo("page-1", "frame-1", "iframe")}
	case command.Method == "Network.enable" && command.SessionID == "page-1":
		return []cdpEvent{
			requestSent("page-1", "1", "https://cdn.example.net/app.js"),
			blockedByDevTools("page-1", "1"),
			requestSent("page-1", "2", "https://CDN.example.net/logo.png"),
			blockedByDevTools("page-1", "2"),
			// A redirect is reported under the same request ID.
			requestSent("page-1", "3", "https://shop.example.com/login"),
			requestSent("page-1", "3", "https://sso.example.net/start"),
			blockedByDevTools("page-1", "3"),
			requestSent("page-1", "4", "https://shop.example.com/app.css"),
			{Method: "Network.loadingFinished", SessionID: "page-1", Params: map[string]any{"requestId": "4"}},
			requestSent("page-1", "5", "https://api.example.org/cart"),
			{Method: "Network.loadingFailed", SessionID: "page-1", Params: map[string]any{
				"requestId": "5", "errorText": "net::ERR_CONNECTION_REFUSED",
			}},
		}
	case command.Method == "Network.enable" && command.SessionID == "frame-1":
		return []cdpEvent{
			requestSent("frame-1", "f1", "https://cdn.example.net/frame.css"),
			blockedByDevTools("frame-1", "f1"),
		}
	}
	return nil
}

// Requests a client blocked in pages and their frames are counted per host,
// including the host a request was redirected to, and reported once.
func TestBlockedRequestWatcherCountsBlockedHosts(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, requestScript)
	watcher, err := browserhost.WatchBlockedRequests(context.Background(), fake.server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })

	want := map[string]int{"cdn.example.net": 3, "sso.example.net": 1}
	blocked := map[string]int{}
	require.Eventually(t, func() bool {
		taken, err := watcher.Take()
		assert.NoError(t, err)
		for host, count := range taken {
			blocked[host] += count
		}
		return maps.Equal(want, blocked)
	}, 5*time.Second, 20*time.Millisecond, "blocked so far: %v", blocked)
	taken, err := watcher.Take()
	require.NoError(t, err)
	assert.Empty(t, taken)

	enable := map[string]any{"maxTotalBufferSize": float64(0), "maxResourceBufferSize": float64(0), "maxPostDataSize": float64(0)}
	assert.Equal(t, []cdpCommand{
		{Method: "Network.enable", SessionID: "page-1", Params: enable},
		{Method: "Network.enable", SessionID: "frame-1", Params: enable},
	}, fake.received("Network.enable"), "only pages and frames are watched")
}

// A watcher that loses its connection says so, since it counts no more
// blocked requests.
func TestBlockedRequestWatcherReportsLostConnection(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, requestScript)
	watcher, err := browserhost.WatchBlockedRequests(context.Background(), fake.server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })

	fake.disconnect()
	require.Eventually(t, func() bool {
		_, err := watcher.Take()
		return err != nil
	}, 5*time.Second, 20*time.Millisecond)
}
