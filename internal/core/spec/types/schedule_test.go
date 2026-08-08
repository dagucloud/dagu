// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/spec/types"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scheduleExpressions(schedules []ir.Schedule) []string {
	result := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		result = append(result, schedule.DisplayValue())
	}
	return result
}

func scheduleKinds(schedules []ir.Schedule) []ir.ScheduleKind {
	result := make([]ir.ScheduleKind, 0, len(schedules))
	for _, schedule := range schedules {
		result = append(result, schedule.GetKind())
	}
	return result
}

func scheduleProfiles(schedules []ir.Schedule) []string {
	result := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		result = append(result, schedule.Profile)
	}
	return result
}

func TestScheduleValue_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		input               string
		wantErr             bool
		errContains         string
		wantStarts          []string
		wantStops           []string
		wantRestarts        []string
		wantStartProfiles   []string
		wantStopProfiles    []string
		wantRestartProfiles []string
		wantHasStop         bool
		wantHasRestart      bool
		checkHasStop        bool
		checkHasRestart     bool
	}{
		{
			name:            "SingleCronExpression",
			input:           `"0 * * * *"`,
			wantStarts:      []string{"0 * * * *"},
			checkHasStop:    true,
			wantHasStop:     false,
			checkHasRestart: true,
			wantHasRestart:  false,
		},
		{
			name:       "ArrayOfCronExpressions",
			input:      `["0 * * * *", "30 * * * *"]`,
			wantStarts: []string{"0 * * * *", "30 * * * *"},
		},
		{
			name: "MultilineArray",
			input: `
- "0 8 * * *"
- "0 12 * * *"
- "0 18 * * *"
`,
			wantStarts: []string{"0 8 * * *", "0 12 * * *", "0 18 * * *"},
		},
		{
			name:         "MapWithStartOnly",
			input:        `start: "0 8 * * *"`,
			wantStarts:   []string{"0 8 * * *"},
			checkHasStop: true,
			wantHasStop:  false,
		},
		{
			name: "MapWithStartAndStop",
			input: `
start: "0 8 * * *"
stop: "0 18 * * *"
`,
			wantStarts:   []string{"0 8 * * *"},
			wantStops:    []string{"0 18 * * *"},
			checkHasStop: true,
			wantHasStop:  true,
		},
		{
			name: "MapWithAllKeys",
			input: `
start: "0 8 * * *"
stop: "0 18 * * *"
restart: "0 0 * * *"
`,
			wantStarts:      []string{"0 8 * * *"},
			wantStops:       []string{"0 18 * * *"},
			wantRestarts:    []string{"0 0 * * *"},
			checkHasRestart: true,
			wantHasRestart:  true,
		},
		{
			name: "MapWithArrayValues",
			input: `
start:
  - "0 8 * * *"
  - "0 12 * * *"
stop: "0 18 * * *"
`,
			wantStarts: []string{"0 8 * * *", "0 12 * * *"},
			wantStops:  []string{"0 18 * * *"},
		},
		{
			name: "ProfileScopedObjectEntries",
			input: `
start:
  - expression: "*/20 * * * *"
    profile: prod
  - expression: "30 */2 * * *"
    profile: dev
stop:
  expression: "0 18 * * *"
  profile: prod
restart:
  - expression: "0 12 * * *"
    profile: dev
`,
			wantStarts:          []string{"*/20 * * * *", "30 */2 * * *"},
			wantStops:           []string{"0 18 * * *"},
			wantRestarts:        []string{"0 12 * * *"},
			wantStartProfiles:   []string{"prod", "dev"},
			wantStopProfiles:    []string{"prod"},
			wantRestartProfiles: []string{"dev"},
		},
		{
			name:        "InvalidMapKey",
			input:       `invalid: "0 * * * *"`,
			wantErr:     true,
			errContains: "unknown key",
		},
		{
			name:        "InvalidScheduleProfileKey",
			input:       `profile: 123`,
			wantErr:     true,
			errContains: "unknown key",
		},
		{
			name: "InvalidScheduleProfileName",
			input: `
start:
  expression: "0 * * * *"
  profile: Prod
`,
			wantErr:     true,
			errContains: "invalid profile name",
		},
		{
			name:        "RejectCronAlias",
			input:       `start: {cron: "0 * * * *"}`,
			wantErr:     true,
			errContains: "unknown key",
		},
		{
			name: "InvalidArrayElementType",
			input: `
start:
  - 123
  - 456
`,
			wantErr:     true,
			errContains: "expected string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s types.ScheduleValue
			err := yaml.Unmarshal([]byte(tt.input), &s)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if tt.wantStarts != nil {
				assert.Equal(t, tt.wantStarts, scheduleExpressions(s.Starts()))
			}
			if tt.wantStops != nil {
				assert.Equal(t, tt.wantStops, scheduleExpressions(s.Stops()))
			}
			if tt.wantRestarts != nil {
				assert.Equal(t, tt.wantRestarts, scheduleExpressions(s.Restarts()))
			}
			if tt.wantStartProfiles != nil {
				assert.Equal(t, tt.wantStartProfiles, scheduleProfiles(s.Starts()))
			}
			if tt.wantStopProfiles != nil {
				assert.Equal(t, tt.wantStopProfiles, scheduleProfiles(s.Stops()))
			}
			if tt.wantRestartProfiles != nil {
				assert.Equal(t, tt.wantRestartProfiles, scheduleProfiles(s.Restarts()))
			}
			if tt.checkHasStop {
				assert.Equal(t, tt.wantHasStop, s.HasStopSchedule())
			}
			if tt.checkHasRestart {
				assert.Equal(t, tt.wantHasRestart, s.HasRestartSchedule())
			}
		})
	}

	t.Run("ZeroValue", func(t *testing.T) {
		t.Parallel()
		var s types.ScheduleValue
		assert.True(t, s.IsZero())
		assert.Nil(t, s.Starts())
	})
}

func TestScheduleValue_InStruct(t *testing.T) {
	t.Parallel()

	type DAGConfig struct {
		Name     string              `yaml:"name"`
		Schedule types.ScheduleValue `yaml:"schedule"`
	}

	tests := []struct {
		name       string
		input      string
		wantName   string
		wantStarts []string
		wantStops  []string
		wantIsZero bool
	}{
		{
			name: "SimpleSchedule",
			input: `
name: my-dag
schedule: "0 * * * *"
`,
			wantName:   "my-dag",
			wantStarts: []string{"0 * * * *"},
		},
		{
			name: "ComplexSchedule",
			input: `
name: my-dag
schedule:
  start: "0 8 * * 1-5"
  stop: "0 18 * * 1-5"
`,
			wantStarts: []string{"0 8 * * 1-5"},
			wantStops:  []string{"0 18 * * 1-5"},
		},
		{
			name:       "NoSchedule",
			input:      "name: my-dag",
			wantIsZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cfg DAGConfig
			err := yaml.Unmarshal([]byte(tt.input), &cfg)
			require.NoError(t, err)
			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, cfg.Name)
			}
			if tt.wantStarts != nil {
				assert.Equal(t, tt.wantStarts, scheduleExpressions(cfg.Schedule.Starts()))
			}
			if tt.wantStops != nil {
				assert.Equal(t, tt.wantStops, scheduleExpressions(cfg.Schedule.Stops()))
			}
			if tt.wantIsZero {
				assert.True(t, cfg.Schedule.IsZero())
			}
		})
	}
}

func TestScheduleValue_TypedScheduleValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		errContains string
	}{
		{
			name:        "LegacyCronWithoutKind",
			input:       "start:\n  expression: \"0 * * * *\"\n",
			errContains: "",
		},
		{
			name:        "RejectsBothExpressionAndAt",
			input:       "start:\n  kind: cron\n  expression: \"0 * * * *\"\n  at: \"2026-03-29T02:10:00+01:00\"\n",
			errContains: "must not include both expression and at",
		},
		{
			name:        "RejectsCronWithoutExpression",
			input:       "start:\n  kind: cron\n",
			errContains: "cron schedules must include expression",
		},
		{
			name:        "RejectsAtWithExpression",
			input:       "start:\n  kind: at\n  expression: \"0 * * * *\"\n",
			errContains: "one-off schedules must include at",
		},
		{
			name:        "RejectsOneOffInStopSchedule",
			input:       "stop:\n  at: \"2026-03-29T02:10:00+01:00\"\n",
			errContains: "one-off schedules are only supported for start schedules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var s types.ScheduleValue
			err := yaml.Unmarshal([]byte(tt.input), &s)
			if tt.errContains == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestScheduleValue_AdditionalCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains string
		wantValue   any
		checkIsZero bool
		wantStarts  []string
	}{
		{
			name:      "ValueReturnsRawString",
			input:     `"0 * * * *"`,
			wantValue: "0 * * * *",
		},
		{
			name:        "NullValue",
			input:       "null",
			checkIsZero: true,
		},
		{
			name:       "EmptyString",
			input:      `""`,
			wantStarts: nil,
		},
		{
			name:        "InvalidTypeNumber",
			input:       "123",
			wantErr:     true,
			errContains: "must be string, array, or map",
		},
		{
			name:        "InvalidScheduleEntryTypeInMap",
			input:       "start: 123",
			wantErr:     true,
			errContains: "expected string, object, or array",
		},
		{
			name:        "InvalidTypeInStartArray",
			input:       `["0 * * * *", 123]`,
			wantErr:     true,
			errContains: "expected string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s types.ScheduleValue
			err := yaml.Unmarshal([]byte(tt.input), &s)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if tt.wantValue != nil {
				assert.Equal(t, tt.wantValue, s.Value())
			}
			if tt.checkIsZero {
				assert.True(t, s.IsZero())
			}
			if tt.wantStarts != nil {
				assert.Equal(t, tt.wantStarts, scheduleExpressions(s.Starts()))
			}
		})
	}

	t.Run("ValueReturnsRawArray", func(t *testing.T) {
		t.Parallel()
		var s types.ScheduleValue
		err := yaml.Unmarshal([]byte(`["0 * * * *"]`), &s)
		require.NoError(t, err)
		val, ok := s.Value().([]any)
		require.True(t, ok)
		assert.Len(t, val, 1)
	})

	t.Run("EmptyStringNotZero", func(t *testing.T) {
		t.Parallel()
		var s types.ScheduleValue
		err := yaml.Unmarshal([]byte(`""`), &s)
		require.NoError(t, err)
		assert.False(t, s.IsZero())
		assert.Nil(t, s.Starts())
	})

	t.Run("OneOffStartEntry", func(t *testing.T) {
		t.Parallel()
		var s types.ScheduleValue
		err := yaml.Unmarshal([]byte(`
start:
  - at: "2026-03-29T02:10:00+01:00"
`), &s)
		require.NoError(t, err)
		require.Equal(t, []ir.ScheduleKind{ir.ScheduleKindAt}, scheduleKinds(s.Starts()))
		assert.Equal(t, []string{"2026-03-29T02:10:00+01:00"}, scheduleExpressions(s.Starts()))
	})

	t.Run("OneOffStopRejected", func(t *testing.T) {
		t.Parallel()
		var s types.ScheduleValue
		err := yaml.Unmarshal([]byte(`
stop:
  - at: "2026-03-29T02:10:00+01:00"
`), &s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "only supported for start schedules")
	})

	t.Run("ZoneLessOneOffRejected", func(t *testing.T) {
		t.Parallel()
		var s types.ScheduleValue
		err := yaml.Unmarshal([]byte(`
start:
  - at: "2026-03-29T02:10:00"
`), &s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "explicit offset")
	})
}
