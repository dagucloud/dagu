// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package publicwebhook_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cloudrelay"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
	"github.com/dagucloud/dagu/v2/internal/webhooksig"
)

// fakeCloud is Dagu Cloud's ticket endpoint and relay in one server.
type fakeCloud struct {
	srv      *httptest.Server
	conns    chan *websocket.Conn
	done     chan struct{}
	tickets  atomic.Int32
	refuse   atomic.Bool
	maxHooks int
}

func newFakeCloud(t *testing.T, maxHooks int) *fakeCloud {
	t.Helper()
	c := &fakeCloud{conns: make(chan *websocket.Conn, 4), done: make(chan struct{}), maxHooks: maxHooks}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/servers/relay":
			c.tickets.Add(1)
			if c.refuse.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","message":"unauthorized"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "ticket", "url": "ws" + strings.TrimPrefix(c.srv.URL, "http") + "/server", "max_hooks": c.maxHooks,
			})
		case "/server":
			if r.Header.Get("Authorization") != "Bearer ticket" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			c.conns <- ws
			<-c.done
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() {
		close(c.done)
		c.srv.Close()
	})
	return c
}

// relayEnd is the relay's end of a connection: every frame the service
// sends arrives on frames, read by one goroutine, since a read that times out
// closes the connection.
type relayEnd struct {
	ws     *websocket.Conn
	frames chan map[string]any
	closed chan error
}

// relay waits for the service to connect and returns the relay's end, after
// its hello.
func (c *fakeCloud) relay(t *testing.T) *relayEnd {
	t.Helper()
	var ws *websocket.Conn
	select {
	case ws = <-c.conns:
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not connect")
	}
	r := &relayEnd{ws: ws, frames: make(chan map[string]any, 64), closed: make(chan error, 1)}
	go func() {
		for {
			_, data, err := ws.Read(context.Background())
			if err != nil {
				r.closed <- err
				close(r.frames)
				return
			}
			if string(data) == "ping" {
				continue
			}
			var f map[string]any
			if json.Unmarshal(data, &f) == nil {
				r.frames <- f
			}
		}
	}()
	assert.Equal(t, "hello", r.next(t, "hello")["t"])
	return r
}

// next returns the next frame of type kind, skipping others.
func (r *relayEnd) next(t *testing.T, kind string) map[string]any {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-r.frames:
			require.True(t, ok, "the connection ended before a %s frame", kind)
			if f["t"] == kind {
				return f
			}
		case <-timeout:
			t.Fatalf("no %s frame", kind)
		}
	}
}

// none checks that no frame of type kind arrives for d.
func (r *relayEnd) none(t *testing.T, kind string, d time.Duration) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case f, ok := <-r.frames:
			if !ok {
				return
			}
			assert.NotEqual(t, kind, f["t"], "an unexpected %s frame: %v", kind, f)
		case <-timeout:
			return
		}
	}
}

func (r *relayEnd) write(t *testing.T, f any) {
	t.Helper()
	data, err := json.Marshal(f)
	require.NoError(t, err)
	require.NoError(t, r.ws.Write(context.Background(), websocket.MessageText, data))
}

// waitClosed waits for the service to end the connection.
func (r *relayEnd) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-r.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection stayed open")
	}
}

func digestOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// fakeRuns records the runs deliveries queue.
type fakeRuns struct {
	mu       sync.Mutex
	queued   int
	err      error
	enqueued []enqueued
}

type enqueued struct {
	dagName, runID, params string
}

func (r *fakeRuns) DAG(_ context.Context, dagName string) (*ir.DAG, error) {
	if dagName == "missing" {
		return nil, errors.New("DAG missing not found")
	}
	return &ir.DAG{Name: dagName, Webhook: &ir.WebhookConfig{ForwardHeaders: []string{"x-github-event"}}}, nil
}

func (r *fakeRuns) Queued(context.Context, *ir.DAG) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queued, nil
}

func (r *fakeRuns) Enqueue(_ context.Context, dagName string, _ *ir.DAG, runID, params string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.enqueued = append(r.enqueued, enqueued{dagName, runID, params})
	return nil
}

func (r *fakeRuns) set(queued int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queued, r.err = queued, err
}

type harness struct {
	cloud    *fakeCloud
	col      persis.Collection
	webhooks *store.WebhookStore
	log      *store.PublicWebhookStore
	runs     *fakeRuns
	svc      *publicwebhook.Service
	// offline makes the server not connected to Dagu Cloud.
	offline atomic.Bool
}

// testMaxPayloadSize is the largest body the harness's service starts a run
// for.
const testMaxPayloadSize = 1024

func newHarness(t *testing.T, maxHooks int) *harness {
	t.Helper()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	backend := testutil.NewMemoryBackend()
	col := backend.Collection("webhooks")
	webhooks, err := store.NewWebhookStore(col, backend.Collection("webhook_dag_claims"), enc)
	require.NoError(t, err)
	h := &harness{
		cloud:    newFakeCloud(t, maxHooks),
		col:      col,
		webhooks: webhooks,
		log:      store.NewPublicWebhookStore(testutil.NewMemoryBackend().Collection("public_webhooks")),
		runs:     &fakeRuns{},
	}
	h.svc = publicwebhook.New(publicwebhook.Config{
		Credentials: func() (license.CloudCredentials, bool) {
			return license.CloudCredentials{CloudURL: h.cloud.srv.URL, LicenseID: "lic", ServerID: "srv", HeartbeatSecret: "s"}, !h.offline.Load()
		},
		Webhooks:       webhooks,
		Store:          h.log,
		Runs:           h.runs,
		HTTPClient:     h.cloud.srv.Client(),
		Hello:          cloudrelay.Hello{Name: "etl-prod-1", Version: "2.20.0"},
		MaxPayloadSize: testMaxPayloadSize,
	})
	h.svc.SetTimingForTest(20*time.Millisecond, 10*time.Millisecond, time.Hour)
	return h
}

// addWebhook creates dagName's webhook, with a public URL unless public is nil.
func (h *harness) addWebhook(t *testing.T, dagName string, public *auth.WebhookPublic) *auth.Webhook {
	t.Helper()
	wh, err := auth.NewWebhook(dagName, "hash", "dagu_wh_", "admin")
	require.NoError(t, err)
	wh.Public = public
	require.NoError(t, h.webhooks.Create(context.Background(), wh))
	return wh
}

func (h *harness) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.svc.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func (h *harness) status(t *testing.T) publicwebhook.Status {
	t.Helper()
	status, err := h.log.Status(context.Background())
	require.NoError(t, err)
	return status
}

func (h *harness) delivery(t *testing.T, webhookID string) publicwebhook.Delivery {
	t.Helper()
	var log publicwebhook.Log
	require.Eventually(t, func() bool {
		var err error
		log, err = h.log.Log(context.Background(), webhookID)
		return err == nil && len(log.Deliveries) > 0
	}, 5*time.Second, 10*time.Millisecond)
	return log.Deliveries[0]
}

// hookOf is the hook ID the relay knows a webhook's public URL by.
func hookOf(wh *auth.Webhook) string {
	return auth.WebhookHookID(wh.ID)
}

func deliveryFrame(id, hook string, header map[string]string, body string) map[string]any {
	return map[string]any{
		"t": "delivery", "id": id, "hook": hook, "received": time.Now().UTC().Format(time.RFC3339Nano),
		"headers": header, "body": body,
	}
}

// A server with a public URL connects, gives the relay its token as a
// digest, learns its URL, and starts a run for a delivery.
func TestServiceStartsRuns(t *testing.T) {
	h := newHarness(t, 1000)
	h.addWebhook(t, "private", nil)
	wh := h.addWebhook(t, "nightly", &auth.WebhookPublic{Token: "token-1", CreatedAt: time.Now()})
	h.run(t)
	ws := h.cloud.relay(t)

	hooks := ws.next(t, "hooks")
	assert.Equal(t, []any{map[string]any{"hook": hookOf(wh), "digest": digestOf("token-1")}}, hooks["hooks"])
	ws.write(t, map[string]any{"t": "hooked", "id": hooks["id"], "url": "https://hooks.example/obj"})
	require.Eventually(t, func() bool { return h.status(t).URL == "https://hooks.example/obj" }, 5*time.Second, 10*time.Millisecond)
	status := h.status(t)
	assert.True(t, status.Connected)
	assert.Equal(t, 1000, status.MaxHooks)

	ws.write(t, deliveryFrame("d-1", hookOf(wh), map[string]string{"X-Github-Event": "push", "Authorization": "x"}, `{"ref":"main"}`))
	assert.Equal(t, map[string]any{"t": "delivered", "id": "d-1", "outcome": "started"}, ws.next(t, "delivered"))

	h.runs.mu.Lock()
	require.Len(t, h.runs.enqueued, 1)
	got := h.runs.enqueued[0]
	h.runs.mu.Unlock()
	assert.Equal(t, "nightly", got.dagName)
	assert.Equal(t, "d-1", got.runID, "the delivery ID names the run, so a repeat starts none")
	assert.Equal(t, `WEBHOOK_PAYLOAD="{\"ref\":\"main\"}" WEBHOOK_HEADERS="{\"x-github-event\":[\"push\"]}"`, got.params)

	logged := h.delivery(t, wh.ID)
	assert.Equal(t, publicwebhook.OutcomeStarted, logged.Outcome)
	assert.Equal(t, "d-1", logged.RunID)
	assert.Equal(t, "push", logged.Event)
	assert.Equal(t, len(`{"ref":"main"}`), logged.Size)

	ws.write(t, map[string]any{"t": "missed", "hooks": []map[string]any{{"hook": hookOf(wh), "expired": 2, "refused": 1}}})
	require.Eventually(t, func() bool {
		log, err := h.log.Log(context.Background(), wh.ID)
		return err == nil && log.Missed.Expired == 2 && log.Missed.Refused == 1
	}, 5*time.Second, 10*time.Millisecond)
}

// The relay reports what it dropped as soon as the server says hello, so a
// count is kept for any public webhook, not only those in the set it holds.
func TestServiceCountsMissedOutsideTheSet(t *testing.T) {
	h := newHarness(t, 1)
	now := time.Now()
	h.addWebhook(t, "first", &auth.WebhookPublic{Token: "token-1", CreatedAt: now})
	second := h.addWebhook(t, "second", &auth.WebhookPublic{Token: "token-2", CreatedAt: now.Add(time.Second)})
	h.run(t)
	ws := h.cloud.relay(t)
	assert.Len(t, ws.next(t, "hooks")["hooks"], 1)

	ws.write(t, map[string]any{"t": "missed", "hooks": []map[string]any{
		{"hook": hookOf(second), "expired": 3, "refused": 0},
		{"hook": auth.WebhookHookID("gone"), "expired": 1, "refused": 0},
	}})
	require.Eventually(t, func() bool {
		log, err := h.log.Log(context.Background(), second.ID)
		return err == nil && log.Missed.Expired == 3
	}, 5*time.Second, 10*time.Millisecond)
}

// Each delivery a webhook does not take, or cannot start yet, is answered
// and logged as the design table says.
func TestServiceDeliveryOutcomes(t *testing.T) {
	h := newHarness(t, 1000)
	now := time.Now()
	github := h.addWebhook(t, "github", &auth.WebhookPublic{Token: "t-github", Sender: "github", SenderSecret: "secret", CreatedAt: now})
	issues := h.addWebhook(t, "issues", &auth.WebhookPublic{Token: "t-issues", Events: []string{"issues"}, CreatedAt: now.Add(time.Second)})
	plain := h.addWebhook(t, "plain", &auth.WebhookPublic{Token: "t-plain", CreatedAt: now.Add(2 * time.Second)})
	missing := h.addWebhook(t, "missing", &auth.WebhookPublic{Token: "t-missing", CreatedAt: now.Add(3 * time.Second)})
	off := h.addWebhook(t, "off", &auth.WebhookPublic{Token: "t-off", CreatedAt: now.Add(4 * time.Second)})
	_, err := h.webhooks.UpdateByDAGName(context.Background(), "off", func(wh *auth.Webhook) error {
		wh.Enabled = false
		return nil
	})
	require.NoError(t, err)
	h.run(t)
	ws := h.cloud.relay(t)
	ws.next(t, "hooks")

	signed, err := webhooksig.Sign(webhooksig.GitHub, "secret", []byte(`{"zen":"x"}`), time.Now())
	require.NoError(t, err)
	unsigned := map[string]string{"X-Github-Event": "push"}
	for _, tc := range []struct {
		name    string
		queued  int
		err     error
		webhook *auth.Webhook
		hook    string
		header  map[string]string
		outcome string
		after   any
		logged  string
	}{
		{"a bad signature", 0, nil, github, hookOf(github), unsigned, "ignored", nil, publicwebhook.OutcomeRejected},
		{"GitHub's ping", 0, nil, github, hookOf(github), map[string]string{"X-Github-Event": "ping", "X-Hub-Signature-256": signed.Get("X-Hub-Signature-256")}, "ignored", nil, publicwebhook.OutcomeSkipped},
		{"an event not taken", 0, nil, issues, hookOf(issues), unsigned, "ignored", nil, publicwebhook.OutcomeSkipped},
		{"a full queue", 100, nil, plain, hookOf(plain), unsigned, "retry", float64(60), publicwebhook.OutcomeWaiting},
		{"a run that cannot start", 0, fmt.Errorf("%w: bad params", publicwebhook.ErrInvalidRun), plain, hookOf(plain), unsigned, "failed", nil, publicwebhook.OutcomeFailed},
		{"a storage failure", 0, errors.New("disk full"), plain, hookOf(plain), unsigned, "retry", float64(30), publicwebhook.OutcomeWaiting},
		{"a DAG that does not load", 0, nil, missing, hookOf(missing), unsigned, "failed", nil, publicwebhook.OutcomeFailed},
		{"a webhook turned off", 0, nil, off, hookOf(off), unsigned, "ignored", nil, publicwebhook.OutcomeIgnored},
	} {
		h.runs.set(tc.queued, tc.err)
		id := "d-" + strings.ReplaceAll(tc.name, " ", "-")
		body := `{"zen":"x"}`
		ws.write(t, deliveryFrame(id, tc.hook, tc.header, body))
		want := map[string]any{"t": "delivered", "id": id, "outcome": tc.outcome}
		if tc.after != nil {
			want["after"] = tc.after
		}
		assert.Equal(t, want, ws.next(t, "delivered"), tc.name)
		require.Eventually(t, func() bool {
			log, err := h.log.Log(context.Background(), tc.webhook.ID)
			return err == nil && len(log.Deliveries) > 0 && log.Deliveries[0].ID == id
		}, 5*time.Second, 10*time.Millisecond, tc.name)
		assert.Equal(t, tc.logged, h.delivery(t, tc.webhook.ID).Outcome, tc.name)
	}

	h.runs.set(0, nil)
	ws.write(t, deliveryFrame("d-large", hookOf(plain), unsigned, strings.Repeat("a", testMaxPayloadSize+1)))
	assert.Equal(t, map[string]any{"t": "delivered", "id": "d-large", "outcome": "failed"}, ws.next(t, "delivered"),
		"a body over webhooks.max_payload_size starts no run")

	ws.write(t, deliveryFrame("d-unknown", auth.WebhookHookID("nobody"), unsigned, "{}"))
	assert.Equal(t, map[string]any{"t": "delivered", "id": "d-unknown", "outcome": "ignored"}, ws.next(t, "delivered"))
}

// The relay holds no more URLs than the ticket allows, the oldest first.
func TestServiceKeepsToThePlan(t *testing.T) {
	h := newHarness(t, 1)
	now := time.Now()
	h.addWebhook(t, "newer", &auth.WebhookPublic{Token: "t-newer", CreatedAt: now.Add(time.Minute)})
	older := h.addWebhook(t, "older", &auth.WebhookPublic{Token: "t-older", CreatedAt: now})
	h.run(t)
	ws := h.cloud.relay(t)

	hooks := ws.next(t, "hooks")
	assert.Equal(t, []any{map[string]any{"hook": hookOf(older), "digest": digestOf("t-older")}}, hooks["hooks"])
	require.Eventually(t, func() bool {
		left := h.status(t).LeftOut
		return len(left) == 1 && left[0] == "newer"
	}, 5*time.Second, 10*time.Millisecond)
}

// A read that cannot account for every webhook never changes the relay's
// set; turning every URL off empties the set and lets the connection go.
func TestServiceSendsOnlyCompleteSets(t *testing.T) {
	h := newHarness(t, 1000)
	wh := h.addWebhook(t, "nightly", &auth.WebhookPublic{Token: "token-1", CreatedAt: time.Now()})
	h.run(t)
	ws := h.cloud.relay(t)
	ws.next(t, "hooks")

	// A record the store cannot decrypt makes every read incomplete.
	require.NoError(t, h.col.Put(context.Background(), &persis.Record{
		ID:   "broken",
		Data: []byte(`{"id":"broken","dagName":"broken","public":{"tokenEnc":"not-encrypted"}}`),
	}))
	_, err := h.webhooks.UpdateByDAGName(context.Background(), "nightly", func(wh *auth.Webhook) error {
		wh.Public.Token = "token-2"
		return nil
	})
	require.NoError(t, err)
	ws.none(t, "hooks", 200*time.Millisecond)

	require.NoError(t, h.col.Delete(context.Background(), "broken"))
	assert.Equal(t, []any{map[string]any{"hook": hookOf(wh), "digest": digestOf("token-2")}}, ws.next(t, "hooks")["hooks"])

	_, err = h.webhooks.UpdateByDAGName(context.Background(), "nightly", func(wh *auth.Webhook) error {
		wh.Public = nil
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []any{}, ws.next(t, "hooks")["hooks"])
	ws.waitClosed(t)
	require.Eventually(t, func() bool { return !h.status(t).Connected }, 5*time.Second, 10*time.Millisecond)
}

// A server Dagu Cloud refuses a ticket waits before asking again; one with
// no public URL never asks.
func TestServiceWaitsAfterARefusal(t *testing.T) {
	h := newHarness(t, 1000)
	h.run(t)
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, h.cloud.tickets.Load(), "no public URL, no ticket")

	h.cloud.refuse.Store(true)
	h.addWebhook(t, "nightly", &auth.WebhookPublic{Token: "token-1", CreatedAt: time.Now()})
	require.Eventually(t, func() bool { return strings.Contains(h.status(t).Error, "refused") }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), h.cloud.tickets.Load(), "a refusal is not asked again at once")
}

// A ticket that allows none of the public URLs is not asked for again at
// once, which would loop on tickets and connections.
func TestServiceWaitsWhenThePlanAllowsNoURLs(t *testing.T) {
	h := newHarness(t, 0)
	h.addWebhook(t, "nightly", &auth.WebhookPublic{Token: "token-1", CreatedAt: time.Now()})
	h.run(t)
	ws := h.cloud.relay(t)
	assert.Equal(t, []any{}, ws.next(t, "hooks")["hooks"])
	ws.waitClosed(t)

	require.Eventually(t, func() bool { return h.status(t).Error == "The plan allows no public URLs." }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"nightly"}, h.status(t).LeftOut)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), h.cloud.tickets.Load(), "the server waits before asking again")
}

// A URL turned off while the scheduler was away is dropped at the relay when
// it returns, though no URL is public any longer.
func TestServiceDropsURLsTurnedOffWhileAway(t *testing.T) {
	h := newHarness(t, 1000)
	require.NoError(t, h.log.SetStatus(context.Background(), publicwebhook.Status{Holding: true}))
	h.addWebhook(t, "nightly", nil)
	// A server not yet connected to Dagu Cloud when it starts still drops
	// them once it is.
	h.offline.Store(true)
	h.run(t)
	require.Eventually(t, func() bool { return h.status(t).Error != "" }, 5*time.Second, 10*time.Millisecond)
	h.offline.Store(false)
	ws := h.cloud.relay(t)

	assert.Equal(t, []any{}, ws.next(t, "hooks")["hooks"])
	ws.waitClosed(t)
	require.Eventually(t, func() bool { return !h.status(t).Holding }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), h.cloud.tickets.Load(), "with nothing left to drop, the server stays away")
}

// The relay knows a URL by its webhook, not its DAG's name, so renaming the
// DAG changes nothing at the relay, and a delivery starts the renamed DAG.
func TestServiceKeepsURLsAcrossRenames(t *testing.T) {
	h := newHarness(t, 1000)
	wh := h.addWebhook(t, "nightly", &auth.WebhookPublic{Token: "token-1", CreatedAt: time.Now()})
	h.run(t)
	ws := h.cloud.relay(t)
	ws.next(t, "hooks")

	require.NoError(t, h.webhooks.RenameDAG(context.Background(), "nightly", "renamed"))
	ws.none(t, "hooks", 200*time.Millisecond)
	ws.write(t, deliveryFrame("d-1", hookOf(wh), map[string]string{"X-Github-Event": "push"}, `{}`))
	assert.Equal(t, "started", ws.next(t, "delivered")["outcome"])

	h.runs.mu.Lock()
	defer h.runs.mu.Unlock()
	require.Len(t, h.runs.enqueued, 1)
	assert.Equal(t, "renamed", h.runs.enqueued[0].dagName)
}
