// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Models spell keys as xdotool keysyms, browser key names or plain words.
func TestParseKey(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]desktop.Key{
		"Return":     desktop.KeyEnter,
		"ENTER":      desktop.KeyEnter,
		"ctrl":       desktop.KeyCtrl,
		"CONTROL":    desktop.KeyCtrl,
		"Control_L":  desktop.KeyCtrl,
		"ShiftRight": desktop.KeyShift,
		"cmd":        desktop.KeyMeta,
		"super":      desktop.KeyMeta,
		"Super_L":    desktop.KeyMeta,
		"option":     desktop.KeyAlt,
		"Page_Down":  desktop.KeyPageDown,
		"ARROWLEFT":  desktop.KeyLeft,
		"BackSpace":  desktop.KeyBackspace,
		"ESC":        desktop.KeyEscape,
		"F12":        "f12",
		"A":          "a",
		"/":          "/",
		"minus":      "-",
		"+":          "+",
	} {
		got, err := desktop.ParseKey(name)
		require.NoError(t, err, name)
		assert.Equal(t, want, got, name)
	}

	for _, name := range []string{"F25", "hyper", "é", ""} {
		_, err := desktop.ParseKey(name)
		assert.Error(t, err, name)
	}
}

func TestKeyUSKey(t *testing.T) {
	t.Parallel()

	base, shift, ok := desktop.Key("?").USKey()
	require.True(t, ok)
	assert.Equal(t, '/', base)
	assert.True(t, shift)

	base, shift, ok = desktop.Key("a").USKey()
	require.True(t, ok)
	assert.Equal(t, 'a', base)
	assert.False(t, shift)

	_, _, ok = desktop.KeyEnter.USKey()
	assert.False(t, ok)
}

func TestKeyFunctionNumber(t *testing.T) {
	t.Parallel()

	n, ok := desktop.Key("f7").FunctionNumber()
	assert.True(t, ok)
	assert.Equal(t, 7, n)

	_, ok = desktop.KeyEnter.FunctionNumber()
	assert.False(t, ok)
}
