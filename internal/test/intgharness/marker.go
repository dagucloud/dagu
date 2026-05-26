// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"os"
	"time"
)

// Marker represents a filesystem marker used to coordinate integration steps.
type Marker struct {
	h    Harness
	path string
}

// Marker returns a probe for a filesystem marker.
func (h Harness) Marker(path string) Marker {
	return Marker{h: h, path: path}
}

// Path returns the marker path.
func (m Marker) Path() string {
	return m.path
}

// WaitCommand returns a portable command that blocks until the marker exists.
func (m Marker) WaitCommand() string {
	return m.h.Commands.WaitForFile(m.path)
}

// WriteCommand returns a portable command that writes content to the marker.
func (m Marker) WriteCommand(content string) string {
	return m.h.Commands.WriteFile(m.path, content)
}

// TouchCommand returns a portable command that creates the marker.
func (m Marker) TouchCommand() string {
	return m.WriteCommand("")
}

// Write writes content to the marker path.
func (m Marker) Write(content string) {
	m.h.t.Helper()
	if err := os.WriteFile(m.path, []byte(content), 0o600); err != nil {
		m.h.t.Fatalf("write marker %q: %v", m.path, err)
	}
}

// RequireExists waits until the marker exists.
func (m Marker) RequireExists(timeout time.Duration) {
	m.h.t.Helper()
	m.h.Wait.FileExists(m.path, timeout)
}

// RequireMissing waits until the marker does not exist.
func (m Marker) RequireMissing(timeout time.Duration) {
	m.h.t.Helper()
	m.h.Wait.FileMissing(m.path, timeout)
}
