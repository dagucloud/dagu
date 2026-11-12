// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudrelay

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/license"
)

func TestRequestTicket(t *testing.T) {
	var got map[string]string
	status := http.StatusOK
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/servers/relay" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(status)
		switch status {
		case http.StatusOK:
			_, _ = w.Write([]byte(`{"token":"tok","url":"wss://relay.example/server","max_hooks":1}`))
		default:
			_, _ = w.Write([]byte(`{"error":"unauthorized","message":"unauthorized"}`))
		}
	}))
	t.Cleanup(console.Close)
	creds := license.CloudCredentials{CloudURL: console.URL + "/", LicenseID: "lic", ServerID: "srv", HeartbeatSecret: "secret"}

	ticket, err := RequestTicket(context.Background(), console.Client(), creds)
	require.NoError(t, err)
	assert.Equal(t, Ticket{Token: "tok", URL: "wss://relay.example/server", MaxHooks: 1}, ticket)
	assert.Equal(t, map[string]string{"license_id": "lic", "server_id": "srv", "heartbeat_secret": "secret"}, got)

	_, err = RequestAccessTicket(context.Background(), console.Client(), creds)
	require.NoError(t, err)
	assert.Equal(t, "access", got["purpose"], "a connection for calls asks for its own ticket")

	status = http.StatusUnauthorized
	_, err = RequestTicket(context.Background(), console.Client(), creds)
	refused, ok := errors.AsType[*TicketRefusedError](err)
	require.True(t, ok, "a refused ticket: %v", err)
	assert.Equal(t, http.StatusUnauthorized, refused.StatusCode)
	assert.Equal(t, "unauthorized", refused.Message)

	status = http.StatusBadGateway
	_, err = RequestTicket(context.Background(), console.Client(), creds)
	require.Error(t, err)
	_, ok = errors.AsType[*TicketRefusedError](err)
	assert.False(t, ok, "a failing Dagu Cloud may answer later")
}

// fakeRelay accepts one connection at a time with the ticket "ticket" and
// hands it to the test.
type fakeRelay struct {
	srv   *httptest.Server
	conns chan *websocket.Conn
	done  chan struct{}
	// instance is the Dagu-Instance header of the last connection.
	instance atomic.Value
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	r := &fakeRelay{conns: make(chan *websocket.Conn, 1), done: make(chan struct{})}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer ticket" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.instance.Store(req.Header.Get("Dagu-Instance"))
		ws, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		ws.SetReadLimit(1 << 20)
		r.conns <- ws
		<-r.done
	}))
	t.Cleanup(func() {
		close(r.done)
		r.srv.Close()
	})
	return r
}

func (r *fakeRelay) ticket(token string) Ticket {
	return Ticket{Token: token, URL: "ws" + strings.TrimPrefix(r.srv.URL, "http")}
}

// accept dials the relay as a server and returns both ends.
func (r *fakeRelay) accept(t *testing.T) (*Conn, *websocket.Conn) {
	t.Helper()
	conn, err := Dial(context.Background(), r.srv.Client(), r.ticket("ticket"), Hello{Name: "etl-prod-1", Version: "2.20.0"})
	require.NoError(t, err)
	select {
	case ws := <-r.conns:
		return conn, ws
	case <-time.After(5 * time.Second):
		t.Fatal("the relay saw no connection")
	}
	return nil, nil
}

func readFrame(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := ws.Read(ctx)
		require.NoError(t, err)
		if string(data) == relayPing {
			continue
		}
		var f map[string]any
		require.NoError(t, json.Unmarshal(data, &f))
		return f
	}
}

func writeFrame(t *testing.T, ws *websocket.Conn, f any) {
	t.Helper()
	data, err := json.Marshal(f)
	require.NoError(t, err)
	require.NoError(t, ws.Write(context.Background(), websocket.MessageText, data))
}

func TestDialRefusedTicket(t *testing.T) {
	relay := newFakeRelay(t)
	_, err := Dial(context.Background(), relay.srv.Client(), relay.ticket("wrong"), Hello{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

// The relay hears hello, then the hooks as digests; its answers and
// deliveries reach the handler, and each delivery's outcome goes back.
func TestRelayWebhooks(t *testing.T) {
	relay := newFakeRelay(t)
	conn, ws := relay.accept(t)
	assert.Equal(t, map[string]any{"t": "hello", "name": "etl-prod-1", "version": "2.20.0"}, readFrame(t, ws))

	var mu sync.Mutex
	var hooked []Hooked
	var missed []Missed
	deliveries := make(chan Delivery, 1)
	served := make(chan error, 1)
	go func() {
		served <- conn.Serve(context.Background(), Handler{
			Delivery: func(_ context.Context, d Delivery) Outcome {
				deliveries <- d
				return OutcomeRetry(1500 * time.Millisecond)
			},
			Hooked: func(h Hooked) {
				mu.Lock()
				defer mu.Unlock()
				hooked = append(hooked, h)
			},
			Missed: func(m []Missed) {
				mu.Lock()
				defer mu.Unlock()
				missed = append(missed, m...)
			},
		})
	}()

	require.NoError(t, conn.SetHooks(context.Background(), []Hook{
		{ID: "nightly/webhook", Token: "token-1"},
		{ID: "slack/webhook", Token: "token-2", Handshake: HandshakeSlack},
	}))
	hooks := readFrame(t, ws)
	assert.Equal(t, "hooks", hooks["t"])
	assert.Equal(t, "1", hooks["id"])
	assert.Equal(t, []any{
		map[string]any{"hook": "nightly/webhook", "digest": hashOf("token-1")},
		map[string]any{"hook": "slack/webhook", "digest": hashOf("token-2"), "handshake": "slack"},
	}, hooks["hooks"])

	require.NoError(t, conn.SetHooks(context.Background(), nil))
	empty := readFrame(t, ws)
	assert.Equal(t, "2", empty["id"])
	assert.Equal(t, []any{}, empty["hooks"], "an empty set is still sent")

	writeFrame(t, ws, map[string]any{"t": "hooked", "id": "1", "url": "https://hooks.example/obj"})
	writeFrame(t, ws, map[string]any{"t": "hooked", "id": "2", "error": "too_many_hooks"})
	writeFrame(t, ws, map[string]any{"t": "missed", "hooks": []map[string]any{{"hook": "nightly/webhook", "expired": 2, "refused": 1}}})
	writeFrame(t, ws, map[string]any{
		"t": "delivery", "id": "d-1", "hook": "nightly/webhook", "received": "2026-10-14T01:02:03.5Z",
		"headers": map[string]string{"X-Github-Event": "push"}, "body": `{"ref":"main"}`,
	})

	d := <-deliveries
	assert.Equal(t, "d-1", d.ID)
	assert.Equal(t, "nightly/webhook", d.Hook)
	assert.Equal(t, time.Date(2026, 10, 14, 1, 2, 3, 500_000_000, time.UTC), d.Received.UTC())
	assert.Equal(t, "push", d.Header.Get("X-Github-Event"))
	assert.Equal(t, `{"ref":"main"}`, string(d.Body))
	assert.Equal(t, map[string]any{"t": "delivered", "id": "d-1", "outcome": "retry", "after": float64(2)}, readFrame(t, ws))

	mu.Lock()
	assert.Equal(t, []Hooked{{URL: "https://hooks.example/obj"}, {Error: ErrorTooManyHooks}}, hooked)
	assert.Equal(t, []Missed{{Hook: "nightly/webhook", Expired: 2, Refused: 1}}, missed)
	mu.Unlock()

	require.NoError(t, ws.Close(websocket.StatusNormalClosure, closeExpired))
	assert.NoError(t, <-served, "a scheduled close needs no backoff")
}

func hashOf(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// A delivery the server cannot read is ignored, not lost silently.
func TestRelayIgnoresUnreadableDeliveries(t *testing.T) {
	relay := newFakeRelay(t)
	conn, ws := relay.accept(t)
	readFrame(t, ws)
	go func() {
		_ = conn.Serve(context.Background(), Handler{Delivery: func(context.Context, Delivery) Outcome {
			return OutcomeStarted
		}})
	}()
	writeFrame(t, ws, map[string]any{"t": "delivery", "id": "d-1", "hook": "h", "received": "yesterday", "body": "{}"})
	assert.Equal(t, map[string]any{"t": "delivered", "id": "d-1", "outcome": "ignored"}, readFrame(t, ws))
}

func TestRelayCloseReasons(t *testing.T) {
	for reason, want := range map[string]error{closeReplaced: ErrReplaced, closeDropped: ErrDropped} {
		relay := newFakeRelay(t)
		conn, ws := relay.accept(t)
		readFrame(t, ws)
		served := make(chan error, 1)
		go func() { served <- conn.Serve(context.Background(), Handler{}) }()
		require.NoError(t, ws.Close(websocket.StatusNormalClosure, reason))
		assert.ErrorIs(t, <-served, want, reason)
	}
}

// A relay that stops answering pings is noticed, and the connection ends.
func TestRelayNoticesSilence(t *testing.T) {
	relay := newFakeRelay(t)
	conn, ws := relay.accept(t)
	readFrame(t, ws)
	conn.timing.pingInterval = 20 * time.Millisecond
	conn.timing.watchInterval = 10 * time.Millisecond
	conn.timing.pongWait = 50 * time.Millisecond
	conn.timing.silence = 50 * time.Millisecond
	served := make(chan error, 1)
	go func() { served <- conn.Serve(context.Background(), Handler{}) }()
	select {
	case err := <-served:
		assert.ErrorIs(t, err, ErrSilent)
	case <-time.After(5 * time.Second):
		t.Fatal("a silent relay was not noticed")
	}
}

// sendCall sends a call as the relay does: its frame, its body in one chunk,
// and its end.
func sendCall(t *testing.T, ws *websocket.Conn, id, body string) {
	t.Helper()
	writeFrame(t, ws, map[string]any{"t": "req", "id": id, "method": "POST", "path": "/mcp",
		"headers": map[string]string{"Content-Type": "application/json"}, "assertion": "signed.assertion"})
	require.NoError(t, ws.Write(context.Background(), websocket.MessageBinary, chunkFor(id, []byte(body))))
	writeFrame(t, ws, map[string]any{"t": "end", "id": id})
}

func chunkFor(id string, body []byte) []byte {
	u := uuid.MustParse(id)
	return append(append([]byte{chunkVersion}, u[:]...), body...)
}

// readAnswer reads a call's answer: its frame, then its chunks until its end.
func readAnswer(t *testing.T, ws *websocket.Conn) (map[string]any, []byte, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res map[string]any
	var body []byte
	chunks := 0
	for {
		kind, data, err := ws.Read(ctx)
		require.NoError(t, err)
		if kind == websocket.MessageBinary {
			body = append(body, data[chunkHeaderBytes:]...)
			chunks++
			continue
		}
		if string(data) == relayPing {
			continue
		}
		var f map[string]any
		require.NoError(t, json.Unmarshal(data, &f))
		switch f["t"] {
		case "res":
			res = f
		case "end":
			return res, body, chunks
		}
	}
}

// A call reaches the handler with its body and assertion, and its answer
// streams back in chunks.
func TestRelayCalls(t *testing.T) {
	relay := newFakeRelay(t)
	conn, err := Dial(context.Background(), relay.srv.Client(), relay.ticket("ticket"),
		Hello{Name: "etl-prod-1", Version: "2.21.0", Instance: "instance-1"})
	require.NoError(t, err)
	ws := <-relay.conns
	assert.Equal(t, "instance-1", relay.instance.Load())
	assert.Equal(t, map[string]any{"t": "hello", "name": "etl-prod-1", "version": "2.21.0"}, readFrame(t, ws))

	large := strings.Repeat("x", 3*chunkBytes/2)
	go func() {
		_ = conn.Serve(context.Background(), Handler{Call: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			if string(body) == "large" {
				_, _ = w.Write([]byte(large))
				return
			}
			_, _ = w.Write([]byte(r.Method + " " + r.URL.Path + " " + r.Header.Get(AssertionHeader) + " " + string(body)))
		})})
	}()

	id := uuid.NewString()
	sendCall(t, ws, id, `{"jsonrpc":"2.0"}`)
	res, body, _ := readAnswer(t, ws)
	assert.Equal(t, map[string]any{"t": "res", "id": id, "status": float64(201),
		"headers": map[string]any{"Content-Type": "application/json"}}, res)
	assert.Equal(t, `POST /mcp signed.assertion {"jsonrpc":"2.0"}`, string(body))

	sendCall(t, ws, uuid.NewString(), "large")
	_, body, chunks := readAnswer(t, ws)
	assert.Equal(t, large, string(body))
	assert.Equal(t, 2, chunks, "an answer goes back in chunks of at most 64 KiB")
}

// What arrives for a call after its end, a chunk or another end, changes
// nothing: the handler runs once, with the body sent before the end.
func TestRelayServesACallOnce(t *testing.T) {
	relay := newFakeRelay(t)
	conn, ws := relay.accept(t)
	readFrame(t, ws)
	release := make(chan struct{})
	var calls atomic.Int32
	go func() {
		_ = conn.Serve(context.Background(), Handler{Call: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			<-release
			body, _ := io.ReadAll(r.Body)
			_, _ = w.Write(body)
		})})
	}()

	id := uuid.NewString()
	sendCall(t, ws, id, "first")
	require.NoError(t, ws.Write(context.Background(), websocket.MessageBinary, chunkFor(id, []byte(" late"))))
	writeFrame(t, ws, map[string]any{"t": "end", "id": id})
	close(release)
	_, body, _ := readAnswer(t, ws)
	assert.Equal(t, "first", string(body))
	assert.Equal(t, int32(1), calls.Load())
}

// A call the relay cancels has its context canceled and gets no answer.
func TestRelayCancelsACall(t *testing.T) {
	relay := newFakeRelay(t)
	conn, ws := relay.accept(t)
	readFrame(t, ws)
	canceled := make(chan struct{})
	go func() {
		_ = conn.Serve(context.Background(), Handler{Call: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			close(canceled)
		})})
	}()
	id := uuid.NewString()
	sendCall(t, ws, id, "{}")
	writeFrame(t, ws, map[string]any{"t": "cancel", "id": id})
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the call was not canceled")
	}
}

func TestRelayRefusesALargeCallBody(t *testing.T) {
	relay := newFakeRelay(t)
	conn, ws := relay.accept(t)
	readFrame(t, ws)
	go func() {
		_ = conn.Serve(context.Background(), Handler{Call: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})})
	}()
	id := uuid.NewString()
	writeFrame(t, ws, map[string]any{"t": "req", "id": id, "method": "POST", "path": "/mcp"})
	for range 3 {
		require.NoError(t, ws.Write(context.Background(), websocket.MessageBinary, chunkFor(id, make([]byte, 512<<10))))
	}
	writeFrame(t, ws, map[string]any{"t": "end", "id": id})
	res, _, _ := readAnswer(t, ws)
	assert.Equal(t, float64(http.StatusRequestEntityTooLarge), res["status"])
}
