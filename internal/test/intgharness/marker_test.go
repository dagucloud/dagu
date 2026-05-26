// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMarkerBuildsPortableCommands(t *testing.T) {
	h := Harness{
		t:        t,
		Commands: commandsForShell(posixShell),
		Wait:     Waiter{t: t},
	}
	marker := h.Marker("/tmp/marker")

	require.Equal(t, "while [ ! -f '/tmp/marker' ]; do\n  sleep 0.05\ndone", marker.WaitCommand())
	require.Equal(t, "printf '%s' 'started' > '/tmp/marker'", marker.WriteCommand("started"))
	require.Equal(t, "printf '%s' '' > '/tmp/marker'", marker.TouchCommand())
}

func TestMarkerWaitsForExistenceAndRemoval(t *testing.T) {
	h := Harness{
		t:        t,
		Commands: commandsForShell(posixShell),
		Wait:     Waiter{t: t},
	}
	path := filepath.Join(t.TempDir(), "marker")
	marker := h.Marker(path)

	marker.Write("ok")
	marker.RequireExists(time.Second)
	require.NoError(t, os.Remove(path))
	marker.RequireMissing(time.Second)
}
