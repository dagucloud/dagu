// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
)

const waitFor = 5 * time.Second

// fakeCloud is Dagu Cloud's ticket endpoint and its relay, which hands each
// connection to the test.
type fakeCloud struct {
	console, relay *httptest.Server
	// refusal, when set, is the message tickets are refused with.
	refusal atomic.Value
	tickets atomic.Int32
	conns   chan *websocket.Conn
	// instance is the Dagu-Instance header of the last connection.
	instance atomic.Value
}

func newFakeCloud(t *testing.T) *fakeCloud {
	t.Helper()
	f := &fakeCloud{conns: make(chan *websocket.Conn, 1)}
	f.refusal.Store("")
	done := make(chan struct{})
	f.relay = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.instance.Store(r.Header.Get("Dagu-Instance"))
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		f.conns <- ws
		<-done
	}))
	f.console = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.tickets.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if refusal := f.refusal.Load().(string); refusal != "" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": refusal, "message": refusal})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "ticket", "url": "ws" + strings.TrimPrefix(f.relay.URL, "http")})
	}))
	t.Cleanup(func() {
		close(done)
		f.relay.Close()
		f.console.Close()
	})
	return f
}

func (f *fakeCloud) accept(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case ws := <-f.conns:
		return ws
	case <-time.After(waitFor):
		t.Fatal("the relay saw no connection")
		return nil
	}
}

// startAccess runs the service against cloud at a remote access level,
// serving calls with mcp, with settings saved in a file.
func startAccess(t *testing.T, cloud *fakeCloud, key cloudKey, level config.RemoteAccessLevel, mcp http.Handler) (*Service, *Settings) {
	t.Helper()
	settings := newSettingsAt(t, level)
	service, _ := runAccess(t, cloud, key, settings, testCreds, mcp)
	return service, settings
}

// runAccess runs the service against cloud with settings, serving calls with
// mcp. It reads creds, pointed at cloud, until the test replaces them
// through the returned pointer.
func runAccess(t *testing.T, cloud *fakeCloud, key cloudKey, settings *Settings, creds license.CloudCredentials,
	mcp http.Handler) (*Service, *atomic.Pointer[license.CloudCredentials]) {
	t.Helper()
	creds.CloudURL = cloud.console.URL
	var current atomic.Pointer[license.CloudCredentials]
	current.Store(&creds)
	service := New(Config{
		Credentials: func() (license.CloudCredentials, bool) { return *current.Load(), true },
		Settings:    settings,
		Key:         key.public,
	})
	service.timing = timing{check: 10 * time.Millisecond, minBackoff: 10 * time.Millisecond, maxBackoff: 10 * time.Millisecond, refused: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(ctx, mcp)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return service, &current
}

// Turning remote access on connects the process, and turning it off
// disconnects it, ending the calls in flight.
func TestRemoteAccessHoldsAConnectionWhileOn(t *testing.T) {
	cloud := newFakeCloud(t)
	key := newCloudKey(t)
	started, ended := make(chan struct{}), make(chan struct{})
	service, settings := startAccess(t, cloud, key, config.RemoteAccessOff, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(ended)
	}))
	ctx := context.Background()

	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, cloud.tickets.Load(), "nothing connects while remote access is off")

	require.NoError(t, settings.Set(ctx, new(config.RemoteAccessViewer), nil))
	ws := cloud.accept(t)
	assert.Regexp(t, `^[A-Za-z0-9_-]{22}$`, cloud.instance.Load())
	require.Eventually(t, func() bool { return service.State().Connected }, waitFor, 10*time.Millisecond)

	// The relay passes on a call, which waits until it is ended.
	id := uuid.New()
	writeJSON(t, ws, map[string]any{"t": "req", "id": id.String(), "method": http.MethodPost, "path": mcpPath,
		"assertion": key.assertion(t, testBody, nil)})
	require.NoError(t, ws.Write(ctx, websocket.MessageBinary, append(append([]byte{1}, id[:]...), testBody...)))
	writeJSON(t, ws, map[string]any{"t": "end", "id": id.String()})
	select {
	case <-started:
	case <-time.After(waitFor):
		t.Fatal("the call was not served")
	}

	require.NoError(t, settings.Set(ctx, new(config.RemoteAccessOff), nil))
	select {
	case <-ended:
	case <-time.After(waitFor):
		t.Fatal("the call in flight did not end")
	}
	readCtx, cancel := context.WithTimeout(ctx, waitFor)
	defer cancel()
	for {
		if _, _, err := ws.Read(readCtx); err != nil {
			require.NoError(t, readCtx.Err(), "the connection stayed open")
			break
		}
	}
	require.Eventually(t, func() bool { return !service.State().Connected }, waitFor, 10*time.Millisecond)
}

// A refused ticket is not asked for again soon, unless the settings change.
func TestRemoteAccessWaitsAfterARefusal(t *testing.T) {
	cloud := newFakeCloud(t)
	cloud.refusal.Store("access from Dagu Cloud needs a plan or a trial")
	service, settings := startAccess(t, cloud, newCloudKey(t), config.RemoteAccessOperator, http.NotFoundHandler())
	require.Eventually(t, func() bool {
		return service.State().Error == "Access from Dagu Cloud needs a plan or a trial."
	}, waitFor, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), cloud.tickets.Load())

	cloud.refusal.Store("")
	require.NoError(t, settings.Set(context.Background(), new(config.RemoteAccessDeveloper), nil))
	cloud.accept(t)
	require.Eventually(t, func() bool { return service.State().Connected }, waitFor, 10*time.Millisecond)
}

// A server refused for want of a plan connects as soon as its license
// changes, rather than after the refusal wait: as a trial replaces the free
// plan, or a plan replaces a trial that ended.
func TestRemoteAccessRetriesWhenTheLicenseChanges(t *testing.T) {
	free, trial, team := testCreds, testCreds, testCreds
	free.Free, free.Plan = true, "free"
	trial.Plan = "trial"
	team.Plan = "team"
	for name, change := range map[string][2]license.CloudCredentials{
		"trial starts":     {free, trial},
		"plan after trial": {trial, team},
	} {
		t.Run(name, func(t *testing.T) {
			cloud := newFakeCloud(t)
			cloud.refusal.Store("access from Dagu Cloud needs a plan or a trial")
			service, creds := runAccess(t, cloud, newCloudKey(t), newSettingsAt(t, config.RemoteAccessOperator), change[0], http.NotFoundHandler())
			require.Eventually(t, func() bool { return service.State().Error != "" }, waitFor, 10*time.Millisecond)
			time.Sleep(50 * time.Millisecond)
			assert.Equal(t, int32(1), cloud.tickets.Load(), "the same license is not asked about again soon")

			cloud.refusal.Store("")
			after := change[1]
			after.CloudURL = creds.Load().CloudURL
			creds.Store(&after)
			cloud.accept(t)
			require.Eventually(t, func() bool { return service.State().Connected }, waitFor, 10*time.Millisecond)
		})
	}
}

// While a refusal holds, a moment the settings cannot be read does not hide
// why Dagu Cloud refused the server once they can be read again.
func TestRemoteAccessKeepsTheRefusalAfterASettingsError(t *testing.T) {
	cloud := newFakeCloud(t)
	cloud.refusal.Store("access from Dagu Cloud needs a plan or a trial")
	store := &failingStore{StateStore: filemonitor.NewStateStore(filepath.Join(t.TempDir(), "access-settings.json"))}
	settings := NewSettings(config.CloudConfig{}, store)
	require.NoError(t, settings.Set(context.Background(), new(config.RemoteAccessOperator), nil))
	// Each read goes to the store rather than the cache.
	var ticks atomic.Int64
	settings.now = func() time.Time { return time.Now().Add(time.Duration(ticks.Add(1)) * settingsTTL) }
	service, _ := runAccess(t, cloud, newCloudKey(t), settings, testCreds, http.NotFoundHandler())
	refusal := "Access from Dagu Cloud needs a plan or a trial."
	require.Eventually(t, func() bool { return service.State().Error == refusal }, waitFor, 10*time.Millisecond)

	store.failing.Store(true)
	require.Eventually(t, func() bool { return service.State().Error == stateUnreadable }, waitFor, 10*time.Millisecond)
	store.failing.Store(false)
	require.Eventually(t, func() bool { return service.State().Error == refusal }, waitFor, 10*time.Millisecond)
	assert.Equal(t, int32(1), cloud.tickets.Load())
}

// newSettingsAt returns settings saved in a file, at a remote access level.
func newSettingsAt(t *testing.T, level config.RemoteAccessLevel) *Settings {
	t.Helper()
	settings := NewSettings(config.CloudConfig{}, filemonitor.NewStateStore(filepath.Join(t.TempDir(), "access-settings.json")))
	require.NoError(t, settings.Set(context.Background(), new(level), nil))
	return settings
}

// failingStore fails every load while failing is set.
type failingStore struct {
	StateStore
	failing atomic.Bool
}

func (s *failingStore) Load(ctx context.Context) ([]byte, bool, error) {
	if s.failing.Load() {
		return nil, false, errors.New("disk unavailable")
	}
	return s.StateStore.Load(ctx)
}

func writeJSON(t *testing.T, ws *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, ws.Write(context.Background(), websocket.MessageText, data))
}
