// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec094_ai_gateway_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstream stands for the model API of a provider. It records each request:
// the headers, the size of the body and the end of the call. The field mode of the
// JSON body selects the answer, so the fake CLI drives it.
//
//	sse       one event, then the event stop after the test calls release
//	block     one event, then no end: the call lasts until the client goes away
//	large     mib MiB of data in chunks of 64 KiB
//	many      events small events, each flushed
//	size      the number of bytes of the body
//	status    the status of the field status, with retry-after and set-cookie
//	redirect  a redirect to another host
//	ok        a small answer
type fakeUpstream struct {
	t      *testing.T
	server *httptest.Server
	// spy is a second server. A redirect points at it. It must get no request.
	spy      *httptest.Server
	spyCalls sync.Mutex
	spyCount int

	mu       sync.Mutex
	requests []upstreamRequest

	releaseOnce sync.Once
	release     chan struct{}
	cancelled   chan struct{} // closed when a call of the mode block ends before the end
	cancelOnce  sync.Once
	started     chan struct{} // closed at the first call of the mode block or sse
	startOnce   sync.Once
}

// upstreamRequest is what the fake saw of one request.
type upstreamRequest struct {
	Method  string
	Path    string
	Query   string
	Host    string
	Header  http.Header
	Bytes   int
	Mode    string
	Started time.Time
}

func startFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	u := &fakeUpstream{
		t:         t,
		release:   make(chan struct{}),
		cancelled: make(chan struct{}),
		started:   make(chan struct{}),
	}
	u.spy = httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		u.spyCalls.Lock()
		u.spyCount++
		u.spyCalls.Unlock()
	}))
	// The coordinator reaches the fake on the loopback address of the host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	u.server = httptest.NewUnstartedServer(http.HandlerFunc(u.serve))
	u.server.Listener = listener
	u.server.Start()
	t.Cleanup(func() {
		u.releaseNow()
		u.server.Close()
		u.spy.Close()
	})
	return u
}

// URL is the base URL of the fake, for the base_url of an endpoint.
func (u *fakeUpstream) URL() string { return u.server.URL }

func (u *fakeUpstream) releaseNow() { u.releaseOnce.Do(func() { close(u.release) }) }

func (u *fakeUpstream) spyHits() int {
	u.spyCalls.Lock()
	defer u.spyCalls.Unlock()
	return u.spyCount
}

func (u *fakeUpstream) all() []upstreamRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]upstreamRequest(nil), u.requests...)
}

// keys returns the key header of each request, in order.
func (u *fakeUpstream) keys() []string {
	var keys []string
	for _, r := range u.all() {
		keys = append(keys, r.Header.Get("X-Api-Key"))
	}
	return keys
}

func (u *fakeUpstream) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var request struct {
		Mode   string `json:"mode"`
		MiB    int    `json:"mib"`
		Events int    `json:"events"`
		Status int    `json:"status"`
	}
	_ = json.Unmarshal(body, &request)
	u.mu.Lock()
	u.requests = append(u.requests, upstreamRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Host: r.Host,
		Header: r.Header.Clone(), Bytes: len(body), Mode: request.Mode, Started: time.Now(),
	})
	u.mu.Unlock()

	flusher, _ := w.(http.Flusher)
	switch request.Mode {
	case "sse":
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		flusher.Flush()
		u.startOnce.Do(func() { close(u.started) })
		select {
		case <-u.release:
		case <-r.Context().Done():
			return
		case <-time.After(60 * time.Second):
		}
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	case "block":
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
		flusher.Flush()
		u.startOnce.Do(func() { close(u.started) })
		select {
		case <-r.Context().Done():
			u.cancelOnce.Do(func() { close(u.cancelled) })
		case <-time.After(90 * time.Second):
		}
	case "large":
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := make([]byte, 64*1024)
		for i := 0; i < request.MiB*16; i++ {
			fillChunk(chunk, i)
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	case "many":
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < request.Events; i++ {
			if _, err := io.WriteString(w, manyEvent(i)); err != nil {
				return
			}
			flusher.Flush()
		}
	case "size":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"bytes":%d}`, len(body))
	case "status":
		w.Header().Set("Retry-After", "17")
		w.Header().Set("Set-Cookie", "session=leak")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(request.Status)
		_, _ = io.WriteString(w, `{"type":"error"}`)
	case "redirect":
		http.Redirect(w, r, u.spy.URL+"/stolen", http.StatusTemporaryRedirect)
	default:
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"ok"}]}`)
	}
}

// fillChunk writes the bytes of the chunk number i. The test computes the same
// bytes to find the digest that a client must see.
func fillChunk(chunk []byte, i int) {
	for j := range chunk {
		chunk[j] = byte((i*31 + j*7) % 251)
	}
}

func manyEvent(i int) string {
	return fmt.Sprintf("data: %d %s\n\n", i, strings.Repeat("x", i%40))
}

// expectedLarge returns the number of bytes and the SHA-256 of the answer of the
// mode large.
func expectedLarge(mib int) (int, string) {
	digest := sha256.New()
	chunk := make([]byte, 64*1024)
	total := 0
	for i := 0; i < mib*16; i++ {
		fillChunk(chunk, i)
		_, _ = digest.Write(chunk)
		total += len(chunk)
	}
	return total, hex.EncodeToString(digest.Sum(nil))
}

// expectedMany returns the number of bytes and the SHA-256 of the answer of the
// mode many.
func expectedMany(events int) (int, string) {
	digest := sha256.New()
	total := 0
	for i := range events {
		event := manyEvent(i)
		_, _ = io.WriteString(digest, event)
		total += len(event)
	}
	return total, hex.EncodeToString(digest.Sum(nil))
}
