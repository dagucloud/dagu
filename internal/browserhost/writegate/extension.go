// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"fmt"
	"os"
	"path/filepath"
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
