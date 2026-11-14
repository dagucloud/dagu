// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// sseHeartbeatInterval matches the Web UI stream and stays well under
	// common proxy idle timeouts.
	sseHeartbeatInterval = 10 * time.Second
	sseHeartbeat         = ": heartbeat\n\n"
)

// withSSEHeartbeat writes an SSE comment when a response starts an event
// stream and then every interval until the handler returns. Proxies that hold
// headers until the first body byte, or drop idle connections, then keep the
// stream open. Clients ignore SSE comments.
func withSSEHeartbeat(next http.Handler, interval time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hw := &heartbeatWriter{ResponseWriter: w, interval: interval}
		defer hw.stop()
		next.ServeHTTP(hw, r)
	})
}

// heartbeatWriter serializes heartbeats with the wrapped handler's writes.
// The MCP SDK writes each event with a single Write, so heartbeats only land
// between events.
type heartbeatWriter struct {
	http.ResponseWriter
	interval time.Duration

	mu          sync.Mutex
	wroteHeader bool
	cancel      context.CancelFunc
	done        chan struct{}
}

func (h *heartbeatWriter) WriteHeader(code int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeHeaderLocked(code)
}

func (h *heartbeatWriter) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeHeaderLocked(http.StatusOK)
	return h.ResponseWriter.Write(p)
}

func (h *heartbeatWriter) Flush() {
	_ = h.FlushError()
}

func (h *heartbeatWriter) FlushError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return http.NewResponseController(h.ResponseWriter).Flush()
}

func (h *heartbeatWriter) Unwrap() http.ResponseWriter {
	return h.ResponseWriter
}

func (h *heartbeatWriter) writeHeaderLocked(code int) {
	if h.wroteHeader {
		return
	}
	h.wroteHeader = true
	h.ResponseWriter.WriteHeader(code)
	if code != http.StatusOK || !strings.HasPrefix(h.Header().Get("Content-Type"), "text/event-stream") {
		return
	}
	if h.beatLocked() != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan struct{})
	go h.run(ctx)
}

func (h *heartbeatWriter) run(ctx context.Context) {
	defer close(h.done)
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			err := h.beatLocked()
			h.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (h *heartbeatWriter) beatLocked() error {
	if _, err := io.WriteString(h.ResponseWriter, sseHeartbeat); err != nil {
		return err
	}
	return http.NewResponseController(h.ResponseWriter).Flush()
}

// stop ends the heartbeat loop and waits for it, so the response is never
// written after the handler returns.
func (h *heartbeatWriter) stop() {
	h.mu.Lock()
	cancel, done := h.cancel, h.done
	h.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}
