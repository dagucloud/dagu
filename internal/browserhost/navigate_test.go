// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/require"
)

// loadingPage answers Navigate's commands as a page whose load of
// newLoader brings a document that reads readyState, with no events unless
// script sends them.
func loadingPage(t *testing.T, readyState string, script func(cdpCommand) []cdpEvent) *scriptedBrowser {
	t.Helper()
	const newLoader = "loader-next"
	return newAnsweringBrowser(t, script, func(command cdpCommand) map[string]any {
		switch command.Method {
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "page"}
		case "Page.navigate":
			return map[string]any{"frameId": "main", "loaderId": newLoader}
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main", "loaderId": newLoader}}}
		case "Runtime.evaluate":
			return map[string]any{"result": map[string]any{"type": "string", "value": readyState}}
		}
		return nil
	})
}

// A load that replaces one still in flight brings a document the page may
// show and parse before it takes in the commands that report its loads.
// The goto still returns once the page has parsed it.
func TestNavigateReturnsForADocumentParsedBeforeItsEventsWereOn(t *testing.T) {
	t.Parallel()

	fake := loadingPage(t, "complete", func(cdpCommand) []cdpEvent { return nil })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	load, err := browserhost.Navigate(ctx, fake.server.URL, "target", "http://site.test/next")
	require.NoError(t, err)
	require.True(t, load.Document)
}

// A document still loading when the goto reads the page is waited on until
// the page reports it parsed.
func TestNavigateWaitsForADocumentStillLoading(t *testing.T) {
	t.Parallel()

	fake := loadingPage(t, "loading", func(command cdpCommand) []cdpEvent {
		if command.Method != "Runtime.evaluate" {
			return nil
		}
		return []cdpEvent{{Method: "Page.lifecycleEvent", SessionID: "page", Params: map[string]any{
			"frameId": "main", "loaderId": "loader-next", "name": "DOMContentLoaded",
		}}}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	load, err := browserhost.Navigate(ctx, fake.server.URL, "target", "http://site.test/next")
	require.NoError(t, err)
	require.True(t, load.Document)
}

// A document a new one replaces while the goto reads it is read again, so
// the goto returns once the page has parsed the one that replaced it.
func TestNavigateReadsAgainADocumentReplacedWhileItReads(t *testing.T) {
	t.Parallel()

	var reads, evaluations atomic.Int32
	fake := startScriptedBrowser(t, &scriptedBrowser{
		script: func(cdpCommand) []cdpEvent { return nil },
		answer: func(command cdpCommand) map[string]any {
			switch command.Method {
			case "Target.attachToTarget":
				return map[string]any{"sessionId": "page"}
			case "Page.navigate":
				return map[string]any{"frameId": "main", "loaderId": "loader-next"}
			case "Page.getFrameTree":
				// The first read finds the page still showing the document
				// the load replaces.
				loader := "loader-next"
				if reads.Add(1) == 1 {
					loader = "loader-start"
				}
				return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main", "loaderId": loader}}}
			case "Runtime.evaluate":
				return map[string]any{"result": map[string]any{"type": "string", "value": "complete"}}
			}
			return nil
		},
		refuse: func(command cdpCommand) string {
			if command.Method == "Runtime.evaluate" && evaluations.Add(1) == 1 {
				return "Execution context was destroyed."
			}
			return ""
		},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	load, err := browserhost.Navigate(ctx, fake.server.URL, "target", "http://site.test/next")
	require.NoError(t, err)
	require.True(t, load.Document)
	require.Equal(t, int32(2), reads.Load(), "the page is read again after the document it showed was replaced")
}

// A page read between two documents has none to read, and the goto waits
// for the next one's events, which it took in the commands for.
func TestNavigateWaitsForEventsWhenThePageShowsNoDocument(t *testing.T) {
	t.Parallel()

	fake := startScriptedBrowser(t, &scriptedBrowser{
		script: func(command cdpCommand) []cdpEvent {
			if command.Method != "Runtime.evaluate" {
				return nil
			}
			return []cdpEvent{{Method: "Page.lifecycleEvent", SessionID: "page", Params: map[string]any{
				"frameId": "main", "loaderId": "loader-next", "name": "DOMContentLoaded",
			}}}
		},
		answer: func(command cdpCommand) map[string]any {
			switch command.Method {
			case "Target.attachToTarget":
				return map[string]any{"sessionId": "page"}
			case "Page.navigate":
				return map[string]any{"frameId": "main", "loaderId": "loader-next"}
			case "Page.getFrameTree":
				return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main", "loaderId": "loader-next"}}}
			}
			return nil
		},
		refuse: func(command cdpCommand) string {
			if command.Method == "Runtime.evaluate" {
				return "Cannot find default execution context"
			}
			return ""
		},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	load, err := browserhost.Navigate(ctx, fake.server.URL, "target", "http://site.test/next")
	require.NoError(t, err)
	require.True(t, load.Document)
}
