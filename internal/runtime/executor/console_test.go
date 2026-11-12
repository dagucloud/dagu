// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodingForCodePage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		codePage uint32
		input    []byte
		want     string
	}{
		{
			name:     "ShiftJIS",
			codePage: 932,
			input:    []byte{0x94, 0xad, 0x90, 0xb6, 0x8f, 0xea, 0x8f, 0x8a},
			want:     "発生場所",
		},
		{
			name:     "OEMUnitedStates",
			codePage: 437,
			input:    []byte{0x82},
			want:     "é",
		},
		{
			name:     "WindowsWestern",
			codePage: 1252,
			input:    []byte{0xe9},
			want:     "é",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			enc := encodingForCodePage(tt.codePage)
			require.NotNil(t, enc)
			got, err := enc.NewDecoder().Bytes(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestEncodingForCodePage_UTF8(t *testing.T) {
	t.Parallel()
	assert.Nil(t, encodingForCodePage(65001))
}
