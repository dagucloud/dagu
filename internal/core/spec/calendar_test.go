// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/core"
)

func TestBuildCalendar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected *core.CalendarConfig
		wantErr  string
	}{
		{
			name:     "NilReturnsNil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "StringForm",
			input:    "jp-banking",
			expected: &core.CalendarConfig{Name: "jp-banking"},
		},
		{
			name: "ObjectForm",
			input: map[string]any{
				"name": "jp-banking",
				"days": "last-business-day",
			},
			expected: &core.CalendarConfig{
				Name: "jp-banking",
				Days: core.CalendarDayFilterLastBusinessDay,
			},
		},
		{
			name:     "ObjectFormNameOnly",
			input:    map[string]any{"name": "ops"},
			expected: &core.CalendarConfig{Name: "ops"},
		},
		{
			name:    "EmptyStringName",
			input:   "  ",
			wantErr: "calendar name is required",
		},
		{
			name:    "ObjectMissingName",
			input:   map[string]any{"days": "business-days"},
			wantErr: "calendar name is required",
		},
		{
			name:    "InvalidName",
			input:   "../escape",
			wantErr: "calendar name must start with",
		},
		{
			name:    "InvalidDays",
			input:   map[string]any{"name": "ops", "days": "weekdays"},
			wantErr: "invalid calendar day filter",
		},
		{
			name:    "UnknownKey",
			input:   map[string]any{"name": "ops", "shift": "next"},
			wantErr: "invalid keys",
		},
		{
			name:    "InvalidType",
			input:   42,
			wantErr: "must be a string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := &dag{Calendar: tt.input}
			result, err := buildCalendar(testBuildContext(), d)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadYAMLCalendar(t *testing.T) {
	t.Parallel()

	t.Run("StringForm", func(t *testing.T) {
		t.Parallel()
		dag, err := LoadYAML(context.Background(), []byte(`
schedule: "0 1 * * *"
calendar: jp-banking
steps:
  - name: s1
    command: "true"
`))
		require.NoError(t, err)
		require.NotNil(t, dag.Calendar)
		assert.Equal(t, "jp-banking", dag.Calendar.Name)
		assert.Equal(t, core.CalendarDayFilterNone, dag.Calendar.Days)
	})

	t.Run("ObjectForm", func(t *testing.T) {
		t.Parallel()
		dag, err := LoadYAML(context.Background(), []byte(`
schedule: "0 1 * * *"
calendar:
  name: jp-banking
  days: business-days
steps:
  - name: s1
    command: "true"
`))
		require.NoError(t, err)
		require.NotNil(t, dag.Calendar)
		assert.Equal(t, "jp-banking", dag.Calendar.Name)
		assert.Equal(t, core.CalendarDayFilterBusinessDays, dag.Calendar.Days)
	})

	t.Run("InvalidDays", func(t *testing.T) {
		t.Parallel()
		_, err := LoadYAML(context.Background(), []byte(`
calendar:
  name: jp-banking
  days: weekdays
steps:
  - name: s1
    command: "true"
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid calendar day filter")
	})
}
