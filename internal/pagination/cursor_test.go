// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package pagination

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchCursorRoundTrip(t *testing.T) {
	t.Parallel()

	want := struct {
		Name   string `json:"name"`
		Offset int    `json:"offset"`
	}{Name: "example", Offset: 42}
	raw := EncodeSearchCursor(want)
	require.NotEmpty(t, raw)

	var got struct {
		Name   string `json:"name"`
		Offset int    `json:"offset"`
	}
	require.NoError(t, DecodeSearchCursor(raw, &got))
	require.Equal(t, want, got)
	assert.ErrorIs(t, DecodeSearchCursor("%%%bad%%%", &got), ErrInvalidCursor)
}
