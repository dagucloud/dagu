// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The structs COM writes into must match their Win32 layout.
func TestVariantLayout(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uintptr(24), unsafe.Sizeof(variant{}))
	assert.Equal(t, uintptr(8), unsafe.Offsetof(variant{}.val))
	assert.Equal(t, uintptr(16), unsafe.Sizeof(rect{}))
}

func TestPointArg(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uintptr(0x0000002000000010), pointArg(16, 32))
	assert.Equal(t, uintptr(0x00000020FFFFFFFF), pointArg(-1, 32), "a negative coordinate keeps its sign in its half")
}

func TestRoleOf(t *testing.T) {
	t.Parallel()

	assert.Equal(t, RoleButton, roleOf(uiaSplitButton, false))
	assert.Equal(t, RoleTextField, roleOf(uiaDocument, false))
	assert.Equal(t, RoleGroup, roleOf(uiaGroup, false))
	assert.Equal(t, RoleOther, roleOf(50033, false), "a pane is other")
	assert.Equal(t, RoleWindow, roleOf(50033, true), "a top-level pane is a window")
}

// The automation client opens in any session and its thread ends on
// Close.
func TestOpenElementsCloses(t *testing.T) {
	t.Parallel()

	els, err := OpenElements()
	require.NoError(t, err)
	require.NoError(t, els.Close())
	require.NoError(t, els.Close(), "closing twice is harmless")
	_, err = els.FrontWindow()
	assert.ErrorIs(t, err, errElementsClosed)
}
