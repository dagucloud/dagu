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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBrowser serves the subset of the DevTools protocol the package uses.
type fakeBrowser struct {
	server     *httptest.Server
	extensions []browserhost.Extension
	mu         sync.Mutex
	methods    []string
}

func newFakeBrowser(t *testing.T, extensions ...browserhost.Extension) *fakeBrowser {
	t.Helper()
	fake := &fakeBrowser{extensions: extensions}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeBrowser) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/json/version" {
		wsURL := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/devtools/browser/test"
		_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": wsURL})
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	_, data, err := conn.Read(r.Context())
	if err != nil {
		return
	}
	var request struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(data, &request)
	f.mu.Lock()
	f.methods = append(f.methods, request.Method)
	f.mu.Unlock()
	result := map[string]any{}
	if request.Method == "Extensions.getExtensions" {
		result["extensions"] = f.extensions
	}
	response, _ := json.Marshal(map[string]any{"id": request.ID, "result": result})
	_ = conn.Write(r.Context(), websocket.MessageText, response)
}

func (f *fakeBrowser) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

// closedURL returns a DevTools URL nothing listens on.
func closedURL(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	return url
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()

	store := browserhost.NewStore(t.TempDir())
	record := browserhost.Record{
		ID:       browserhost.RecordID("run-1", "login"),
		DAGRunID: "run-1",
		StepName: "login",
		State:    browserhost.StateDetached,
		CDPURL:   "http://127.0.0.1:9222",
		Cursor:   3,
		Outputs:  map[string]any{"total": 12.5},
	}
	require.NoError(t, store.Save(record))

	loaded, err := store.Load(record.ID)
	require.NoError(t, err)
	assert.Equal(t, record, loaded)

	listed, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, []browserhost.Record{record}, listed)

	require.NoError(t, store.Delete(record.ID))
	_, err = store.Load(record.ID)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NoError(t, store.Delete(record.ID), "deleting a missing record is not an error")
}

func TestStoreKeepsRecordsPrivate(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}

	dir := t.TempDir()
	store := browserhost.NewStore(dir)
	record := browserhost.Record{ID: "private"}
	require.NoError(t, store.Save(record))

	info, err := os.Stat(filepath.Join(dir, "sessions", "private.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestStagehandExtension(t *testing.T) {
	t.Parallel()

	fake := newFakeBrowser(t,
		browserhost.Extension{ID: "other", Name: "Other", Path: "/x", Enabled: true},
		browserhost.Extension{ID: "disabled", Name: browserhost.StagehandExtensionName, Path: "/y", Enabled: false},
		browserhost.Extension{ID: "runtime", Name: browserhost.StagehandExtensionName, Path: "/z", Enabled: true},
	)
	extension, err := browserhost.StagehandExtension(context.Background(), fake.server.URL)
	require.NoError(t, err)
	assert.Equal(t, "runtime", extension.ID)
	assert.Equal(t, "/z", extension.Path)

	empty := newFakeBrowser(t)
	_, err = browserhost.StagehandExtension(context.Background(), empty.server.URL)
	assert.ErrorIs(t, err, browserhost.ErrExtensionNotFound)
}

func TestProbeAndClose(t *testing.T) {
	t.Parallel()

	fake := newFakeBrowser(t)
	require.NoError(t, browserhost.Probe(context.Background(), fake.server.URL))
	require.NoError(t, browserhost.CloseBrowser(context.Background(), fake.server.URL))
	assert.Equal(t, []string{"Browser.close"}, fake.calls())

	gone := closedURL(t)
	assert.Error(t, browserhost.Probe(context.Background(), gone))
	assert.NoError(t, browserhost.CloseBrowser(context.Background(), gone), "an unreachable browser is already closed")
}

// Sweep keeps sessions a step can still use and releases every other one.
func TestSweep(t *testing.T) {
	t.Parallel()

	now := time.Now()
	startedAt, _ := procutil.StartTime(os.Getpid())
	resumable := func(_ context.Context, record browserhost.Record) bool {
		return record.StepName != "cancelled"
	}

	for _, tc := range []struct {
		name   string
		record browserhost.Record
		kept   bool
	}{
		{
			name:   "running with live owner",
			record: browserhost.Record{State: browserhost.StateRunning, OwnerPID: os.Getpid(), OwnerStartedAt: startedAt},
			kept:   true,
		},
		{
			name:   "running with dead owner",
			record: browserhost.Record{State: browserhost.StateRunning, OwnerPID: 1 << 30},
		},
		{
			name:   "detached before deadline",
			record: browserhost.Record{State: browserhost.StateDetached, Deadline: now.Add(time.Hour)},
			kept:   true,
		},
		{
			name:   "detached past deadline",
			record: browserhost.Record{State: browserhost.StateDetached, Deadline: now.Add(-time.Minute)},
		},
		{
			name:   "detached for a step that cannot resume",
			record: browserhost.Record{State: browserhost.StateDetached, Deadline: now.Add(time.Hour), StepName: "cancelled"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeBrowser(t)
			store := browserhost.NewStore(t.TempDir())
			extensionDir := t.TempDir()
			userDataDir := t.TempDir()
			record := tc.record
			record.ID = "session"
			record.CDPURL = fake.server.URL
			record.ExtensionDir = extensionDir
			record.UserDataDir = userDataDir
			record.OwnsUserDataDir = true
			require.NoError(t, store.Save(record))

			require.NoError(t, browserhost.Sweep(context.Background(), store, now, resumable))

			_, err := store.Load(record.ID)
			if tc.kept {
				require.NoError(t, err)
				assert.Empty(t, fake.calls())
				assert.DirExists(t, userDataDir)
				return
			}
			assert.ErrorIs(t, err, os.ErrNotExist)
			assert.Equal(t, []string{"Browser.close"}, fake.calls())
			assert.NoDirExists(t, extensionDir)
			assert.NoDirExists(t, userDataDir)
		})
	}
}
