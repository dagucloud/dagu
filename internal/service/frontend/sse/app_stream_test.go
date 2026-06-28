// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAppEventFrame(t *testing.T) {
	recorder := httptest.NewRecorder()

	err := writeAppEventFrame(recorder, AppEvent{
		Type:     AppEventTypeDAGChanged,
		FileName: "example.yaml",
		Reason:   "updated",
	})
	require.NoError(t, err)

	body := recorder.Body.String()
	assert.Contains(t, body, "event: dag.changed\n")
	assert.Contains(t, body, `"fileName":"example.yaml"`)
	assert.Contains(t, body, `"reason":"updated"`)
}

func TestDirectoryWatcherStopIsIdempotent(_ *testing.T) {
	watcher := &directoryWatcher{
		done: make(chan struct{}),
	}

	// Calling Stop twice asserts the method is idempotent and does not panic.
	watcher.Stop()
	watcher.Stop()
}

func TestAppStreamServiceShutdownIsIdempotent(_ *testing.T) {
	service := &AppStreamService{
		cancel: func() {},
		watchers: []appWatcher{
			&directoryWatcher{done: make(chan struct{})},
		},
	}

	// Calling Shutdown twice asserts the method is idempotent and does not panic.
	service.Shutdown()
	service.Shutdown()
}

func TestAppHandlerHandleStreamUnavailable(t *testing.T) {
	handler := NewAppHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/app", nil)
	recorder := httptest.NewRecorder()

	handler.HandleStream(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); strings.Contains(got, "text/event-stream") {
		t.Fatalf("expected non-stream error response, got Content-Type %q", got)
	}
}
