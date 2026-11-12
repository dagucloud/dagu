// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chooserScript reports a page, a service worker, and a cross-origin frame
// under the page as auto-attach reaches them.
func chooserScript(command cdpCommand) []cdpEvent {
	if command.Method != "Target.setAutoAttach" {
		return nil
	}
	switch command.SessionID {
	case "":
		return []cdpEvent{attachedTo("", "page-1", "page"), attachedTo("", "worker-1", "service_worker")}
	case "page-1":
		return []cdpEvent{attachedTo("page-1", "frame-1", "iframe")}
	}
	return nil
}

// chooserOpened is the event of a page or frame opening a file picker for
// the file input with backendNodeID.
func chooserOpened(session, mode string, backendNodeID int) cdpEvent {
	return cdpEvent{Method: "Page.fileChooserOpened", SessionID: session, Params: map[string]any{
		"frameId": "frame", "mode": mode, "backendNodeId": backendNodeID,
	}}
}

// interceptingSessions lists the sessions told to intercept file pickers.
func interceptingSessions(fake *scriptedBrowser) []string {
	var sessions []string
	for _, command := range fake.received("Page.setInterceptFileChooserDialog") {
		sessions = append(sessions, command.SessionID)
	}
	return sessions
}

// Every page and frame intercepts its pickers before the click that opens
// one, and the picker's file input gets the files.
func TestChooseFilesGivesTheFilesToThePicker(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, chooserScript)
	paths := []string{"/data/invoice.pdf", "/data/receipt.png"}
	var armed []string
	err := browserhost.ChooseFiles(context.Background(), fake.server.URL, paths, 5*time.Second, func(context.Context) error {
		armed = interceptingSessions(fake)
		fake.emit(chooserOpened("frame-1", "selectMultiple", 42))
		return nil
	})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"page-1", "frame-1"}, armed, "pages and frames intercept pickers before the click")
	assert.ElementsMatch(t, []string{"page-1", "frame-1"}, sessionsOf(fake.received("Page.enable")))
	assert.Equal(t, []cdpCommand{{
		Method: "DOM.setFileInputFiles", SessionID: "frame-1",
		Params: map[string]any{"files": []any{"/data/invoice.pdf", "/data/receipt.png"}, "backendNodeId": float64(42)},
	}}, fake.received("DOM.setFileInputFiles"))
}

func TestChooseFilesRefusesSeveralFilesForASingleFilePicker(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, chooserScript)
	err := browserhost.ChooseFiles(context.Background(), fake.server.URL, []string{"/a.pdf", "/b.pdf"}, 5*time.Second, func(context.Context) error {
		fake.emit(chooserOpened("page-1", "selectSingle", 7))
		return nil
	})
	require.ErrorIs(t, err, browserhost.ErrSingleFileChooser)
	assert.Empty(t, fake.received("DOM.setFileInputFiles"), "no file is given")
}

// A target that cannot be armed fails before the click, unless it closed
// meanwhile, since a closed target opens no picker.
func TestChooseFilesReportsATargetThatCannotIntercept(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, refusal string
		opens         bool
	}{
		{name: "refused", refusal: "Not allowed"},
		{name: "closed", refusal: "Session with given id not found", opens: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := newRefusingBrowser(t, chooserScript, func(command cdpCommand) string {
				if command.Method == "Page.setInterceptFileChooserDialog" && command.SessionID == "frame-1" {
					return tc.refusal
				}
				return ""
			})
			opened := false
			err := browserhost.ChooseFiles(context.Background(), fake.server.URL, []string{"/a.pdf"}, 50*time.Millisecond, func(context.Context) error {
				opened = true
				return nil
			})
			assert.Equal(t, tc.opens, opened)
			if tc.opens {
				require.ErrorIs(t, err, browserhost.ErrNoFileChooser)
				return
			}
			require.ErrorContains(t, err, "intercept file pickers: Page.setInterceptFileChooserDialog: Not allowed")
		})
	}
}

func TestChooseFilesReportsNoPicker(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, chooserScript)
	err := browserhost.ChooseFiles(context.Background(), fake.server.URL, []string{"/a.pdf"}, 50*time.Millisecond, func(context.Context) error { return nil })
	require.ErrorIs(t, err, browserhost.ErrNoFileChooser)
}

func TestChooseFilesReturnsTheOpenError(t *testing.T) {
	t.Parallel()

	fake := newScriptedBrowser(t, chooserScript)
	clickErr := errors.New("element is not visible")
	err := browserhost.ChooseFiles(context.Background(), fake.server.URL, []string{"/a.pdf"}, time.Second, func(context.Context) error { return clickErr })
	require.ErrorIs(t, err, clickErr)
}

func sessionsOf(commands []cdpCommand) []string {
	sessions := make([]string, 0, len(commands))
	for _, command := range commands {
		sessions = append(sessions, command.SessionID)
	}
	return sessions
}
