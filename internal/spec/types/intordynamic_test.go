// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntOrDynamic_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantInt int
		wantStr string
		wantDyn bool
		wantSet bool
		wantErr bool
	}{
		{
			name:    "integer",
			yaml:    "3",
			wantInt: 3,
			wantSet: true,
		},
		{
			name:    "zero integer",
			yaml:    "0",
			wantInt: 0,
			wantSet: true,
		},
		{
			name:    "string integer",
			yaml:    `"5"`,
			wantInt: 5,
			wantSet: true,
		},
		{
			name:    "dynamic ${VAR}",
			yaml:    `"${REPEAT_LIMIT}"`,
			wantStr: "${REPEAT_LIMIT}",
			wantDyn: true,
			wantSet: true,
		},
		{
			name:    "dynamic $VAR",
			yaml:    `"$REPEAT_LIMIT"`,
			wantStr: "$REPEAT_LIMIT",
			wantDyn: true,
			wantSet: true,
		},
		{
			name:    "dynamic backtick quoted in yaml",
			yaml:    "\"`echo 3`\"",
			wantStr: "`echo 3`",
			wantDyn: true,
			wantSet: true,
		},
		{
			name:    "nil unsets",
			yaml:    "null",
			wantSet: false,
		},
		{
			name:    "invalid string",
			yaml:    `"abc"`,
			wantErr: true,
		},
		{
			name:    "float rejected",
			yaml:    "3.5",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var v IntOrDynamic
			err := v.UnmarshalYAML([]byte(tt.yaml))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSet, !v.IsZero())
			assert.Equal(t, tt.wantInt, v.Int())
			assert.Equal(t, tt.wantStr, v.Str())
			assert.Equal(t, tt.wantDyn, v.IsDynamic())
		})
	}
}
