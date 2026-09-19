// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPassEnvValue_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantSet   bool
		wantAll   bool
		wantNames []string
		wantErr   bool
	}{
		{
			name:    "bool true",
			yaml:    "true",
			wantSet: true,
			wantAll: true,
		},
		{
			name:    "bool false",
			yaml:    "false",
			wantSet: true,
		},
		{
			name:      "list",
			yaml:      "[TODAY, GH_USER]",
			wantSet:   true,
			wantNames: []string{"TODAY", "GH_USER"},
		},
		{
			name:      "flow list",
			yaml:      "[A]",
			wantSet:   true,
			wantNames: []string{"A"},
		},
		{
			name:    "empty list",
			yaml:    "[]",
			wantSet: true,
		},
		{
			name:    "null",
			yaml:    "null",
			wantSet: false,
		},
		{
			name:    "string rejected",
			yaml:    `"TODAY"`,
			wantErr: true,
		},
		{
			name:    "integer rejected",
			yaml:    "2",
			wantErr: true,
		},
		{
			name:    "non-string entry rejected",
			yaml:    "[TODAY, 2]",
			wantErr: true,
		},
		{
			name:    "map rejected",
			yaml:    "{name: TODAY}",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var v PassEnvValue
			err := v.UnmarshalYAML([]byte(tt.yaml))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSet, !v.IsZero())
			assert.Equal(t, tt.wantAll, v.All())
			assert.Equal(t, tt.wantNames, v.Names())
		})
	}
}
