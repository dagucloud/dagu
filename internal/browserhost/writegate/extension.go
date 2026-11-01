// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// The extension that blocks the browser's sockets. DevTools cannot pause a
// WebSocket or a WebTransport session, so a declarativeNetRequest rule
// refuses them for pages, frames, and dedicated workers. Service workers
// and shared workers open theirs past the rule; the gate watches those.
const (
	extensionManifest = `{
  "manifest_version": 3,
  "name": "Dagu write gate",
  "version": "1.0",
  "description": "Blocks the WebSockets of a browser whose writes Dagu decides.",
  "permissions": ["declarativeNetRequest"],
  "host_permissions": ["<all_urls>"],
  "declarative_net_request": {"rule_resources": [{"id": "sockets", "enabled": true, "path": "rules.json"}]}
}
`
	extensionRules = `[
  {"id": 1, "priority": 1, "action": {"type": "block"}, "condition": {"resourceTypes": ["websocket", "webtransport"]}}
]
`
	extensionDirMode  = 0o700
	extensionFileMode = 0o600
)

const (
	// socketRuleTimeout bounds the wait for the browser to apply the
	// extension's rule, which it does a moment after it loads the
	// extension.
	socketRuleTimeout = 30 * time.Second
	// socketProbePause spaces the probes of the rule.
	socketProbePause = 100 * time.Millisecond
)

// socketProbe opens a socket to the port %d from a page and says whether
// it opened, failed, or neither in time.
const socketProbe = `new Promise((done) => {
  const socket = new WebSocket('ws://127.0.0.1:%d/');
  socket.onopen = () => done('open');
  socket.onerror = () => done('failed');
  setTimeout(() => done('timeout'), 2000);
})`

// awaitSocketRule returns once the extension refuses a page's socket: one
// opened from a page in the background fails without reaching a port the
// gate listens on. Until then, a socket would be sent.
func (g *gate) awaitSocketRule(ctx context.Context) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for the socket probe: %w", err)
	}
	defer func() { _ = listener.Close() }()
	var reached atomic.Int64
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			reached.Add(1)
			_ = conn.Close()
		}
	}()
	raw, err := g.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": true})
	if err != nil {
		return err
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return err
	}
	defer g.send("", "Target.closeTarget", map[string]any{"targetId": created.TargetID})
	raw, err = g.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true})
	if err != nil {
		return err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &attached); err != nil {
		return err
	}
	expression := fmt.Sprintf(socketProbe, listener.Addr().(*net.TCPAddr).Port)
	deadline := time.Now().Add(socketRuleTimeout)
	for {
		before := reached.Load()
		raw, err := g.call(ctx, attached.SessionID, "Runtime.evaluate", map[string]any{"expression": expression, "awaitPromise": true, "returnByValue": true})
		if err != nil {
			return err
		}
		var result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &result) == nil && result.Result.Value == "failed" && reached.Load() == before {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the browser did not apply the write gate's socket rule in time")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(socketProbePause):
		}
	}
}

// writeExtension writes the socket-blocking extension into dir.
func writeExtension(dir string) error {
	if err := os.MkdirAll(dir, extensionDirMode); err != nil {
		return fmt.Errorf("create the write gate extension: %w", err)
	}
	for name, content := range map[string]string{"manifest.json": extensionManifest, "rules.json": extensionRules} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), extensionFileMode); err != nil {
			return fmt.Errorf("write the write gate extension: %w", err)
		}
	}
	return nil
}
