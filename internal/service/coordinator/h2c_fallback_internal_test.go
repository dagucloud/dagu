// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// Windows a process opened stay open when the record can be read but no
// longer written, also one it renewed.
func TestH2CWindowsKeepUnwrittenWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only directories")
	}
	dir := t.TempDir()
	windows := newH2CWindows(filepath.Join(dir, "peer-h2c.json"), "host-a")
	now := time.Now()
	require.True(t, windows.open("a:1", windowUntil(now), now))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	require.True(t, windows.open("b:1", windowUntil(now), now))
	require.True(t, windows.open("c:1", windowUntil(now), now))
	assertOpen(t, windows, now, "a:1", "b:1", "c:1")

	later := now.Add(h2cWindowLength)
	require.True(t, windows.open("a:1", windowUntil(later), later))
	require.True(t, windows.open("d:1", windowUntil(later), later))
	assertOpen(t, windows, later.Add(time.Minute), "a:1", "d:1")
}

// A window in the record lasts at most 10 minutes from when a process reads
// it, whatever the clock of the process that wrote it.
func TestH2CWindowsBoundSkewedClock(t *testing.T) {
	file := filepath.Join(t.TempDir(), "peer-h2c.json")
	now := time.Now()
	ahead := now.AddDate(1, 0, 0)
	require.True(t, newH2CWindows(file, "host-a").open("a:1", windowUntil(ahead), ahead))

	reader := newH2CWindows(file, "host-a")
	assertOpen(t, reader, now, "a:1")
	_, open := reader.active("a:1", now.Add(h2cWindowLength))
	assert.False(t, open)

	// A save by the reader does not write the far end back either.
	require.True(t, reader.open("b:1", windowUntil(now), now))
	_, open = newH2CWindows(file, "host-a").active("a:1", now.Add(h2cWindowLength))
	assert.False(t, open)
}

// A window that one process ended stays ended when another process, which
// read it earlier, saves its own.
func TestH2CWindowsKeepEndedWindowsEnded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "peer-h2c.json")
	now := time.Now()
	first, second := newH2CWindows(file, "host-a"), newH2CWindows(file, "host-a")
	require.True(t, first.open("x:1", windowUntil(now), now))
	require.True(t, second.open("y:1", windowUntil(now), now))
	first.end("x:1", now)
	require.True(t, second.open("z:1", windowUntil(now), now))

	later := newH2CWindows(file, "host-a")
	_, open := later.active("x:1", now)
	assert.False(t, open, "ended window")
	assertOpen(t, later, now, "y:1", "z:1")
}

// On a data directory that hosts share, a host uses only its own windows,
// and keeps the windows of the others in the record.
func TestH2CWindowsPerHost(t *testing.T) {
	file := filepath.Join(t.TempDir(), "peer-h2c.json")
	now := time.Now()
	require.True(t, newH2CWindows(file, "host-a").open("x:1", windowUntil(now), now))

	other := newH2CWindows(file, "host-b")
	_, open := other.active("x:1", now)
	assert.False(t, open, "another host's window")
	require.True(t, other.open("y:1", windowUntil(now), now))

	assertOpen(t, newH2CWindows(file, "host-a"), now, "x:1")
	data, err := fileutil.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(data), "host-a")
	assert.Contains(t, string(data), "host-b")
}

func windowUntil(now time.Time) h2cWindow {
	return h2cWindow{Until: now.Add(h2cWindowLength), Source: sourceNetworkPath}
}

func assertOpen(t *testing.T, windows *h2cWindows, now time.Time, addresses ...string) {
	t.Helper()
	for _, address := range addresses {
		_, open := windows.active(address, now)
		assert.True(t, open, address)
	}
}
