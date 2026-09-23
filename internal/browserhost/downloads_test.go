// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// downloadEvent is one Browser download event a fake browser sends.
type downloadEvent struct {
	method string
	params map[string]any
}

// serveDownloads acknowledges the download behavior request and then sends
// events to the client.
func serveDownloads(t *testing.T, events []downloadEvent) string {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
			_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": wsURL})
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx := r.Context()
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		ack, _ := json.Marshal(map[string]any{"id": 1, "result": map[string]any{}})
		_ = conn.Write(ctx, websocket.MessageText, ack)
		for _, event := range events {
			time.Sleep(50 * time.Millisecond)
			data, _ := json.Marshal(map[string]any{"method": event.method, "params": event.params})
			_ = conn.Write(ctx, websocket.MessageText, data)
		}
		<-ctx.Done()
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func begin(guid, name string) downloadEvent {
	return downloadEvent{"Browser.downloadWillBegin", map[string]any{"guid": guid, "suggestedFilename": name}}
}

func progress(guid, state string) downloadEvent {
	return downloadEvent{"Browser.downloadProgress", map[string]any{"guid": guid, "state": state}}
}

// A finished download is renamed from its guid to the suggested name, and a
// second file with the same name gets a unique one.
func TestDownloadWatcherSavesFinishedDownloads(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, guid := range []string{"g1", "g2"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, guid), []byte(guid), 0o600))
	}
	cdpURL := serveDownloads(t, []downloadEvent{
		begin("g1", "invoice.pdf"), progress("g1", "inProgress"), progress("g1", "completed"),
		begin("g2", "../invoice.pdf"), progress("g2", "completed"),
	})
	watcher, err := browserhost.WatchDownloads(context.Background(), cdpURL, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = watcher.Close() })

	downloads, err := watcher.Wait(context.Background(), time.Second, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, downloads, 2)
	assert.Equal(t, "invoice.pdf", downloads[0].Name)
	assert.Equal(t, "invoice-1.pdf", downloads[1].Name)
	data, err := os.ReadFile(filepath.Join(dir, "invoice-1.pdf"))
	require.NoError(t, err)
	assert.Equal(t, "g2", string(data))
}

func TestDownloadWatcherReportsFailedDownloads(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		events []downloadEvent
		want   string
	}{
		{
			name:   "canceled",
			events: []downloadEvent{begin("g1", "report.csv"), progress("g1", "canceled")},
			want:   "download of report.csv was canceled",
		},
		{
			name:   "unfinished",
			events: []downloadEvent{begin("g1", "report.csv"), progress("g1", "inProgress")},
			want:   "download of report.csv did not finish",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			watcher, err := browserhost.WatchDownloads(context.Background(), serveDownloads(t, tc.events), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { _ = watcher.Close() })

			_, err = watcher.Wait(context.Background(), 500*time.Millisecond, time.Second)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}
