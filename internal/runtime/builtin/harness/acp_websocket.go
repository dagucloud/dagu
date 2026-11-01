// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"golang.org/x/net/http/httpguts"
)

var acpRemoteDefaultHTTPClient = &http.Client{
	Transport: newOpenCodeTransport(),
	// Remote endpoints must not forward credentials through redirects.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// runACPRemote shares session, permission, output, and cancellation handling across remote transports.
// The working directory is passed to the agent unchanged, never created locally.
func (e *harnessExecutor) runACPRemote(ctx context.Context, cfg providerConfig, client *acpHarnessClient) error {
	for key := range cfg.flags {
		if !reservedKeys[key] {
			return fmt.Errorf("harness: remote ACP does not accept CLI option %q", key)
		}
	}
	settings, err := runtime.EvalObject(ctx, struct {
		URL     string
		CAFile  string
		Cwd     string
		Headers map[string]string
	}{cfg.definition.URL, cfg.definition.CAFile, cfg.definition.Cwd, cfg.definition.Headers})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("harness: could not resolve remote ACP connection settings")
	}
	def := cloneDefinition(cfg.definition)
	def.URL, def.Cwd, def.Headers = settings.URL, settings.Cwd, settings.Headers
	def.CAFile = settings.CAFile
	// Load-time validation skips values with variable references, so any left
	// after evaluation are unresolved.
	if strings.Contains(def.URL, "$") || strings.Contains(def.Cwd, "$") {
		return errors.New("harness: unresolved remote ACP connection settings")
	}
	if err := def.ValidateConnection(); err != nil {
		return fmt.Errorf("harness: remote ACP: %w", err)
	}
	if len(def.Headers) != 0 && sendsCleartext(def.URL) {
		_, _ = fmt.Fprintln(e.stderrWriter(), "harness: warning: remote ACP headers are sent unencrypted; use wss:// or https:// for non-loopback hosts")
	}
	httpClient, release, err := acpRemoteHTTPClient(settings.CAFile, e.workDir)
	if err != nil {
		return err
	}
	defer release()
	wireCtx, closeWire := context.WithCancel(context.Background())
	defer closeWire()
	var stream io.ReadWriteCloser
	if def.Transport == ir.HarnessTransportHTTP {
		stream = newACPHTTPStream(wireCtx, def, httpClient)
	} else {
		socket, dialErr := dialACPWebSocket(ctx, def, httpClient)
		if dialErr != nil {
			return dialErr
		}
		stream = &acpWebSocketStream{ctx: wireCtx, conn: socket}
		go keepACPWebSocketAlive(wireCtx, socket, 25*time.Second)
	}
	conn := acp.NewClientSideConnection(client, stream, stream)
	conn.SetLogger(acpLogger(e.stderrWriter()))
	defer func() {
		closeWire()
		_ = stream.Close()
		<-conn.Done()
	}()
	defer watchACPCancellation(ctx, conn, client, func() { _ = stream.Close() })()
	err = e.acpTurn(ctx, conn, client, def.Cwd)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("harness: remote ACP: %w", err)
	}
	return nil
}

// sendsCleartext reports whether a ws:// or http:// URL leaves the local host.
func sendsCleartext(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "http") {
		return false
	}
	if host := u.Hostname(); host != "localhost" {
		ip := net.ParseIP(host)
		return ip == nil || !ip.IsLoopback()
	}
	return false
}

// dialACPWebSocket authenticates the handshake without exposing credentials in errors.
func dialACPWebSocket(ctx context.Context, def *ir.HarnessDefinition, client *http.Client) (*websocket.Conn, error) {
	headers := make(http.Header, len(def.Headers))
	for key, value := range def.Headers {
		headers.Set(key, value)
	}
	protocols := []string{"acp.v1"}
	if def.BearerSubprotocol {
		auth := strings.Fields(headers.Get("Authorization"))
		if len(auth) != 2 || !strings.EqualFold(auth[0], "Bearer") || !httpguts.ValidHeaderFieldName("bearer."+auth[1]) {
			return nil, errors.New("harness: bearer_subprotocol requires a Bearer token valid as an HTTP token")
		}
		protocols = append(protocols, "bearer."+auth[1])
	}
	dialCtx, cancel := context.WithTimeout(ctx, acpRequestTimeout)
	defer cancel()
	socket, response, err := websocket.Dial(dialCtx, def.URL, &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: headers, Subprotocols: protocols,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Dial errors can include the URL, query parameters, or response body.
		if response != nil {
			return nil, fmt.Errorf("harness: ACP WebSocket connection failed (HTTP %d)", response.StatusCode)
		}
		return nil, acpTransportError("harness: ACP WebSocket connection failed", err)
	}
	socket.SetReadLimit(acpMessageLimit)
	return socket, nil
}

// acpWebSocketStream adapts complete text frames to the SDK's NDJSON stream.
// A bridge may also batch multiple newline-delimited messages in one frame.
type acpWebSocketStream struct {
	ctx     context.Context
	conn    *websocket.Conn
	pending io.Reader
}

// Read appends a delimiter at each text frame boundary for the SDK's scanner.
func (s *acpWebSocketStream) Read(p []byte) (int, error) {
	for {
		if s.pending != nil {
			n, err := s.pending.Read(p)
			if err != io.EOF {
				return n, err
			}
			s.pending = nil
			if n > 0 {
				return n, nil
			}
		}
		kind, data, err := s.conn.Read(s.ctx)
		if err != nil {
			return 0, err
		}
		if kind != websocket.MessageText {
			return 0, errors.New("ACP WebSocket requires text frames")
		}
		s.pending = io.MultiReader(bytes.NewReader(data), strings.NewReader("\n"))
	}
}

// Write preserves the SDK's NDJSON delimiter for bridges that forward frames to stdin.
func (s *acpWebSocketStream) Write(p []byte) (int, error) {
	if err := s.conn.Write(s.ctx, websocket.MessageText, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close interrupts blocked reads and writes without waiting for a peer handshake.
func (s *acpWebSocketStream) Close() error { return s.conn.CloseNow() }

// keepACPWebSocketAlive uses protocol-level ping frames across idle proxies.
func keepACPWebSocketAlive(ctx context.Context, socket *websocket.Conn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := socket.Ping(pingCtx)
			cancel()
			if err != nil {
				_ = socket.CloseNow()
				return
			}
		}
	}
}
