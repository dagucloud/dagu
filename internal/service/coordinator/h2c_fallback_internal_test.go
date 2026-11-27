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
)

// Windows a process opened stay open when the record can be read but no
// longer written.
func TestH2CWindowsKeepUnwrittenWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only directories")
	}
	dir := t.TempDir()
	windows := &h2cWindows{file: filepath.Join(dir, "peer-h2c.json"), records: make(map[string]h2cWindow)}
	now := time.Now()
	window := h2cWindow{Until: now.Add(h2cWindowLength), Source: sourceNetworkPath}
	require.True(t, windows.open("a:1", window, now))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	require.True(t, windows.open("b:1", window, now))
	require.True(t, windows.open("c:1", window, now))
	for _, address := range []string{"a:1", "b:1", "c:1"} {
		_, open := windows.active(address, now)
		assert.True(t, open, address)
	}

	// A window renewed in memory outlives the older one in the record.
	later := now.Add(h2cWindowLength)
	require.True(t, windows.open("a:1", h2cWindow{Until: later.Add(h2cWindowLength), Source: sourceNetworkPath}, later))
	require.True(t, windows.open("d:1", window, now))
	_, open := windows.active("a:1", later.Add(time.Minute))
	assert.True(t, open, "renewed window")
}
