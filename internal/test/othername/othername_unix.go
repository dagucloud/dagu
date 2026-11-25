// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

// Package othername creates directories that a second name also opens, for
// tests that compare paths by file identity.
package othername

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Mkdir creates a directory in parent and returns its path and another name
// that opens it: the name in decomposed Unicode form, which file systems that
// ignore normalization, such as APFS, accept. It skips the test on a file
// system that tells the forms apart.
func Mkdir(t *testing.T, parent string) (string, string) {
	t.Helper()
	dir := filepath.Join(parent, "café")
	require.NoError(t, os.Mkdir(dir, 0750))
	otherName := "café"

	want, err := os.Stat(dir)
	require.NoError(t, err)
	got, err := os.Stat(filepath.Join(parent, otherName))
	if err != nil || !os.SameFile(want, got) {
		t.Skip("the file system tells Unicode normalization forms apart")
	}
	return dir, otherName
}
