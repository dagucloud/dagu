// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckMisleadingStepValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		expr        string
		wantWarning bool
		contains    []string
	}{
		{
			name:        "minute step 33 warns",
			expr:        "*/33 * * * *",
			wantWarning: true,
			contains:    []string{"*/33", "minute field", "not every 33 minutes"},
		},
		{
			name:        "minute step 5 is valid",
			expr:        "*/5 * * * *",
			wantWarning: false,
		},
		{
			name:        "minute step 15 is valid",
			expr:        "*/15 * * * *",
			wantWarning: false,
		},
		{
			name:        "minute step 7 warns",
			expr:        "*/7 * * * *",
			wantWarning: true,
			contains:    []string{"*/7", "minute field", "not every 7 minutes"},
		},
		{
			name:        "hour step 5 is valid",
			expr:        "0 */5 * * *",
			wantWarning: false,
		},
		{
			name:        "hour step 7 warns",
			expr:        "0 */7 * * *",
			wantWarning: true,
			contains:    []string{"*/7", "hour field", "not every 7 hours"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			warnings := checkMisleadingStepValues(tt.expr)
			if tt.wantWarning {
				require.NotEmpty(t, warnings)
				for _, s := range tt.contains {
					require.Contains(t, warnings[0], s)
				}
				return
			}

			require.Empty(t, warnings)
		})
	}
}
