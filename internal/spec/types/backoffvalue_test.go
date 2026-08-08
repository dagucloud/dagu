// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackoffValue_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		yaml           string
		wantMultiplier float64
		wantSet        bool
		wantErr        bool
	}{
		{
			name:           "bool true",
			yaml:           "true",
			wantMultiplier: 2.0,
			wantSet:        true,
		},
		{
			name:           "bool false",
			yaml:           "false",
			wantMultiplier: 0,
			wantSet:        true,
		},
		{
			name:           "integer",
			yaml:           "3",
			wantMultiplier: 3.0,
			wantSet:        true,
		},
		{
			name:           "float",
			yaml:           "1.5",
			wantMultiplier: 1.5,
			wantSet:        true,
		},
		{
			name:    "invalid multiplier <= 1.0",
			yaml:    "0.5",
			wantErr: true,
		},
		{
			name:    "invalid multiplier == 1.0",
			yaml:    "1.0",
			wantErr: true,
		},
		{
			name:    "nil",
			yaml:    "null",
			wantSet: false,
		},
		{
			name:    "string rejected",
			yaml:    `"fast"`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var v BackoffValue
			err := v.UnmarshalYAML([]byte(tt.yaml))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSet, v.IsSet())
			assert.InDelta(t, tt.wantMultiplier, v.Multiplier(), 0.001)
		})
	}
}
