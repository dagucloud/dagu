// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestSSEHeartbeatRepeats(t *testing.T) {
	handler := withSSEHeartbeat(newStreamableHandler(NewServer(nil)), 10*time.Millisecond)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), testConnectTimeout)
	defer cancel()
	session := connectHTTPTestClient(t, ctx, httpServer.URL, nil, false)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", session.ID())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	stream := bufio.NewReader(resp.Body)
	for heartbeats := 0; heartbeats < 3; {
		line, err := stream.ReadString('\n')
		require.NoError(t, err)
		// The SDK opens the stream with a comment of its own.
		if line != ": ok\n" {
			require.Equal(t, ": heartbeat\n", line)
			heartbeats++
		}
		blank, err := stream.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "\n", blank)
	}
}

// Heartbeats share the stream with server-pushed messages, so a notification
// must still reach the client intact while heartbeats are written often.
func TestSSEHeartbeatKeepsNotifications(t *testing.T) {
	const uri = "dagu://reference/tools"
	server := NewServer(nil)
	httpServer := httptest.NewServer(withSSEHeartbeat(newStreamableHandler(server), time.Millisecond))
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), testConnectTimeout)
	defer cancel()
	updated := make(chan string, 1)
	session := connectHTTPTestClient(t, ctx, httpServer.URL, &mcpsdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *mcpsdk.ResourceUpdatedNotificationRequest) {
			select {
			case updated <- req.Params.URI:
			default:
			}
		},
	}, true)
	require.NoError(t, session.Subscribe(ctx, &mcpsdk.SubscribeParams{URI: uri}))

	// The client opens its event stream in the background, so retry until a
	// notification is delivered on it.
	require.Eventually(t, func() bool {
		if err := server.ResourceUpdated(ctx, &mcpsdk.ResourceUpdatedNotificationParams{URI: uri}); err != nil {
			return false
		}
		select {
		case got := <-updated:
			return got == uri
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}, testConnectTimeout, 10*time.Millisecond)
}

// connectHTTPTestClient connects over Streamable HTTP. Without eventStream the
// client leaves the standalone event stream for the test to open itself.
func connectHTTPTestClient(t *testing.T, ctx context.Context, endpoint string, opts *mcpsdk.ClientOptions, eventStream bool) *mcpsdk.ClientSession {
	t.Helper()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "dagu-mcp-test", Version: "v0.0.0"}, opts)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             endpoint,
		DisableStandaloneSSE: !eventStream,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}
