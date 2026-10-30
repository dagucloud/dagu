// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frameEvent reports a change to the load of frame, a frame of the page
// attached as session.
func frameEvent(method, session, frame string) cdpEvent {
	return cdpEvent{Method: method, SessionID: session, Params: map[string]any{"frameId": frame}}
}

// The browser is loading while a frame of any of its pages loads a
// document, until each has stopped or is gone, or its page has closed.
func TestFrameLoadWatcherTellsLoadingFrames(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, func(command cdpCommand) []cdpEvent {
		if command.Method == "Target.setAutoAttach" {
			return []cdpEvent{pageAttached("page-1", "target-1"), pageAttached("page-2", "target-2")}
		}
		return nil
	})
	watcher, err := browserhost.WatchFrameLoads(context.Background(), fake.server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })
	// The watcher reads events in the order they are sent, so once it has
	// enabled a page attached after them, it has read them all.
	marks := 0
	read := func(events ...cdpEvent) {
		t.Helper()
		marks++
		mark := fmt.Sprintf("mark-%d", marks)
		for _, event := range append(events, pageAttached(mark, mark)) {
			fake.emit(event)
		}
		require.Eventually(t, func() bool {
			return slices.ContainsFunc(fake.received("Page.enable"), func(c cdpCommand) bool { return c.SessionID == mark })
		}, 5*time.Second, 20*time.Millisecond)
	}

	read(frameEvent("Page.frameStartedLoading", "page-1", "main"), frameEvent("Page.frameStartedLoading", "page-1", "results"))
	assert.True(t, watcher.Loading())
	read(frameEvent("Page.frameStoppedLoading", "page-1", "main"))
	assert.True(t, watcher.Loading(), "the results frame still loads")
	read(frameEvent("Page.frameDetached", "page-1", "results"))
	assert.False(t, watcher.Loading(), "a frame that is gone loads nothing")

	read(frameEvent("Page.frameStartedLoading", "page-2", "main"))
	assert.True(t, watcher.Loading(), "another page's frame")
	read(cdpEvent{Method: "Target.detachedFromTarget", Params: map[string]any{"sessionId": "page-2"}})
	assert.False(t, watcher.Loading(), "a page that closed loads nothing")
}
