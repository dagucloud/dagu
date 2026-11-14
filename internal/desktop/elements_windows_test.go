// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"syscall"
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

// The pattern layer's indexes and ids match UIAutomationClient.h.
func TestPatternConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 14, uiaGetCurrentPatternAs)
	assert.Equal(t, 28, uiaGetCurrentIsEnabled)
	assert.Equal(t, 63, uia2PutTransactionTimeout)
	assert.Equal(t, []int{10000, 10002, 10005, 10010, 10015, 10017},
		[]int{uiaInvokePatternID, uiaValuePatternID, uiaExpandCollapsePatternID, uiaSelectionItemPatternID, uiaTogglePatternID, uiaScrollItemPatternID})
	assert.Less(t, transactionTimeout, elementsTimeout)
}

// A BSTR made here reads back as its text, embedded NULs included.
func TestBstrAlloc(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"保存", "a\x00b", ""} {
		b, err := bstrAlloc(text)
		require.NoError(t, err)
		assert.Equal(t, text, bstrString(b))
		bstrFree(b)
	}
}

// An element without a pattern answers with no interface, or with a result
// saying so, and both read as ErrNoPattern; a stale element is not found.
func TestPatternAsWithoutPattern(t *testing.T) {
	t.Parallel()

	for name, hr := range map[string]hresult{"S_OK with no interface": 0, "E_NOINTERFACE": hrNoInterface, "UIA_E_NOTSUPPORTED": hrNotSupported} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			el := fakeElement(hr)
			_, err := el.patternAs(uiaInvokePatternID, &iidInvokePattern)
			assert.ErrorIs(t, err, ErrNoPattern)
		})
	}
	_, err := fakeElement(hrElementNotAvailable).patternAs(uiaInvokePatternID, &iidInvokePattern)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = fakeElement(0x80004005).patternAs(uiaInvokePatternID, &iidInvokePattern)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "E_FAIL")
	assert.NotErrorIs(t, err, ErrNoPattern)
}

// fakeElement is an element whose GetCurrentPatternAs answers hr and
// writes nothing, so the out-pointer stays nil.
func fakeElement(hr hresult) *element {
	vtbl := new([128]uintptr)
	vtbl[uiaGetCurrentPatternAs] = syscall.NewCallback(func(_, _, _, _ uintptr) uintptr { return uintptr(hr) })
	return (*element)(&comObject{vtbl: vtbl})
}

// The automation client opens in any session and its thread ends on
// Close.
func TestOpenElementsCloses(t *testing.T) {
	t.Parallel()

	els, err := OpenElements()
	require.NoError(t, err)
	_, err = els.FrontWindow()
	require.NoError(t, err)
	err = els.Apply(Element{Role: RoleButton, Name: "nope", Path: []PathStep{{Role: RoleButton, Name: "nope"}}}, PatternInvoke, "")
	assert.ErrorIs(t, err, ErrNotFound, "a pattern call runs with a client of its own")
	require.NoError(t, els.Close())
	require.NoError(t, els.Close(), "closing twice is harmless")
	_, err = els.FrontWindow()
	assert.ErrorIs(t, err, errElementsClosed)
	assert.ErrorIs(t, els.Apply(Element{}, PatternInvoke, ""), errElementsClosed)
}
