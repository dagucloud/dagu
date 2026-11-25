// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package dag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// mkdirWithOtherName creates a directory in parent and returns its path and
// another name that opens it: its short name. It skips the test on a volume
// that keeps no short names.
func mkdirWithOtherName(t *testing.T, parent string) (string, string) {
	t.Helper()
	dir := filepath.Join(parent, "long directory name")
	require.NoError(t, os.Mkdir(dir, 0750))

	longPath, err := windows.UTF16PtrFromString(dir)
	require.NoError(t, err)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(longPath, &buf[0], uint32(len(buf)))
	require.NoError(t, err)
	otherName := filepath.Base(windows.UTF16ToString(buf[:n]))
	if strings.EqualFold(otherName, filepath.Base(dir)) {
		t.Skip("the volume keeps no short names")
	}
	return dir, otherName
}
