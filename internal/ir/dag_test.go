// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalJSON(t *testing.T) {
	th := test.Setup(t)
	t.Run("MarshalJSON", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "true"
`)
		_, err := json.Marshal(dag.DAG)
		require.NoError(t, err)
	})
}

func TestDAGUnmarshalJSONDeprecatedTags(t *testing.T) {
	t.Parallel()

	var dag ir.DAG
	err := json.Unmarshal([]byte(`{"name":"legacy","tags":["env=prod","team=platform"]}`), &dag)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"env=prod", "team=platform"}, dag.Labels.Strings())

	var explicitLabels ir.DAG
	err = json.Unmarshal([]byte(`{"name":"canonical","labels":[],"tags":["env=legacy"]}`), &explicitLabels)
	require.NoError(t, err)
	assert.Empty(t, explicitLabels.Labels.Strings())
}

func TestScheduleJSON(t *testing.T) {
	t.Parallel()

	t.Run("MarshalUnmarshalJSON", func(t *testing.T) {
		t.Parallel()

		original, err := ir.NewCronSchedule("0 0 * * *")
		require.NoError(t, err)

		data, err := json.Marshal(original)
		require.NoError(t, err)

		jsonStr := string(data)
		require.Contains(t, jsonStr, `"kind":"cron"`)
		require.Contains(t, jsonStr, `"expression":"0 0 * * *"`)

		var unmarshaled ir.Schedule
		require.NoError(t, json.Unmarshal(data, &unmarshaled))
		require.Equal(t, ir.ScheduleKindCron, unmarshaled.GetKind())
		require.Equal(t, original.Expression, unmarshaled.Expression)
		require.NotNil(t, unmarshaled.Parsed)

		now := time.Now()
		require.Equal(t, original.Parsed.Next(now), unmarshaled.Parsed.Next(now))
	})

	t.Run("UnmarshalInvalidCron", func(t *testing.T) {
		t.Parallel()

		var schedule ir.Schedule
		err := json.Unmarshal([]byte(`{"expression":"invalid cron"}`), &schedule)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid cron expression")
	})

	t.Run("UnmarshalLegacyCronJSON", func(t *testing.T) {
		t.Parallel()

		var schedule ir.Schedule
		err := json.Unmarshal([]byte(`{"expression":"0 0 * * *"}`), &schedule)
		require.NoError(t, err)
		require.Equal(t, ir.ScheduleKindCron, schedule.GetKind())
		require.Equal(t, "0 0 * * *", schedule.Expression)
		require.NotNil(t, schedule.Parsed)
	})

	t.Run("UnmarshalExpressionWithProfile", func(t *testing.T) {
		t.Parallel()

		var schedule ir.Schedule
		err := json.Unmarshal([]byte(`{"expression":"0 0 * * *","profile":" prod "}`), &schedule)
		require.NoError(t, err)
		require.Equal(t, ir.ScheduleKindCron, schedule.GetKind())
		require.Equal(t, "0 0 * * *", schedule.Expression)
		require.Equal(t, "prod", schedule.Profile)
		require.NotNil(t, schedule.Parsed)
		require.Contains(t, schedule.Fingerprint(), "|profile:prod")
	})

	t.Run("UnmarshalInvalidProfile", func(t *testing.T) {
		t.Parallel()

		var schedule ir.Schedule
		err := json.Unmarshal([]byte(`{"expression":"0 0 * * *","profile":"Prod"}`), &schedule)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid profile name")
	})

	t.Run("MarshalJSONIncludesProfile", func(t *testing.T) {
		t.Parallel()

		original, err := ir.NewCronSchedule("0 0 * * *")
		require.NoError(t, err)
		original.Profile = "prod"

		data, err := json.Marshal(original)
		require.NoError(t, err)
		require.Contains(t, string(data), `"profile":"prod"`)

		var unmarshaled ir.Schedule
		require.NoError(t, json.Unmarshal(data, &unmarshaled))
		require.Equal(t, "prod", unmarshaled.Profile)
	})

	t.Run("UnmarshalIgnoresWarningsMetadata", func(t *testing.T) {
		t.Parallel()

		original, err := ir.NewCronSchedule("*/40 * * * *")
		require.NoError(t, err)
		require.NotEmpty(t, original.Warnings)

		data, err := json.Marshal(original)
		require.NoError(t, err)
		require.Contains(t, string(data), `"warnings"`)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(data, &raw))
		raw["warnings"] = []string{"external warning metadata"}
		data, err = json.Marshal(raw)
		require.NoError(t, err)

		var unmarshaled ir.Schedule
		require.NoError(t, json.Unmarshal(data, &unmarshaled))
		require.Equal(t, original.Expression, unmarshaled.Expression)
		require.Equal(t, original.Warnings, unmarshaled.Warnings)
		require.NotContains(t, unmarshaled.Warnings, "external warning metadata")
	})

	t.Run("UnmarshalRejectsConflictingScheduleFields", func(t *testing.T) {
		t.Parallel()

		var schedule ir.Schedule
		err := json.Unmarshal([]byte(`{"kind":"cron","expression":"0 0 * * *","at":"2026-03-29T02:10:00+01:00"}`), &schedule)
		require.Error(t, err)
		require.Contains(t, err.Error(), "must not include both expression and at")
	})

	t.Run("UnmarshalRejectsCronAlias", func(t *testing.T) {
		t.Parallel()

		var schedule ir.Schedule
		err := json.Unmarshal([]byte(`{"cron":"0 0 * * *"}`), &schedule)
		require.Error(t, err)
		require.Contains(t, err.Error(), `unknown key "cron"`)
	})

	t.Run("MarshalUnmarshalOneOffJSON", func(t *testing.T) {
		t.Parallel()

		original, err := ir.NewOneOffSchedule("2026-03-29T02:10:00+01:00")
		require.NoError(t, err)

		data, err := json.Marshal(original)
		require.NoError(t, err)
		require.Contains(t, string(data), `"kind":"at"`)
		require.Contains(t, string(data), `"at":"2026-03-29T02:10:00+01:00"`)

		var unmarshaled ir.Schedule
		require.NoError(t, json.Unmarshal(data, &unmarshaled))
		require.Equal(t, ir.ScheduleKindAt, unmarshaled.GetKind())
		require.Equal(t, original.At, unmarshaled.At)
		assert.Equal(t, original.Fingerprint(), unmarshaled.Fingerprint())
	})
}

func TestNextRun(t *testing.T) {
	t.Parallel()

	cronSchedule, err := ir.NewCronSchedule("0 1 * * *")
	require.NoError(t, err)
	oneOffSchedule, err := ir.NewOneOffSchedule("2026-03-29T02:10:00+01:00")
	require.NoError(t, err)
	dag := &ir.DAG{
		Schedule: []ir.Schedule{cronSchedule, oneOffSchedule},
	}

	now := time.Date(2023, 10, 1, 1, 0, 0, 0, time.UTC)
	expected := time.Date(2023, 10, 2, 1, 0, 0, 0, time.UTC)

	require.Equal(t, expected, dag.NextRun(now))
}

func TestNextRun_OneOffPastIsIgnored(t *testing.T) {
	t.Parallel()

	schedule, err := ir.NewOneOffSchedule("2026-03-29T02:10:00+01:00")
	require.NoError(t, err)

	dag := &ir.DAG{Schedule: []ir.Schedule{schedule}}
	now := time.Date(2026, 3, 29, 2, 10, 0, 0, time.FixedZone("CET", 3600)).Add(time.Minute)

	assert.True(t, dag.NextRun(now).IsZero())
}

func TestEffectiveLogOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		dagLogOutput  ir.LogOutputMode
		stepLogOutput ir.LogOutputMode
		expected      ir.LogOutputMode
	}{
		{
			name:          "BothEmpty_ReturnsSeparate",
			dagLogOutput:  "",
			stepLogOutput: "",
			expected:      ir.LogOutputSeparate,
		},
		{
			name:          "DAGSeparate_StepEmpty_ReturnsSeparate",
			dagLogOutput:  ir.LogOutputSeparate,
			stepLogOutput: "",
			expected:      ir.LogOutputSeparate,
		},
		{
			name:          "DAGMerged_StepEmpty_ReturnsMerged",
			dagLogOutput:  ir.LogOutputMerged,
			stepLogOutput: "",
			expected:      ir.LogOutputMerged,
		},
		{
			name:          "DAGEmpty_StepSeparate_ReturnsSeparate",
			dagLogOutput:  "",
			stepLogOutput: ir.LogOutputSeparate,
			expected:      ir.LogOutputSeparate,
		},
		{
			name:          "DAGEmpty_StepMerged_ReturnsMerged",
			dagLogOutput:  "",
			stepLogOutput: ir.LogOutputMerged,
			expected:      ir.LogOutputMerged,
		},
		{
			name:          "DAGSeparate_StepMerged_StepOverrides",
			dagLogOutput:  ir.LogOutputSeparate,
			stepLogOutput: ir.LogOutputMerged,
			expected:      ir.LogOutputMerged,
		},
		{
			name:          "DAGMerged_StepSeparate_StepOverrides",
			dagLogOutput:  ir.LogOutputMerged,
			stepLogOutput: ir.LogOutputSeparate,
			expected:      ir.LogOutputSeparate,
		},
		{
			name:          "NilDAG_StepMerged_ReturnsMerged",
			dagLogOutput:  "", // Will use nil DAG
			stepLogOutput: ir.LogOutputMerged,
			expected:      ir.LogOutputMerged,
		},
		{
			name:          "NilStep_DAGMerged_ReturnsMerged",
			dagLogOutput:  ir.LogOutputMerged,
			stepLogOutput: "", // Will use nil Step
			expected:      ir.LogOutputMerged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var dag *ir.DAG
			var step *ir.Step

			// Setup DAG
			if tt.name != "NilDAG_StepMerged_ReturnsMerged" {
				dag = &ir.DAG{LogOutput: tt.dagLogOutput}
			}

			// Setup Step
			if tt.name != "NilStep_DAGMerged_ReturnsMerged" {
				step = &ir.Step{LogOutput: tt.stepLogOutput}
			}

			result := ir.EffectiveLogOutput(dag, step)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDAG_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		dag    *ir.DAG
		errMsg string
	}{
		{
			name: "valid DAG with name passes",
			dag: &ir.DAG{
				Name:  "test-dag",
				Steps: []ir.Step{{Name: "step1"}},
			},
		},
		{
			name:   "empty name fails",
			dag:    &ir.DAG{Name: ""},
			errMsg: "DAG name is required",
		},
		{
			name: "valid dependencies pass",
			dag: &ir.DAG{
				Name: "test-dag",
				Steps: []ir.Step{
					{Name: "step1"},
					{Name: "step2", Depends: []string{"step1"}},
				},
			},
		},
		{
			name: "missing dependency fails",
			dag: &ir.DAG{
				Name: "test-dag",
				Steps: []ir.Step{
					{Name: "step1"},
					{Name: "step2", Depends: []string{"nonexistent"}},
				},
			},
			errMsg: "non-existent step",
		},
		{
			name: "complex multi-level dependencies pass",
			dag: &ir.DAG{
				Name: "test-dag",
				Steps: []ir.Step{
					{Name: "step1"},
					{Name: "step2", Depends: []string{"step1"}},
					{Name: "step3", Depends: []string{"step1", "step2"}},
					{Name: "step4", Depends: []string{"step3"}},
				},
			},
		},
		{
			name: "steps with no dependencies pass",
			dag: &ir.DAG{
				Name: "test-dag",
				Steps: []ir.Step{
					{Name: "step1"},
					{Name: "step2"},
					{Name: "step3"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.dag.Validate()
			if tt.errMsg != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDAG_Validate_MultipleErrors(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "",
		Steps: []ir.Step{
			{Name: "a", Depends: []string{"missing1"}},
			{Name: "b", Depends: []string{"missing2"}},
			{Name: "c", Depends: []string{"missing3"}},
		},
	}

	err := dag.Validate()
	require.Error(t, err)

	var errList ir.ErrorList
	require.True(t, errors.As(err, &errList), "error should be an ErrorList")
	assert.Len(t, errList, 4, "should collect all 4 errors (1 name + 3 dependencies)")

	errStr := err.Error()
	for _, expected := range []string{"DAG name is required", "missing1", "missing2", "missing3"} {
		assert.Contains(t, errStr, expected)
	}
}

func TestDAG_HasLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		labels   []string
		search   string
		expected bool
	}{
		{
			name:     "empty labels, search for any returns false",
			labels:   []string{},
			search:   "test",
			expected: false,
		},
		{
			name:     "has label, search for it returns true",
			labels:   []string{"production", "critical"},
			search:   "production",
			expected: true,
		},
		{
			name:     "has label, search for different returns false",
			labels:   []string{"production", "critical"},
			search:   "staging",
			expected: false,
		},
		{
			name:     "multiple labels, search for last one returns true",
			labels:   []string{"a", "b", "c", "d"},
			search:   "d",
			expected: true,
		},
		{
			name:     "case insensitive - uppercase search matches lowercase label",
			labels:   []string{"production"},
			search:   "PRODUCTION",
			expected: true,
		},
		{
			name:     "case insensitive - lowercase search matches uppercase label",
			labels:   []string{"Production"},
			search:   "production",
			expected: true,
		},
		{
			name:     "nil labels returns false",
			labels:   nil,
			search:   "test",
			expected: false,
		},
		{
			name:     "key-value label with exact match",
			labels:   []string{"env=prod"},
			search:   "env=prod",
			expected: true,
		},
		{
			name:     "key-value label with key-only search",
			labels:   []string{"env=prod"},
			search:   "env",
			expected: true,
		},
		{
			name:     "key-value label with wrong value",
			labels:   []string{"env=prod"},
			search:   "env=staging",
			expected: false,
		},
		{
			name:     "negation filter - key not present",
			labels:   []string{"env=prod"},
			search:   "!deprecated",
			expected: true,
		},
		{
			name:     "negation filter - key present",
			labels:   []string{"env=prod", "deprecated"},
			search:   "!deprecated",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag := &ir.DAG{Labels: ir.NewLabels(tt.labels)}
			result := dag.HasLabel(tt.search)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDAG_ParamsMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   []string
		expected map[string]string
	}{
		{
			name:     "empty params returns empty map",
			params:   []string{},
			expected: map[string]string{},
		},
		{
			name:     "single param key=value",
			params:   []string{"key=value"},
			expected: map[string]string{"key": "value"},
		},
		{
			name:     "multiple params",
			params:   []string{"key1=value1", "key2=value2", "key3=value3"},
			expected: map[string]string{"key1": "value1", "key2": "value2", "key3": "value3"},
		},
		{
			name:     "param with multiple equals - first splits",
			params:   []string{"key=value=with=equals"},
			expected: map[string]string{"key": "value=with=equals"},
		},
		{
			name:     "param without equals - excluded",
			params:   []string{"noequals"},
			expected: map[string]string{},
		},
		{
			name:     "mixed valid and invalid params",
			params:   []string{"valid=value", "invalid", "another=one"},
			expected: map[string]string{"valid": "value", "another": "one"},
		},
		{
			name:     "empty value",
			params:   []string{"key="},
			expected: map[string]string{"key": ""},
		},
		{
			name:     "nil params returns empty map",
			params:   nil,
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag := &ir.DAG{Params: tt.params}
			result := dag.ParamsMap()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDAG_ProcGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		queue    string
		dagName  string
		expected string
	}{
		{
			name:     "queue set returns queue",
			queue:    "my-queue",
			dagName:  "my-dag",
			expected: "my-queue",
		},
		{
			name:     "queue empty returns dag name",
			queue:    "",
			dagName:  "my-dag",
			expected: "my-dag",
		},
		{
			name:     "both empty returns empty string",
			queue:    "",
			dagName:  "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag := &ir.DAG{Queue: tt.queue, Name: tt.dagName}
			result := dag.ProcGroup()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDAG_FileName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		location string
		expected string
	}{
		{
			name:     "location with .yaml extension",
			location: "/path/to/mydag.yaml",
			expected: "mydag",
		},
		{
			name:     "location with .yml extension",
			location: "/path/to/mydag.yml",
			expected: "mydag",
		},
		{
			name:     "location with no extension",
			location: "/path/to/mydag",
			expected: "mydag",
		},
		{
			name:     "empty location returns empty string",
			location: "",
			expected: "",
		},
		{
			name:     "just filename with yaml",
			location: "simple.yaml",
			expected: "simple",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag := &ir.DAG{Location: tt.location}
			result := dag.FileName()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDAG_String(t *testing.T) {
	t.Parallel()

	t.Run("full DAG formatted output", func(t *testing.T) {
		t.Parallel()

		dag := &ir.DAG{
			Name:        "test-dag",
			Description: "A test DAG",
			Params:      []string{"param1=value1", "param2=value2"},
			LogDir:      "/var/log/dags",
			Steps:       []ir.Step{{Name: "step1"}, {Name: "step2"}},
		}
		result := dag.String()

		for _, expected := range []string{"test-dag", "A test DAG", "param1=value1", "/var/log/dags"} {
			assert.Contains(t, result, expected)
		}
	})

	t.Run("minimal DAG basic output", func(t *testing.T) {
		t.Parallel()

		result := (&ir.DAG{Name: "minimal"}).String()

		for _, expected := range []string{"minimal", "{", "}"} {
			assert.Contains(t, result, expected)
		}
	})
}

func TestDAG_InitializeDefaults(t *testing.T) {
	t.Parallel()

	t.Run("empty DAG sets all defaults", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{}
		ir.InitializeDefaults(dag)

		assert.Equal(t, ir.TypeGraph, dag.Type)
		assert.Equal(t, 30, dag.HistRetentionDays)
		assert.Equal(t, 0, dag.HistRetentionRuns)
		assert.Equal(t, 5*time.Second, dag.MaxCleanUpTime)
		assert.Equal(t, 1, dag.MaxActiveRuns)
		assert.Equal(t, 1024*1024, dag.MaxOutputSize)
	})

	t.Run("pre-existing Type not overwritten", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{Type: ir.TypeGraph}
		ir.InitializeDefaults(dag)

		assert.Equal(t, ir.TypeGraph, dag.Type)
	})

	t.Run("pre-existing HistRetentionDays not overwritten", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{HistRetentionDays: 90}
		ir.InitializeDefaults(dag)

		assert.Equal(t, 90, dag.HistRetentionDays)
	})

	t.Run("pre-existing HistRetentionRuns prevents HistRetentionDays default", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{HistRetentionRuns: 3}
		ir.InitializeDefaults(dag)

		assert.Equal(t, 0, dag.HistRetentionDays)
		assert.Equal(t, 3, dag.HistRetentionRuns)
	})

	t.Run("pre-existing MaxActiveRuns not overwritten", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{MaxActiveRuns: 5}
		ir.InitializeDefaults(dag)

		assert.Equal(t, 5, dag.MaxActiveRuns)
	})

	t.Run("negative MaxActiveRuns preserved (deprecated)", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{MaxActiveRuns: -1}
		ir.InitializeDefaults(dag)

		// Negative values are deprecated but still preserved for backwards compatibility
		// A build warning will be emitted when the DAG is loaded
		assert.Equal(t, -1, dag.MaxActiveRuns)
	})
}

func TestDAG_NextRun_Extended(t *testing.T) {
	t.Parallel()

	t.Run("empty schedule returns zero time", func(t *testing.T) {
		t.Parallel()

		dag := &ir.DAG{Schedule: []ir.Schedule{}}
		assert.True(t, dag.NextRun(time.Now()).IsZero())
	})

	t.Run("single schedule returns correct next time", func(t *testing.T) {
		t.Parallel()

		parsed, err := cron.ParseStandard("0 * * * *")
		require.NoError(t, err)

		dag := &ir.DAG{
			Schedule: []ir.Schedule{{Expression: "0 * * * *", Parsed: parsed}},
		}

		now := time.Date(2023, 10, 1, 12, 30, 0, 0, time.UTC)
		expected := time.Date(2023, 10, 1, 13, 0, 0, 0, time.UTC)

		assert.Equal(t, expected, dag.NextRun(now))
	})

	t.Run("multiple schedules returns earliest", func(t *testing.T) {
		t.Parallel()

		hourly, err := cron.ParseStandard("0 * * * *")
		require.NoError(t, err)
		halfHourly, err := cron.ParseStandard("*/30 * * * *")
		require.NoError(t, err)

		dag := &ir.DAG{
			Schedule: []ir.Schedule{
				{Expression: "0 * * * *", Parsed: hourly},
				{Expression: "*/30 * * * *", Parsed: halfHourly},
			},
		}

		now := time.Date(2023, 10, 1, 12, 15, 0, 0, time.UTC)
		expected := time.Date(2023, 10, 1, 12, 30, 0, 0, time.UTC)

		assert.Equal(t, expected, dag.NextRun(now))
	})

	t.Run("nil Parsed in schedule is skipped", func(t *testing.T) {
		t.Parallel()

		parsed, err := cron.ParseStandard("0 * * * *")
		require.NoError(t, err)

		dag := &ir.DAG{
			Schedule: []ir.Schedule{
				{Expression: "invalid", Parsed: nil},
				{Expression: "0 * * * *", Parsed: parsed},
			},
		}

		now := time.Date(2023, 10, 1, 12, 30, 0, 0, time.UTC)
		expected := time.Date(2023, 10, 1, 13, 0, 0, 0, time.UTC)

		assert.Equal(t, expected, dag.NextRun(now))
	})
}

func TestDAG_GetName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dag      *ir.DAG
		expected string
	}{
		{
			name:     "name set returns name",
			dag:      &ir.DAG{Name: "my-dag", Location: "/path/to/other.yaml"},
			expected: "my-dag",
		},
		{
			name:     "name empty returns filename from location",
			dag:      &ir.DAG{Name: "", Location: "/path/to/mydag.yaml"},
			expected: "mydag",
		},
		{
			name:     "name empty and location empty returns empty",
			dag:      &ir.DAG{Name: "", Location: ""},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.dag.GetName())
		})
	}
}

func TestDAGHasApprovalSteps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		steps    []ir.Step
		expected bool
	}{
		{
			name:     "Empty",
			steps:    []ir.Step{},
			expected: false,
		},
		{
			name: "NoApproval",
			steps: []ir.Step{
				{Name: "step1", ExecutorConfig: ir.ExecutorConfig{Type: "command"}},
				{Name: "step2", ExecutorConfig: ir.ExecutorConfig{Type: "dag"}},
			},
			expected: false,
		},
		{
			name: "ApprovalField",
			steps: []ir.Step{
				{Name: "step1", ExecutorConfig: ir.ExecutorConfig{Type: "command"}},
				{Name: "step2", Approval: &ir.ApprovalConfig{Prompt: "review"}},
			},
			expected: true,
		},
		{
			name: "OnlyApproval",
			steps: []ir.Step{
				{Name: "step1", Approval: &ir.ApprovalConfig{}},
			},
			expected: true,
		},
		{
			name: "EmptyType",
			steps: []ir.Step{
				{Name: "step1", ExecutorConfig: ir.ExecutorConfig{Type: ""}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag := &ir.DAG{Steps: tt.steps}
			result := dag.HasApprovalSteps()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDAGHasHumanTaskSteps(t *testing.T) {
	t.Parallel()

	assert.False(t, (&ir.DAG{Steps: []ir.Step{{Name: "run"}}}).HasHumanTaskSteps())
	assert.True(t, (&ir.DAG{Steps: []ir.Step{{
		Name:      "review",
		HumanTask: &ir.HumanTaskConfig{Prompt: "Review"},
	}}}).HasHumanTaskSteps())
}
