// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package fileutil

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// requireOwnerOnly fails the test unless the access list of path grants
// access to the current user only and inherits nothing from its parent.
func requireOwnerOnly(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	dacl, _, err := sd.DACL()
	require.NoError(t, err)
	require.NotNil(t, dacl, "%s has no access list, which grants everyone access", path)
	require.NotZero(t, dacl.AceCount)
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		require.NoError(t, windows.GetAce(dacl, i, &ace))
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		require.True(t, sid.Equals(user.User.Sid), "%s grants access to %s", path, sid)
	}
}
