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

// windowAttached reports a page attached as session that the page of
// opener opened.
func windowAttached(session, target, opener string) cdpEvent {
	return cdpEvent{Method: "Target.attachedToTarget", Params: map[string]any{
		"sessionId": session, "targetInfo": map[string]any{"type": "page", "targetId": target, "openerId": opener},
	}}
}

// eventReader returns a function that sends events to a frame load watcher
// of fake and returns once the watcher has read them. The watcher reads
// events in the order they are sent, so once it has enabled a page attached
// after them, it has read them all.
func eventReader(t *testing.T, fake *scriptedBrowser) func(events ...cdpEvent) {
	t.Helper()
	marks := 0
	return func(events ...cdpEvent) {
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
}

// The browser is loading while a frame of any of its pages loads a
// document, until each has stopped or is gone, or its page has closed.
func TestFrameLoadWatcherTellsLoadingFrames(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, func(command cdpCommand) []cdpEvent {
		// The browser's pages attach to the browser's own session; a page's
		// session has no frames of its own here.
		if command.Method == "Target.setAutoAttach" && command.SessionID == "" {
			return []cdpEvent{pageAttached("page-1", "target-1"), pageAttached("page-2", "target-2")}
		}
		return nil
	})
	watcher, err := browserhost.WatchFrameLoads(context.Background(), fake.server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })
	read := eventReader(t, fake)

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

// A cross-origin frame is a target of its own under its page, whose loads
// count once the watcher attaches to it and enables its Page domain.
func TestFrameLoadWatcherTellsCrossOriginFrames(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, func(command cdpCommand) []cdpEvent {
		switch {
		case command.Method == "Target.setAutoAttach" && command.SessionID == "":
			return []cdpEvent{pageAttached("page-1", "target-1")}
		case command.Method == "Target.setAutoAttach" && command.SessionID == "page-1":
			return []cdpEvent{attachedTo("page-1", "ad-frame", "iframe")}
		case command.Method == "Page.enable" && command.SessionID == "ad-frame":
			return []cdpEvent{frameEvent("Page.frameStartedLoading", "ad-frame", "main")}
		}
		return nil
	})
	watcher, err := browserhost.WatchFrameLoads(context.Background(), fake.server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })

	require.Eventually(t, watcher.Loading, 5*time.Second, 20*time.Millisecond)
	fake.emit(frameEvent("Page.frameStoppedLoading", "ad-frame", "main"))
	require.Eventually(t, func() bool { return !watcher.Loading() }, 5*time.Second, 20*time.Millisecond)
}

// A window is listed with the page that opened it while both are open, and
// a page's loads are told by the page: those of its frames, cross-origin
// ones too, when the latest began, and when the page stopped loading.
func TestFrameLoadWatcherTellsWindows(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, func(command cdpCommand) []cdpEvent {
		if command.Method == "Target.setAutoAttach" && command.SessionID == "" {
			return []cdpEvent{pageAttached("page-1", "target-1")}
		}
		return nil
	})
	watcher, err := browserhost.WatchFrameLoads(context.Background(), fake.server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })
	read := eventReader(t, fake)

	read(windowAttached("page-2", "target-2", "target-1"), windowAttached("page-3", "target-3", "closed-target"))
	windows := watcher.Windows()
	require.Len(t, windows, 1, "a window whose opener is gone stands alone")
	assert.Equal(t, "target-2", windows[0].TargetID)
	assert.Equal(t, "target-1", windows[0].Opener)
	assert.Equal(t, browserhost.PageLoad{}, watcher.PageLoad("target-1"), "nothing loaded yet")

	read(attachedTo("page-1", "ad-frame", "iframe"), frameEvent("Page.frameStartedLoading", "ad-frame", "main"))
	load := watcher.PageLoad("target-1")
	assert.True(t, load.Loading, "a cross-origin frame of the page loads")
	assert.False(t, load.Began.Before(windows[0].Opened), "the load began after the window opened")
	assert.True(t, load.Ended.IsZero())
	assert.False(t, watcher.PageLoad("target-2").Loading, "the window loads nothing")

	read(frameEvent("Page.frameStoppedLoading", "ad-frame", "main"))
	stopped := watcher.PageLoad("target-1")
	assert.False(t, stopped.Loading)
	assert.Equal(t, load.Began, stopped.Began)
	assert.False(t, stopped.Ended.Before(stopped.Began), "the page stopped loading after it began")

	read(cdpEvent{Method: "Target.detachedFromTarget", Params: map[string]any{"sessionId": "page-2"}})
	assert.Empty(t, watcher.Windows(), "a window that closed")
	read(cdpEvent{Method: "Target.detachedFromTarget", Params: map[string]any{"sessionId": "page-1"}})
	assert.Equal(t, browserhost.PageLoad{}, watcher.PageLoad("target-1"), "a page that closed")
}
