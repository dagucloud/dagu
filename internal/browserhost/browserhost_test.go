// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

// hungBrowserEnv makes the test binary run as a browser process that keeps
// running after Browser.close, and print its DevTools URL.
const hungBrowserEnv = "BROWSERHOST_TEST_HUNG_BROWSER"

// hungBrowserLifetime bounds a helper browser process the test failed to end.
const hungBrowserLifetime = time.Minute

func TestMain(m *testing.M) {
	if os.Getenv(hungBrowserEnv) != "" {
		fake := &fakeBrowser{hung: true}
		fake.start()
		fmt.Println(fake.server.URL)
		time.Sleep(hungBrowserLifetime)
		return
	}
	os.Exit(m.Run())
}

// fakeBrowser serves the subset of the DevTools protocol the package uses.
// Like Chrome, it stops accepting connections after Browser.close unless it
// is hung.
type fakeBrowser struct {
	server     *httptest.Server
	extensions []browserhost.Extension
	hung       bool
	mu         sync.Mutex
	methods    []string
}

func newFakeBrowser(t *testing.T, extensions ...browserhost.Extension) *fakeBrowser {
	t.Helper()
	fake := &fakeBrowser{extensions: extensions}
	fake.start()
	t.Cleanup(fake.server.Close)
	return fake
}

func newHungBrowser(t *testing.T) *fakeBrowser {
	t.Helper()
	fake := &fakeBrowser{hung: true}
	fake.start()
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeBrowser) start() {
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
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
	if request.Method == "Browser.close" && !f.hung {
		// Close waits for this handler to return.
		go f.server.Close()
	}
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
	assert.ErrorIs(t, browserhost.Probe(context.Background(), fake.server.URL), browserhost.ErrUnreachable)

	gone := closedURL(t)
	assert.ErrorIs(t, browserhost.Probe(context.Background(), gone), browserhost.ErrUnreachable)
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

// A browser that keeps running after Browser.close keeps its record and
// files, so a later sweep can try again.
func TestReleaseKeepsHungBrowser(t *testing.T) {
	t.Parallel()

	fake := newHungBrowser(t)
	store := browserhost.NewStore(t.TempDir())
	record := ownedRecord(t, fake.server.URL)
	require.NoError(t, store.Save(record))

	assert.Error(t, browserhost.Release(context.Background(), store, record))

	_, err := store.Load(record.ID)
	require.NoError(t, err)
	assert.DirExists(t, record.ExtensionDir)
	assert.DirExists(t, record.UserDataDir)
}

// A browser process that ignores Browser.close is ended through its recorded
// process ID, but only while that ID still belongs to the same process.
func TestReleaseEndsHungBrowserProcess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		startDelta int64
		released   bool
	}{
		{name: "same process", released: true},
		{name: "reused process ID", startDelta: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pid, cdpURL, exited := startHungBrowserProcess(t)
			startedAt, ok := procutil.StartTime(pid)
			require.True(t, ok)
			store := browserhost.NewStore(t.TempDir())
			record := ownedRecord(t, cdpURL)
			record.BrowserPID = pid
			record.BrowserStartedAt = startedAt + tc.startDelta
			require.NoError(t, store.Save(record))

			err := browserhost.Release(context.Background(), store, record)

			_, loadErr := store.Load(record.ID)
			if !tc.released {
				assert.Error(t, err)
				require.NoError(t, loadErr)
				assert.NoError(t, browserhost.Probe(context.Background(), cdpURL), "the process keeps running")
				return
			}
			require.NoError(t, err)
			assert.ErrorIs(t, loadErr, os.ErrNotExist)
			assert.NoDirExists(t, record.UserDataDir)
			select {
			case <-exited:
			case <-time.After(10 * time.Second):
				t.Fatal("browser process did not exit")
			}
		})
	}
}

// ownedRecord returns a session record for the browser at cdpURL that owns
// its extension and profile directories.
func ownedRecord(t *testing.T, cdpURL string) browserhost.Record {
	t.Helper()
	return browserhost.Record{
		ID:              "session",
		State:           browserhost.StateDetached,
		CDPURL:          cdpURL,
		ExtensionDir:    t.TempDir(),
		UserDataDir:     t.TempDir(),
		OwnsUserDataDir: true,
	}
}

// startHungBrowserProcess runs a browser process that ignores Browser.close
// and returns its process ID, DevTools URL, and a channel closed on exit.
func startHungBrowserProcess(t *testing.T) (int, string, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), hungBrowserEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	return cmd.Process.Pid, strings.TrimSpace(line), exited
}
