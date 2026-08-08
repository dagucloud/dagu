// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepeatMode_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yaml     string
		wantMode string
		wantSet  bool
		wantBool bool
		wantErr  bool
	}{
		{
			name:     "bool true",
			yaml:     "true",
			wantMode: "while",
			wantSet:  true,
			wantBool: true,
		},
		{
			name:    "bool false",
			yaml:    "false",
			wantSet: false,
		},
		{
			name:     "string while",
			yaml:     `"while"`,
			wantMode: "while",
			wantSet:  true,
		},
		{
			name:     "string until",
			yaml:     `"until"`,
			wantMode: "until",
			wantSet:  true,
		},
		{
			name:    "invalid string",
			yaml:    `"always"`,
			wantErr: true,
		},
		{
			name:    "nil",
			yaml:    "null",
			wantSet: false,
		},
		{
			name:    "integer rejected",
			yaml:    "1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var v RepeatMode
			err := v.UnmarshalYAML([]byte(tt.yaml))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSet, v.IsSet())
			assert.Equal(t, tt.wantMode, v.String())
			assert.Equal(t, tt.wantBool, v.IsBool())
		})
	}
}
