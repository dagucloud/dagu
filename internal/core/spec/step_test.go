// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Register executor capabilities for testing.
	// In production, this is done by runtime/builtin init functions.

	// Command executors: support command, multiple commands, script, shell
	for _, t := range []string{"", "shell", "command"} {
		core.RegisterExecutorCapabilities(t, core.ExecutorCapabilities{
			Command: true, MultipleCommands: true, Script: true, Shell: true,
		})
	}
	// Docker: supports command, multiple commands, and container
	for _, t := range []string{"docker", "container"} {
		core.RegisterExecutorCapabilities(t, core.ExecutorCapabilities{
			Command: true, MultipleCommands: true, Container: true,
		})
	}
	// SSH: supports command, multiple commands, and shell
	core.RegisterExecutorCapabilities("ssh", core.ExecutorCapabilities{
		Command: true, MultipleCommands: true, Shell: true,
	})
	// jq and http: support command and script
	core.RegisterExecutorCapabilities("jq", core.ExecutorCapabilities{Command: true, Script: true})
	core.RegisterExecutorCapabilities("http", core.ExecutorCapabilities{Command: true, Script: true})
	// SQL executors: support query command and script execution
	for _, t := range []string{"postgres", "sqlite", "duckdb"} {
		core.RegisterExecutorCapabilities(t, core.ExecutorCapabilities{Command: true, Script: true})
	}
	// kubernetes: supports a single command only
	for _, t := range []string{"kubernetes", "k8s"} {
		core.RegisterExecutorCapabilities(t, core.ExecutorCapabilities{Command: true})
	}
	// archive: supports command only
	core.RegisterExecutorCapabilities("archive", core.ExecutorCapabilities{Command: true})
	// file: supports command only
	core.RegisterExecutorCapabilities("file", core.ExecutorCapabilities{Command: true})
	// data: supports operation commands only
	core.RegisterExecutorCapabilities("data", core.ExecutorCapabilities{Command: true})
	// wait: supports command only
	core.RegisterExecutorCapabilities("wait", core.ExecutorCapabilities{Command: true})
	// dag/subworkflow/parallel: support SubDAG and WorkerSelector
	for _, t := range []string{"dag", "subworkflow", "parallel"} {
		core.RegisterExecutorCapabilities(t, core.ExecutorCapabilities{
			SubDAG: true, WorkerSelector: true,
		})
	}
	// mail: no command support
	core.RegisterExecutorCapabilities("mail", core.ExecutorCapabilities{})
	// log: no command support
	core.RegisterExecutorCapabilities("log", core.ExecutorCapabilities{})
	// chat: LLM executor
	core.RegisterExecutorCapabilities("chat", core.ExecutorCapabilities{LLM: true})

	os.Exit(m.Run())
}

// testStepBuildContext creates a StepBuildContext for testing
func testStepBuildContext() StepBuildContext {
	return StepBuildContext{
		BuildContext: BuildContext{
			ctx:   context.Background(),
			file:  "/test/dag.yaml",
			opts:  BuildOpts{},
			index: 0,
		},
	}
}

// Helper to create ShellValue from string
func shellValue(s string) types.ShellValue {
	var v types.ShellValue
	_ = yaml.Unmarshal([]byte(`"`+s+`"`), &v)
	return v
}

// Helper to create ShellValue from array
func shellValueArray(args []string) types.ShellValue {
	var v types.ShellValue
	data, _ := yaml.Marshal(args)
	_ = yaml.Unmarshal(data, &v)
	return v
}

// Helper to create ContinueOnValue from string
func continueOnValue(s string) types.ContinueOnValue {
	var v types.ContinueOnValue
	_ = yaml.Unmarshal([]byte(`"`+s+`"`), &v)
	return v
}

// Helper to create ContinueOnValue from map
func continueOnValueMap(m map[string]any) types.ContinueOnValue {
	var v types.ContinueOnValue
	data, _ := yaml.Marshal(m)
	_ = yaml.Unmarshal(data, &v)
	return v
}

// Helper to create EnvValue from map
func envValueMap(m map[string]string) types.EnvValue {
	var v types.EnvValue
	data, _ := yaml.Marshal(m)
	_ = yaml.Unmarshal(data, &v)
	return v
}

func TestBuildStepName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimpleName", input: "my-step", expected: "my-step"},
		{name: "Trimmed", input: "  step  ", expected: "step"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Name: tt.input}
			result, err := buildStepName(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimpleID", input: "step-1", expected: "step-1"},
		{name: "Trimmed", input: "  id  ", expected: "id"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{ID: tt.input}
			result, err := buildStepID(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimpleDescription", input: "My step description", expected: "My step description"},
		{name: "Trimmed", input: "  description  ", expected: "description"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Description: tt.input}
			result, err := buildStepDescription(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepShellPackages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{name: "SinglePackage", input: []string{"python3"}, expected: []string{"python3"}},
		{name: "MultiplePackages", input: []string{"python3", "nodejs"}, expected: []string{"python3", "nodejs"}},
		{name: "Empty", input: nil, expected: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{ShellPackages: tt.input}
			result, err := buildStepShellPackages(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimpleScript", input: "echo hello", expected: "echo hello"},
		{name: "MultilineScript", input: "echo hello\necho world", expected: "echo hello\necho world"},
		{name: "Trimmed", input: "  script  ", expected: "script"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Script: tt.input}
			result, err := buildStepScript(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadYAMLLogStep(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - name: announce
    action: log.write
    with:
      message: "Deploying ${ENVIRONMENT}"
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	assert.Equal(t, "announce", dag.Steps[0].Name)
	assert.Equal(t, "log", dag.Steps[0].ExecutorConfig.Type)
	assert.Equal(t, "Deploying ${ENVIRONMENT}", dag.Steps[0].ExecutorConfig.Config["message"])
}

func TestBuildStepStdout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimplePath", input: "/tmp/output.log", expected: "/tmp/output.log"},
		{name: "Trimmed", input: "  /tmp/out.log  ", expected: "/tmp/out.log"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Stdout: tt.input}
			result, err := buildStepStdout(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepStderr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimplePath", input: "/tmp/error.log", expected: "/tmp/error.log"},
		{name: "Trimmed", input: "  /tmp/err.log  ", expected: "/tmp/err.log"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Stderr: tt.input}
			result, err := buildStepStderr(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepMailOnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    bool
		expected bool
	}{
		{name: "True", input: true, expected: true},
		{name: "False", input: false, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{MailOnError: tt.input}
			result, err := buildStepMailOnError(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepWorkerSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]string
		expected map[string]string
	}{
		{name: "SingleLabel", input: map[string]string{"env": "prod"}, expected: map[string]string{"env": "prod"}},
		{name: "MultipleLabels", input: map[string]string{"env": "prod", "region": "us-west"}, expected: map[string]string{"env": "prod", "region": "us-west"}},
		{name: "Empty", input: nil, expected: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{WorkerSelector: tt.input}
			result, err := buildStepWorkerSelector(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepWorkingDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workingDir string
		expected   string
	}{
		{name: "FromWorkingDir", workingDir: "/path/to/dir", expected: "/path/to/dir"},
		{name: "Trimmed", workingDir: "  /path  ", expected: "/path"},
		{name: "Empty", workingDir: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{WorkingDir: tt.workingDir}
			result, err := buildStepWorkingDir(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepShell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		shell    types.ShellValue
		expected string
	}{
		{name: "SimpleShell", shell: shellValue("bash"), expected: "bash"},
		{name: "ShellWithArgsAsString", shell: shellValue("bash -e"), expected: "bash"},
		{name: "ShellAsArray", shell: shellValueArray([]string{"bash", "-e", "-x"}), expected: "bash"},
		{name: "Empty", shell: types.ShellValue{}, expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Shell: tt.shell}
			result, err := buildStepShell(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepShellArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		shell    types.ShellValue
		expected []string
	}{
		{name: "NoArgs", shell: shellValue("bash"), expected: []string{}},
		{name: "ShellWithArgsAsString", shell: shellValue("bash -e"), expected: []string{"-e"}},
		{name: "ShellAsArray", shell: shellValueArray([]string{"bash", "-e", "-x"}), expected: []string{"-e", "-x"}},
		{name: "Empty", shell: types.ShellValue{}, expected: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Shell: tt.shell}
			result, err := buildStepShellArgs(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    int
		expected time.Duration
		wantErr  bool
	}{
		{name: "PositiveTimeout", input: 60, expected: 60 * time.Second},
		{name: "ZeroTimeout", input: 0, expected: 0},
		{name: "NegativeTimeout", input: -1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{TimeoutSec: tt.input}
			result, err := buildStepTimeout(testStepBuildContext(), s)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepDepends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		depends  types.StringOrArray
		expected []string
	}{
		{name: "SingleDependency", depends: stringOrArray("step1"), expected: []string{"step1"}},
		{name: "MultipleDependencies", depends: stringOrArrayList([]string{"step1", "step2"}), expected: []string{"step1", "step2"}},
		{name: "Empty", depends: types.StringOrArray{}, expected: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Depends: tt.depends}
			result, err := buildStepDepends(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepExplicitlyNoDeps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		depends  types.StringOrArray
		expected bool
	}{
		{name: "ExplicitEmptyArray", depends: stringOrArrayList([]string{}), expected: true},
		{name: "HasDependencies", depends: stringOrArrayList([]string{"step1"}), expected: false},
		{name: "ZeroValue", depends: types.StringOrArray{}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Depends: tt.depends}
			result, err := buildStepExplicitlyNoDeps(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepContinueOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		continueOn types.ContinueOnValue
		expected   core.ContinueOn
	}{
		{
			name:       "SkippedString",
			continueOn: continueOnValue("skipped"),
			expected:   core.ContinueOn{Skipped: true},
		},
		{
			name:       "FailedString",
			continueOn: continueOnValue("failed"),
			expected:   core.ContinueOn{Failure: true},
		},
		{
			name: "ObjectWithMultipleFields",
			continueOn: continueOnValueMap(map[string]any{
				"skipped":      true,
				"failed":       true,
				"mark_success": true,
			}),
			expected: core.ContinueOn{Skipped: true, Failure: true, MarkSuccess: true},
		},
		{
			name:       "Empty",
			continueOn: types.ContinueOnValue{},
			expected:   core.ContinueOn{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{ContinueOn: tt.continueOn}
			result, err := buildStepContinueOn(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepRetryPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		retryPolicy *retryPolicy
		expected    core.RetryPolicy
		wantErr     bool
	}{
		{
			name:        "NilPolicy",
			retryPolicy: nil,
			expected:    core.RetryPolicy{},
		},
		{
			name: "BasicPolicyWithIntValues",
			retryPolicy: &retryPolicy{
				Limit:       3,
				IntervalSec: 10,
			},
			expected: core.RetryPolicy{
				Limit:    3,
				Interval: 10 * time.Second,
			},
		},
		{
			name: "PolicyWithStringLimit",
			retryPolicy: &retryPolicy{
				Limit:       "${RETRY_LIMIT}",
				IntervalSec: 5,
			},
			expected: core.RetryPolicy{
				LimitStr: "${RETRY_LIMIT}",
				Interval: 5 * time.Second,
			},
		},
		{
			name: "PolicyWithExitCodes",
			retryPolicy: &retryPolicy{
				Limit:       2,
				IntervalSec: 5,
				ExitCode:    []int{1, 2, 3},
			},
			expected: core.RetryPolicy{
				Limit:     2,
				Interval:  5 * time.Second,
				ExitCodes: []int{1, 2, 3},
			},
		},
		{
			name: "PolicyWithBackoffTrue",
			retryPolicy: &retryPolicy{
				Limit:       3,
				IntervalSec: 5,
				Backoff:     true,
			},
			expected: core.RetryPolicy{
				Limit:    3,
				Interval: 5 * time.Second,
				Backoff:  2.0,
			},
		},
		{
			name: "PolicyWithInvalidBackoffMultiplier",
			retryPolicy: &retryPolicy{
				Limit:       3,
				IntervalSec: 5,
				Backoff:     0.5,
			},
			wantErr: true,
		},
		{
			name: "PolicyWithValidBackoffMultiplier",
			retryPolicy: &retryPolicy{
				Limit:       3,
				IntervalSec: 5,
				Backoff:     2.5,
			},
			expected: core.RetryPolicy{
				Limit:    3,
				Interval: 5 * time.Second,
				Backoff:  2.5,
			},
		},
		{
			name: "PolicyWithMaxInterval",
			retryPolicy: &retryPolicy{
				Limit:          3,
				IntervalSec:    5,
				Backoff:        2.0,
				MaxIntervalSec: 60,
			},
			expected: core.RetryPolicy{
				Limit:       3,
				Interval:    5 * time.Second,
				Backoff:     2.0,
				MaxInterval: 60 * time.Second,
			},
		},
		{
			name: "MissingLimit",
			retryPolicy: &retryPolicy{
				IntervalSec: 5,
			},
			wantErr: true,
		},
		{
			name: "MissingIntervalSec",
			retryPolicy: &retryPolicy{
				Limit: 3,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{RetryPolicy: tt.retryPolicy}
			result, err := buildStepRetryPolicy(testStepBuildContext(), s)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepRepeatPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		repeatPolicy *repeatPolicy
		expected     core.RepeatPolicy
		wantErr      bool
	}{
		{
			name:         "NilPolicy",
			repeatPolicy: nil,
			expected:     core.RepeatPolicy{},
		},
		{
			name: "WhileModeWithCondition",
			repeatPolicy: &repeatPolicy{
				Repeat:      types.RepeatModeFromString("while"),
				Condition:   "test -f /tmp/flag",
				IntervalSec: types.IntOrDynamicFromInt(5),
			},
			expected: core.RepeatPolicy{
				RepeatMode: core.RepeatModeWhile,
				Condition:  &core.Condition{Condition: "test -f /tmp/flag"},
				Interval:   5 * time.Second,
			},
		},
		{
			name: "UntilModeWithConditionAndExpected",
			repeatPolicy: &repeatPolicy{
				Repeat:      types.RepeatModeFromString("until"),
				Condition:   "cat /tmp/status",
				Expected:    "done",
				IntervalSec: types.IntOrDynamicFromInt(10),
			},
			expected: core.RepeatPolicy{
				RepeatMode: core.RepeatModeUntil,
				Condition:  &core.Condition{Condition: "cat /tmp/status", Expected: "done"},
				Interval:   10 * time.Second,
			},
		},
		{
			name: "LegacyBooleanTrue",
			repeatPolicy: &repeatPolicy{
				Repeat:    types.RepeatModeFromBool(true),
				Condition: "test condition",
			},
			expected: core.RepeatPolicy{
				RepeatMode: core.RepeatModeWhile,
				Condition:  &core.Condition{Condition: "test condition"},
			},
		},
		{
			name: "WithExitCodes",
			repeatPolicy: &repeatPolicy{
				Repeat:   types.RepeatModeFromString("while"),
				ExitCode: []int{0, 1},
			},
			expected: core.RepeatPolicy{
				RepeatMode: core.RepeatModeWhile,
				ExitCode:   []int{0, 1},
			},
		},
		{
			name: "WithLimit",
			repeatPolicy: &repeatPolicy{
				Repeat:    types.RepeatModeFromString("while"),
				Condition: "true",
				Limit:     types.IntOrDynamicFromInt(10),
			},
			expected: core.RepeatPolicy{
				RepeatMode: core.RepeatModeWhile,
				Condition:  &core.Condition{Condition: "true"},
				Limit:      10,
			},
		},
		{
			name: "WithBackoff",
			repeatPolicy: &repeatPolicy{
				Repeat:      types.RepeatModeFromString("while"),
				Condition:   "true",
				IntervalSec: types.IntOrDynamicFromInt(5),
				Backoff:     types.BackoffValueFromFloat(2.0),
			},
			expected: core.RepeatPolicy{
				RepeatMode: core.RepeatModeWhile,
				Condition:  &core.Condition{Condition: "true"},
				Interval:   5 * time.Second,
				Backoff:    2.0,
			},
		},
		{
			name: "WithMaxInterval",
			repeatPolicy: &repeatPolicy{
				Repeat:         types.RepeatModeFromString("while"),
				Condition:      "true",
				IntervalSec:    types.IntOrDynamicFromInt(5),
				Backoff:        types.BackoffValueFromFloat(2.0),
				MaxIntervalSec: types.IntOrDynamicFromInt(120),
			},
			expected: core.RepeatPolicy{
				RepeatMode:  core.RepeatModeWhile,
				Condition:   &core.Condition{Condition: "true"},
				Interval:    5 * time.Second,
				Backoff:     2.0,
				MaxInterval: 120 * time.Second,
			},
		},
		{
			name: "WhileWithoutConditionOrExitCode",
			repeatPolicy: &repeatPolicy{
				Repeat: types.RepeatModeFromString("while"),
			},
			wantErr: true,
		},
		{
			name: "LimitWithVariableReference",
			repeatPolicy: &repeatPolicy{
				Repeat:    types.RepeatModeFromString("while"),
				Condition: "true",
				Limit:     types.IntOrDynamicFromStr("${max_rounds}"),
			},
			expected: core.RepeatPolicy{
				RepeatMode: core.RepeatModeWhile,
				Condition:  &core.Condition{Condition: "true"},
				LimitStr:   "${max_rounds}",
			},
		},
		{
			name: "IntervalSecWithVariableReference",
			repeatPolicy: &repeatPolicy{
				Repeat:      types.RepeatModeFromString("while"),
				Condition:   "true",
				IntervalSec: types.IntOrDynamicFromStr("$INTERVAL"),
			},
			expected: core.RepeatPolicy{
				RepeatMode:  core.RepeatModeWhile,
				Condition:   &core.Condition{Condition: "true"},
				IntervalStr: "$INTERVAL",
			},
		},
		{
			name: "MaxIntervalSecWithVariableReference",
			repeatPolicy: &repeatPolicy{
				Repeat:         types.RepeatModeFromString("while"),
				Condition:      "true",
				IntervalSec:    types.IntOrDynamicFromInt(5),
				Backoff:        types.BackoffValueFromFloat(2.0),
				MaxIntervalSec: types.IntOrDynamicFromStr("${MAX_INTERVAL}"),
			},
			expected: core.RepeatPolicy{
				RepeatMode:     core.RepeatModeWhile,
				Condition:      &core.Condition{Condition: "true"},
				Interval:       5 * time.Second,
				Backoff:        2.0,
				MaxIntervalStr: "${MAX_INTERVAL}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{RepeatPolicy: tt.repeatPolicy}
			result, err := buildStepRepeatPolicy(testStepBuildContext(), s)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepSignalOnStop(t *testing.T) {
	t.Parallel()

	sigTerm := "SIGTERM"
	sigKill := "SIGKILL"
	sigInt := "SIGINT"
	invalid := "INVALID"

	tests := []struct {
		name         string
		signalOnStop *string
		expected     string
		wantErr      bool
	}{
		{name: "Nil", signalOnStop: nil, expected: ""},
		{name: "SIGTERM", signalOnStop: &sigTerm, expected: "SIGTERM"},
		{name: "SIGKILL", signalOnStop: &sigKill, expected: "SIGKILL"},
		{name: "SIGINT", signalOnStop: &sigInt, expected: "SIGINT"},
		{name: "InvalidSignal", signalOnStop: &invalid, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{SignalOnStop: tt.signalOnStop}
			result, err := buildStepSignalOnStop(testStepBuildContext(), s)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimpleVariable", input: "MY_VAR", expected: "MY_VAR"},
		{name: "WithDollarPrefix", input: "$MY_VAR", expected: "MY_VAR"},
		{name: "Trimmed", input: "  OUTPUT  ", expected: "OUTPUT"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Output: tt.input}
			result, err := buildStepOutput(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepEnvs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		env      types.EnvValue
		expected []string
	}{
		{
			name:     "SingleEnv",
			env:      envValueMap(map[string]string{"KEY": "value"}),
			expected: []string{"KEY=value"},
		},
		{
			name:     "Empty",
			env:      types.EnvValue{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Env: tt.env}
			result, err := buildStepEnvs(testStepBuildContext(), s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		command          any
		expectedScript   string
		expectedCommands []core.CommandEntry
		wantErr          bool
	}{
		{
			name:    "NilCommand",
			command: nil,
		},
		{
			name:    "SimpleStringCommand",
			command: "echo hello",
			expectedCommands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}, CmdWithArgs: "echo hello"},
			},
		},
		{
			name:           "MultilineCommandBecomesScript",
			command:        "echo hello\necho world",
			expectedScript: "echo hello\necho world",
		},
		{
			name:    "EmptyStringCommand",
			command: "   ",
			wantErr: true,
		},
		{
			name:    "InvalidType",
			command: 123,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Command: tt.command}
			result := &core.Step{ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)}}
			err := buildStepCommand(testStepBuildContext(), s, result)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedScript, result.Script)
			assert.Equal(t, tt.expectedCommands, result.Commands)
			// Legacy fields should NOT be populated by build functions
			assert.Empty(t, result.Command)
			assert.Nil(t, result.Args)
			assert.Empty(t, result.CmdWithArgs)
		})
	}
}

func TestBuildStepCommand_MultilineHarnessPromptStaysCommand(t *testing.T) {
	t.Parallel()

	s := &step{Command: "hey\nyou"}
	result := &core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "passthrough"},
		},
	}

	err := buildStepCommand(testStepBuildContext(), s, result)
	require.NoError(t, err)
	assert.Empty(t, result.Script)
	assert.Equal(t, []core.CommandEntry{
		{CmdWithArgs: "hey\nyou"},
	}, result.Commands)
}

func TestBuildStepCommand_MultipleCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		command          any
		expectedCommands []core.CommandEntry
		wantErr          bool
	}{
		{
			name:    "SingleCommandInArray",
			command: []any{"echo hello"},
			expectedCommands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}, CmdWithArgs: "echo hello"},
			},
		},
		{
			name:    "TwoSimpleCommands",
			command: []any{"echo hello", "echo world"},
			expectedCommands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}, CmdWithArgs: "echo hello"},
				{Command: "echo", Args: []string{"world"}, CmdWithArgs: "echo world"},
			},
		},
		{
			name:    "MultipleCommandsWithArgs",
			command: []any{"npm install", "npm run build", "npm test"},
			expectedCommands: []core.CommandEntry{
				{Command: "npm", Args: []string{"install"}, CmdWithArgs: "npm install"},
				{Command: "npm", Args: []string{"run", "build"}, CmdWithArgs: "npm run build"},
				{Command: "npm", Args: []string{"test"}, CmdWithArgs: "npm test"},
			},
		},
		{
			name:    "CommandsWithQuotedArgs",
			command: []any{`echo "hello world"`, `grep "search term"`},
			expectedCommands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello world"}, CmdWithArgs: `echo "hello world"`},
				{Command: "grep", Args: []string{"search term"}, CmdWithArgs: `grep "search term"`},
			},
		},
		{
			name:    "CommandsWithPipes",
			command: []any{"ls -la", "cat file.txt | grep pattern"},
			expectedCommands: []core.CommandEntry{
				{Command: "ls", Args: []string{"-la"}, CmdWithArgs: "ls -la"},
				{Command: "cat", Args: []string{"file.txt", "|", "grep", "pattern"}, CmdWithArgs: "cat file.txt | grep pattern"},
			},
		},
		{
			name:    "SimpleCommandsNoArgs",
			command: []any{"pwd", "whoami", "date"},
			expectedCommands: []core.CommandEntry{
				{Command: "pwd", Args: []string{}, CmdWithArgs: "pwd"},
				{Command: "whoami", Args: []string{}, CmdWithArgs: "whoami"},
				{Command: "date", Args: []string{}, CmdWithArgs: "date"},
			},
		},
		{
			name:    "EmptyArrayCommand",
			command: []any{},
			wantErr: true,
		},
		{
			name:    "ArrayWithOnlyEmptyStrings",
			command: []any{"", "   ", ""},
			wantErr: true,
		},
		{
			name:    "ArrayWithMixedEmptyAndValid",
			command: []any{"", "echo hello", "   "},
			expectedCommands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}, CmdWithArgs: "echo hello"},
			},
		},
		{
			name:    "NonStringElementsConverted",
			command: []any{123, true, 45.6},
			expectedCommands: []core.CommandEntry{
				{Command: "123", Args: []string{}, CmdWithArgs: "123"},
				{Command: "true", Args: []string{}, CmdWithArgs: "true"},
				{Command: "45.6", Args: []string{}, CmdWithArgs: "45.6"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Command: tt.command}
			result := &core.Step{ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)}}
			err := buildStepCommand(testStepBuildContext(), s, result)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Verify Commands slice
			require.Equal(t, len(tt.expectedCommands), len(result.Commands), "Commands count mismatch")
			for i, expected := range tt.expectedCommands {
				assert.Equal(t, expected.Command, result.Commands[i].Command, "Command[%d].Command mismatch", i)
				assert.Equal(t, expected.Args, result.Commands[i].Args, "Command[%d].Args mismatch", i)
				assert.Equal(t, expected.CmdWithArgs, result.Commands[i].CmdWithArgs, "Command[%d].CmdWithArgs mismatch", i)
			}

			// Legacy fields should NOT be populated by build functions
			assert.Empty(t, result.Command)
			assert.Nil(t, result.Args)
			assert.Empty(t, result.CmdWithArgs)
		})
	}
}

func TestBuildSingleCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		command         string
		expectedCommand string
		expectedArgs    []string
		expectedScript  string
		wantErr         bool
	}{
		{
			name:            "SimpleCommand",
			command:         "echo hello",
			expectedCommand: "echo",
			expectedArgs:    []string{"hello"},
		},
		{
			name:            "CommandWithMultipleArgs",
			command:         "python script.py --arg1 value1 --arg2 value2",
			expectedCommand: "python",
			expectedArgs:    []string{"script.py", "--arg1", "value1", "--arg2", "value2"},
		},
		{
			name:            "CommandWithQuotes",
			command:         `echo "hello world"`,
			expectedCommand: "echo",
			expectedArgs:    []string{"hello world"},
		},
		{
			name:           "MultilineBecomesScript",
			command:        "echo line1\necho line2",
			expectedScript: "echo line1\necho line2",
		},
		{
			name:            "CommandOnly",
			command:         "pwd",
			expectedCommand: "pwd",
			expectedArgs:    []string{},
		},
		{
			name:    "EmptyCommand",
			command: "",
			wantErr: true,
		},
		{
			name:    "WhitespaceOnly",
			command: "   \t  ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &core.Step{}
			err := buildSingleCommand(tt.command, result)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedScript, result.Script)

			// Legacy fields should NOT be populated
			assert.Empty(t, result.Command)
			assert.Nil(t, result.Args)

			// For non-script commands, Commands should be populated
			if tt.expectedScript == "" {
				require.Len(t, result.Commands, 1)
				assert.Equal(t, tt.expectedCommand, result.Commands[0].Command)
				assert.Equal(t, tt.expectedArgs, result.Commands[0].Args)
				assert.Equal(t, tt.command, result.Commands[0].CmdWithArgs)
			}
		})
	}
}

func TestBuildMultipleCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		commands         []any
		expectedCommands []core.CommandEntry
		wantErr          bool
	}{
		{
			name:     "BasicCommands",
			commands: []any{"echo foo", "echo bar"},
			expectedCommands: []core.CommandEntry{
				{Command: "echo", Args: []string{"foo"}, CmdWithArgs: "echo foo"},
				{Command: "echo", Args: []string{"bar"}, CmdWithArgs: "echo bar"},
			},
		},
		{
			name:     "EmptyArray",
			commands: []any{},
			wantErr:  true,
		},
		{
			name:     "AllEmpty",
			commands: []any{"", "", ""},
			wantErr:  true,
		},
		{
			name:     "SkipsEmptyPreservesValid",
			commands: []any{"", "valid command", ""},
			expectedCommands: []core.CommandEntry{
				{Command: "valid", Args: []string{"command"}, CmdWithArgs: "valid command"},
			},
		},
		{
			name:     "AcceptsSingleKeyMap",
			commands: []any{"echo hello", map[string]any{"key": "value"}},
			expectedCommands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}, CmdWithArgs: "echo hello"},
				{Command: "key:", Args: []string{"value"}, CmdWithArgs: "key: value"},
			},
		},
		{
			name:     "RejectsMultiKeyMap",
			commands: []any{"echo hello", map[string]any{"key1": "val1", "key2": "val2"}},
			wantErr:  true,
		},
		{
			name:     "RejectsNestedArray",
			commands: []any{"echo hello", []string{"nested"}},
			wantErr:  true,
		},
		{
			name:     "AcceptsPrimitiveTypes",
			commands: []any{"echo", 123, true, 45.6},
			expectedCommands: []core.CommandEntry{
				{Command: "echo", Args: []string{}, CmdWithArgs: "echo"},
				{Command: "123", Args: []string{}, CmdWithArgs: "123"},
				{Command: "true", Args: []string{}, CmdWithArgs: "true"},
				{Command: "45.6", Args: []string{}, CmdWithArgs: "45.6"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &core.Step{}
			err := buildMultipleCommands(tt.commands, result)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, len(tt.expectedCommands), len(result.Commands))
			for i, expected := range tt.expectedCommands {
				assert.Equal(t, expected.Command, result.Commands[i].Command)
				assert.Equal(t, expected.Args, result.Commands[i].Args)
				assert.Equal(t, expected.CmdWithArgs, result.Commands[i].CmdWithArgs)
			}
		})
	}
}

func TestStepHasMultipleCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		step     *core.Step
		expected bool
	}{
		{
			name:     "NoCommands",
			step:     &core.Step{},
			expected: false,
		},
		{
			name: "SingleCommandInCommands",
			step: &core.Step{
				Commands: []core.CommandEntry{
					{Command: "echo", Args: []string{"hello"}},
				},
			},
			expected: false, // Single command = not multiple
		},
		{
			name: "HasMultipleCommands",
			step: &core.Step{
				Commands: []core.CommandEntry{
					{Command: "echo", Args: []string{"hello"}},
					{Command: "echo", Args: []string{"world"}},
				},
			},
			expected: true,
		},
		{
			name: "EmptyCommandsSlice",
			step: &core.Step{
				Commands: []core.CommandEntry{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.step.HasMultipleCommands())
		})
	}
}

func TestBuildStepExecutor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		step     *step
		ctx      StepBuildContext
		expected core.ExecutorConfig
		wantErr  bool
	}{
		{
			name:     "NoType",
			step:     &step{},
			ctx:      testStepBuildContext(),
			expected: core.ExecutorConfig{Config: make(map[string]any)},
		},
		{
			name:     "TypeField",
			step:     &step{Type: "http"},
			ctx:      testStepBuildContext(),
			expected: core.ExecutorConfig{Type: "http", Config: make(map[string]any)},
		},
		{
			name: "SFTPTypeAndWith",
			step: &step{
				Type: "sftp",
				With: map[string]any{
					"source":      "./backup.tar.gz",
					"destination": "/srv/backups/backup.tar.gz",
				},
			},
			ctx: testStepBuildContext(),
			expected: core.ExecutorConfig{
				Type: "sftp",
				Config: map[string]any{
					"source":      "./backup.tar.gz",
					"destination": "/srv/backups/backup.tar.gz",
				},
			},
		},
		{
			name: "TypeAndWith",
			step: &step{
				Type: "docker",
				With: map[string]any{
					"image": "alpine:latest",
				},
			},
			ctx: testStepBuildContext(),
			expected: core.ExecutorConfig{
				Type:   "docker",
				Config: map[string]any{"image": "alpine:latest"},
			},
		},
		{
			name: "TypeAndLegacyConfig",
			step: &step{
				Type: "docker",
				Config: map[string]any{
					"image": "alpine:latest",
				},
			},
			ctx: testStepBuildContext(),
			expected: core.ExecutorConfig{
				Type:   "docker",
				Config: map[string]any{"image": "alpine:latest"},
			},
		},
		{
			name: "RejectWithAndLegacyConfig",
			step: &step{
				Type:   "docker",
				With:   map[string]any{"image": "alpine:latest"},
				Config: map[string]any{"image": "busybox:latest"},
			},
			ctx:     testStepBuildContext(),
			wantErr: true,
		},
		{
			name: "InheritsContainerExecutor",
			step: &step{},
			ctx: StepBuildContext{
				BuildContext: testBuildContext(),
				dag:          &core.DAG{Container: &core.Container{Image: "alpine"}},
			},
			expected: core.ExecutorConfig{Type: "container", Config: make(map[string]any)},
		},
		{
			name: "InheritsSSHExecutor",
			step: &step{},
			ctx: StepBuildContext{
				BuildContext: testBuildContext(),
				dag:          &core.DAG{SSH: &core.SSHConfig{Host: "example.com"}},
			},
			expected: core.ExecutorConfig{Type: "ssh", Config: make(map[string]any)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &core.Step{ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)}}
			err := buildStepExecutor(tt.ctx, tt.step, result)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected.Type, result.ExecutorConfig.Type)
			assert.Equal(t, tt.expected.Config, result.ExecutorConfig.Config)
		})
	}
}

func TestBuildStepParamsField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		params     any
		wantEmpty  bool
		wantParams map[string]string
	}{
		{
			name:      "NilParams",
			params:    nil,
			wantEmpty: true,
		},
		{
			name: "ParamsAsMap",
			params: map[string]any{
				"repository": "myorg/myrepo",
				"ref":        "main",
				"token":      "secret123",
			},
			wantParams: map[string]string{
				"repository": "myorg/myrepo",
				"ref":        "main",
				"token":      "secret123",
			},
		},
		{
			name:   "ParamsAsString",
			params: "go-version=1.21 cache=true",
			wantParams: map[string]string{
				"go-version": "1.21",
				"cache":      "true",
			},
		},
		{
			name: "ParamsWithNumbers",
			params: map[string]any{
				"timeout": 300,
				"retries": 3,
				"enabled": true,
			},
			wantParams: map[string]string{
				"timeout": "300",
				"retries": "3",
				"enabled": "true",
			},
		},
		{
			name:       "EmptyMap",
			params:     map[string]any{},
			wantParams: map[string]string{},
		},
		{
			name:   "ParamsWithQuotedValues",
			params: `message="hello world" count="42"`,
			wantParams: map[string]string{
				"message": "hello world",
				"count":   "42",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &step{Params: tt.params}
			result := &core.Step{}
			err := buildStepParamsField(testStepBuildContext(), s, result)
			require.NoError(t, err)

			if tt.wantEmpty {
				assert.True(t, result.Params.IsEmpty())
				return
			}

			params, err := result.Params.AsStringMap()
			require.NoError(t, err)
			if len(tt.wantParams) == 0 {
				assert.Empty(t, params)
			} else {
				for k, v := range tt.wantParams {
					assert.Equal(t, v, params[k])
				}
			}
		})
	}
}

func TestBuildStepParallel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		parallel any
		expected *core.ParallelConfig
		wantErr  bool
	}{
		{
			name:     "NilParallel",
			parallel: nil,
			expected: nil,
		},
		{
			name:     "StringVariableReference",
			parallel: "${ITEMS}",
			expected: &core.ParallelConfig{Variable: "${ITEMS}", MaxConcurrent: core.DefaultMaxConcurrent},
		},
		{
			name:     "StaticArrayOfStrings",
			parallel: []any{"item1", "item2", "item3"},
			expected: &core.ParallelConfig{
				Items: []core.ParallelItem{
					{Value: "item1"},
					{Value: "item2"},
					{Value: "item3"},
				},
				MaxConcurrent: core.DefaultMaxConcurrent,
			},
		},
		{
			name:     "ArrayOfNumbers",
			parallel: []any{1, 2, 3},
			expected: &core.ParallelConfig{
				Items: []core.ParallelItem{
					{Value: "1"},
					{Value: "2"},
					{Value: "3"},
				},
				MaxConcurrent: core.DefaultMaxConcurrent,
			},
		},
		{
			name: "ArrayOfObjects",
			parallel: []any{
				map[string]any{"name": "first", "value": 100},
				map[string]any{"name": "second", "value": 200},
			},
			expected: &core.ParallelConfig{
				Items: []core.ParallelItem{
					{Params: map[string]string{"name": "first", "value": "100"}},
					{Params: map[string]string{"name": "second", "value": "200"}},
				},
				MaxConcurrent: core.DefaultMaxConcurrent,
			},
		},
		{
			name: "ObjectConfigWithItemsArray",
			parallel: map[string]any{
				"items":          []any{"a", "b"},
				"max_concurrent": 5,
			},
			expected: &core.ParallelConfig{
				Items: []core.ParallelItem{
					{Value: "a"},
					{Value: "b"},
				},
				MaxConcurrent: 5,
			},
		},
		{
			name: "ObjectConfigWithVariableReference",
			parallel: map[string]any{
				"items":          "${MY_ITEMS}",
				"max_concurrent": 3,
			},
			expected: &core.ParallelConfig{
				Variable:      "${MY_ITEMS}",
				MaxConcurrent: 3,
			},
		},
		{
			name: "MaxConcurrentAsInt64",
			parallel: map[string]any{
				"items":          "${ITEMS}",
				"max_concurrent": int64(10),
			},
			expected: &core.ParallelConfig{
				Variable:      "${ITEMS}",
				MaxConcurrent: 10,
			},
		},
		{
			name: "MaxConcurrentAsFloat64",
			parallel: map[string]any{
				"items":          "${ITEMS}",
				"max_concurrent": float64(7),
			},
			expected: &core.ParallelConfig{
				Variable:      "${ITEMS}",
				MaxConcurrent: 7,
			},
		},
		{
			name:     "InvalidType",
			parallel: 123,
			wantErr:  true,
		},
		{
			name: "InvalidItemsType",
			parallel: map[string]any{
				"items": 123,
			},
			wantErr: true,
		},
		{
			name: "InvalidMaxConcurrentType",
			parallel: map[string]any{
				"items":          "${ITEMS}",
				"max_concurrent": "invalid",
			},
			wantErr: true,
		},
		{
			name:     "InvalidItemValue_NestedArray",
			parallel: []any{[]any{"nested", "array"}},
			wantErr:  true,
		},
		{
			name: "ObjectItemWithBool",
			parallel: []any{
				map[string]any{"name": "test", "enabled": true},
			},
			expected: &core.ParallelConfig{
				Items: []core.ParallelItem{
					{Params: map[string]string{"name": "test", "enabled": "true"}},
				},
				MaxConcurrent: core.DefaultMaxConcurrent,
			},
		},
		{
			name: "ObjectItemWithFloat",
			parallel: []any{
				map[string]any{"name": "test", "rate": 3.14},
			},
			expected: &core.ParallelConfig{
				Items: []core.ParallelItem{
					{Params: map[string]string{"name": "test", "rate": "3.14"}},
				},
				MaxConcurrent: core.DefaultMaxConcurrent,
			},
		},
		{
			name: "InvalidObjectParamType_NestedMap",
			parallel: []any{
				map[string]any{"name": "test", "nested": map[string]any{"key": "value"}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Parallel: tt.parallel}
			result := &core.Step{ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)}}
			err := buildStepParallel(testStepBuildContext(), s, result)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Parallel)
		})
	}
}

func TestBuildStepSubDAG(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		step     *step
		expected *core.SubDAG
	}{
		{
			name:     "NoSubDAG",
			step:     &step{},
			expected: nil,
		},
		{
			name:     "SimpleCall",
			step:     &step{Call: "other-dag"},
			expected: &core.SubDAG{Name: "other-dag", Params: ""},
		},
		{
			name: "CallWithParams",
			step: &step{
				Call:   "other-dag",
				Params: map[string]any{"key": "value"},
			},
			expected: &core.SubDAG{Name: "other-dag", Params: `key="value"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &core.Step{ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)}}
			err := buildStepSubDAG(testStepBuildContext(), tt.step, result)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.SubDAG)
		})
	}
}

func TestParseParallelItems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		items    []any
		expected []core.ParallelItem
		wantErr  bool
	}{
		{
			name:  "StringItems",
			items: []any{"a", "b", "c"},
			expected: []core.ParallelItem{
				{Value: "a"},
				{Value: "b"},
				{Value: "c"},
			},
		},
		{
			name:  "NumericItems",
			items: []any{1, 2.5, int64(3)},
			expected: []core.ParallelItem{
				{Value: "1"},
				{Value: "2.5"},
				{Value: "3"},
			},
		},
		{
			name: "ObjectItems",
			items: []any{
				map[string]any{"name": "item1", "count": 5},
				map[string]any{"name": "item2", "count": 10},
			},
			expected: []core.ParallelItem{
				{Params: map[string]string{"name": "item1", "count": "5"}},
				{Params: map[string]string{"name": "item2", "count": "10"}},
			},
		},
		{
			name:    "InvalidItemType",
			items:   []any{[]string{"nested", "array"}},
			wantErr: true,
		},
		{
			name: "InvalidParamValueType",
			items: []any{
				map[string]any{"nested": []string{"array"}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseParallelItems(tt.items)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildStepContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *container
		expected *core.Container
		wantErr  bool
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name:    "MissingImage",
			input:   &container{},
			wantErr: true,
		},
		{
			name: "BasicContainer",
			input: &container{
				Image: "alpine:latest",
			},
			expected: &core.Container{
				Image:      "alpine:latest",
				PullPolicy: core.PullPolicyMissing,
			},
		},
		{
			name: "FullContainerConfig",
			input: &container{
				Name:          "my-step-container",
				Image:         "golang:1.22",
				PullPolicy:    "always",
				Volumes:       []string{"./src:/app"},
				User:          "1000",
				WorkingDir:    "/app",
				Platform:      "linux/amd64",
				Ports:         []string{"8080:8080"},
				Network:       "host",
				KeepContainer: false,
				Startup:       "entrypoint",
				Command:       []string{"go", "build"},
				WaitFor:       "running",
				LogPattern:    "ready",
				RestartPolicy: "no",
			},
			expected: &core.Container{
				Name:          "my-step-container",
				Image:         "golang:1.22",
				PullPolicy:    core.PullPolicyAlways,
				Volumes:       []string{"./src:/app"},
				User:          "1000",
				WorkingDir:    "/app",
				Platform:      "linux/amd64",
				Ports:         []string{"8080:8080"},
				Network:       "host",
				KeepContainer: false,
				Startup:       core.StartupEntrypoint,
				Command:       []string{"go", "build"},
				WaitFor:       core.WaitForRunning,
				LogPattern:    "ready",
				RestartPolicy: "no",
			},
		},
		{
			name: "ContainerWithEnvAsMap",
			input: &container{
				Image: "node:20",
				Env:   map[string]any{"NODE_ENV": "production"},
			},
			expected: &core.Container{
				Image:      "node:20",
				PullPolicy: core.PullPolicyMissing,
				Env:        []string{"NODE_ENV=production"},
			},
		},
		{
			name: "ContainerWithVolumes",
			input: &container{
				Image:   "postgres:16",
				Volumes: []string{"./data:/var/lib/postgresql/data", "/tmp:/tmp:ro"},
			},
			expected: &core.Container{
				Image:      "postgres:16",
				PullPolicy: core.PullPolicyMissing,
				Volumes:    []string{"./data:/var/lib/postgresql/data", "/tmp:/tmp:ro"},
			},
		},
		{
			name: "PullPolicyNever",
			input: &container{
				Image:      "myimage:local",
				PullPolicy: "never",
			},
			expected: &core.Container{
				Image:      "myimage:local",
				PullPolicy: core.PullPolicyNever,
			},
		},
		{
			name: "PullPolicyMissing",
			input: &container{
				Image:      "alpine:3.18",
				PullPolicy: "missing",
			},
			expected: &core.Container{
				Image:      "alpine:3.18",
				PullPolicy: core.PullPolicyMissing,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &step{Container: tt.input}
			result := &core.Step{}
			err := buildStepContainer(testStepBuildContext(), s, result)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Container)
		})
	}

}

func TestValidateMultipleCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executorType string
		commands     []core.CommandEntry
		wantErr      bool
	}{
		// Single command - should always pass
		{
			name:         "SingleCommand_NoExecutorType",
			executorType: "",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      false,
		},
		{
			name:         "SingleCommand_JQExecutor",
			executorType: "jq",
			commands:     []core.CommandEntry{{Command: ".foo"}},
			wantErr:      false,
		},
		// Multiple commands - should pass for multi-command executors
		{
			name:         "MultipleCommands_NoExecutorType",
			executorType: "",
			commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}},
				{Command: "echo", Args: []string{"world"}},
			},
			wantErr: false,
		},
		{
			name:         "MultipleCommands_ShellExecutor",
			executorType: "shell",
			commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}},
				{Command: "echo", Args: []string{"world"}},
			},
			wantErr: false,
		},
		{
			name:         "MultipleCommands_CommandExecutor",
			executorType: "command",
			commands: []core.CommandEntry{
				{Command: "npm", Args: []string{"install"}},
				{Command: "npm", Args: []string{"run", "build"}},
			},
			wantErr: false,
		},
		{
			name:         "MultipleCommands_DockerExecutor",
			executorType: "docker",
			commands: []core.CommandEntry{
				{Command: "apt-get", Args: []string{"update"}},
				{Command: "apt-get", Args: []string{"install", "curl"}},
			},
			wantErr: false,
		},
		{
			name:         "MultipleCommands_ContainerExecutor",
			executorType: "container",
			commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}},
				{Command: "echo", Args: []string{"world"}},
			},
			wantErr: false,
		},
		{
			name:         "MultipleCommands_SSHExecutor",
			executorType: "ssh",
			commands: []core.CommandEntry{
				{Command: "ls", Args: []string{"-la"}},
				{Command: "pwd"},
			},
			wantErr: false,
		},
		// Multiple commands - should fail for single-command executors
		{
			name:         "MultipleCommands_JQExecutor",
			executorType: "jq",
			commands: []core.CommandEntry{
				{Command: ".foo"},
				{Command: ".bar"},
			},
			wantErr: true,
		},
		{
			name:         "MultipleCommands_HTTPExecutor",
			executorType: "http",
			commands: []core.CommandEntry{
				{Command: "GET", Args: []string{"https://example.com"}},
				{Command: "POST", Args: []string{"https://example.com"}},
			},
			wantErr: true,
		},
		{
			name:         "MultipleCommands_KubernetesExecutor",
			executorType: "kubernetes",
			commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}},
				{Command: "echo", Args: []string{"world"}},
			},
			wantErr: true,
		},
		{
			name:         "MultipleCommands_K8sExecutor",
			executorType: "k8s",
			commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}},
				{Command: "echo", Args: []string{"world"}},
			},
			wantErr: true,
		},
		{
			name:         "MultipleCommands_ArchiveExecutor",
			executorType: "archive",
			commands: []core.CommandEntry{
				{Command: "extract"},
				{Command: "list"},
			},
			wantErr: true,
		},
		{
			name:         "MultipleCommands_MailExecutor",
			executorType: "mail",
			commands: []core.CommandEntry{
				{Command: "send"},
				{Command: "another"},
			},
			wantErr: true,
		},
		{
			name:         "MultipleCommands_DAGExecutor",
			executorType: "dag",
			commands: []core.CommandEntry{
				{Command: "dag1"},
				{Command: "dag2"},
			},
			wantErr: true,
		},
		{
			name:         "MultipleCommands_ParallelExecutor",
			executorType: "parallel",
			commands: []core.CommandEntry{
				{Command: "task1"},
				{Command: "task2"},
			},
			wantErr: true,
		},
		// Empty commands - should always pass
		{
			name:         "NoCommands_JQExecutor",
			executorType: "jq",
			commands:     nil,
			wantErr:      false,
		},
		{
			name:         "EmptyCommands_HTTPExecutor",
			executorType: "http",
			commands:     []core.CommandEntry{},
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{
				Commands: tt.commands,
				ExecutorConfig: core.ExecutorConfig{
					Type: tt.executorType,
				},
			}
			err := validateMultipleCommands(result)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), `action "`+tt.executorType+`" supports only one command`)
				assert.NotContains(t, err.Error(), "action does not support multiple commands")
				assert.NotContains(t, err.Error(), "executor")
				assert.True(t, errors.Is(err, ErrExecutorDoesNotSupportMultipleCmd))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executorType string
		script       string
		wantErr      bool
	}{
		// Executors that support script
		{
			name:         "ScriptWithCommandExecutor",
			executorType: "command",
			script:       "echo hello",
			wantErr:      false,
		},
		{
			name:         "ScriptWithShellExecutor",
			executorType: "shell",
			script:       "echo hello",
			wantErr:      false,
		},
		{
			name:         "ScriptWithDockerExecutor",
			executorType: "docker",
			script:       "echo hello",
			wantErr:      true, // Docker doesn't use step.Script field
		},
		{
			name:         "ScriptWithJQExecutor",
			executorType: "jq",
			script:       `{"key": "value"}`,
			wantErr:      false,
		},
		{
			name:         "ScriptWithHTTPExecutor",
			executorType: "http",
			script:       `{"body": "data"}`,
			wantErr:      false,
		},
		// Executors that do not support script
		{
			name:         "ScriptWithSSHExecutor",
			executorType: "ssh",
			script:       "echo hello",
			wantErr:      true,
		},
		{
			name:         "ScriptWithMailExecutor",
			executorType: "mail",
			script:       "echo hello",
			wantErr:      true,
		},
		{
			name:         "ScriptWithArchiveExecutor",
			executorType: "archive",
			script:       "echo hello",
			wantErr:      true,
		},
		// Empty script - should always pass
		{
			name:         "EmptyScriptWithSSHExecutor",
			executorType: "ssh",
			script:       "",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{
				Script: tt.script,
				ExecutorConfig: core.ExecutorConfig{
					Type: tt.executorType,
				},
			}
			err := validateScript(result)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "does not support script field")
				assert.Contains(t, err.Error(), tt.executorType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateShell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executorType string
		shell        string
		wantErr      bool
	}{
		// Executors that support shell
		{
			name:         "ShellWithCommandExecutor",
			executorType: "command",
			shell:        "/bin/bash",
			wantErr:      false,
		},
		{
			name:         "ShellWithDockerExecutor",
			executorType: "docker",
			shell:        "/bin/sh",
			wantErr:      true, // Docker doesn't use step.Shell field
		},
		{
			name:         "ShellWithSSHExecutor",
			executorType: "ssh",
			shell:        "/bin/bash",
			wantErr:      false, // SSH now supports step.Shell field
		},
		// Executors that do not support shell
		{
			name:         "ShellWithJQExecutor",
			executorType: "jq",
			shell:        "/bin/bash",
			wantErr:      true,
		},
		{
			name:         "ShellWithHTTPExecutor",
			executorType: "http",
			shell:        "/bin/bash",
			wantErr:      true,
		},
		{
			name:         "ShellWithKubernetesExecutor",
			executorType: "kubernetes",
			shell:        "/bin/bash",
			wantErr:      true,
		},
		{
			name:         "ShellWithK8sExecutor",
			executorType: "k8s",
			shell:        "/bin/bash",
			wantErr:      true,
		},
		{
			name:         "ShellWithMailExecutor",
			executorType: "mail",
			shell:        "/bin/bash",
			wantErr:      true,
		},
		// Empty shell - should always pass
		{
			name:         "EmptyShellWithJQExecutor",
			executorType: "jq",
			shell:        "",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{
				Shell: tt.shell,
				ExecutorConfig: core.ExecutorConfig{
					Type: tt.executorType,
				},
			}
			err := validateShell(result)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "does not support shell configuration")
				assert.Contains(t, err.Error(), tt.executorType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executorType string
		container    *core.Container
		wantErr      bool
	}{
		// Executors that support container
		{
			name:         "ContainerWithDockerExecutor",
			executorType: "docker",
			container:    &core.Container{Image: "alpine"},
			wantErr:      false,
		},
		// Executors that do not support container
		{
			name:         "ContainerWithSSHExecutor",
			executorType: "ssh",
			container:    &core.Container{Image: "alpine"},
			wantErr:      true,
		},
		{
			name:         "ContainerWithCommandExecutor",
			executorType: "command",
			container:    &core.Container{Image: "alpine"},
			wantErr:      true,
		},
		{
			name:         "ContainerWithJQExecutor",
			executorType: "jq",
			container:    &core.Container{Image: "alpine"},
			wantErr:      true,
		},
		// Nil container - should always pass
		{
			name:         "NilContainerWithSSHExecutor",
			executorType: "ssh",
			container:    nil,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{
				Container: tt.container,
				ExecutorConfig: core.ExecutorConfig{
					Type: tt.executorType,
				},
			}
			err := validateContainer(result)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "does not support container field")
				assert.Contains(t, err.Error(), tt.executorType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSubDAG(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executorType string
		subDAG       *core.SubDAG
		wantErr      bool
	}{
		// Executors that support SubDAG
		{
			name:         "SubDAGWithDAGExecutor",
			executorType: "dag",
			subDAG:       &core.SubDAG{Name: "child-dag"},
			wantErr:      false,
		},
		{
			name:         "SubDAGWithParallelExecutor",
			executorType: "parallel",
			subDAG:       &core.SubDAG{Name: "child-dag"},
			wantErr:      false,
		},
		// Executors that do not support SubDAG
		{
			name:         "SubDAGWithCommandExecutor",
			executorType: "command",
			subDAG:       &core.SubDAG{Name: "child-dag"},
			wantErr:      true,
		},
		{
			name:         "SubDAGWithSSHExecutor",
			executorType: "ssh",
			subDAG:       &core.SubDAG{Name: "child-dag"},
			wantErr:      true,
		},
		{
			name:         "SubDAGWithDockerExecutor",
			executorType: "docker",
			subDAG:       &core.SubDAG{Name: "child-dag"},
			wantErr:      true,
		},
		// Nil SubDAG - should always pass
		{
			name:         "NilSubDAGWithCommandExecutor",
			executorType: "command",
			subDAG:       nil,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{
				SubDAG: tt.subDAG,
				ExecutorConfig: core.ExecutorConfig{
					Type: tt.executorType,
				},
			}
			err := validateSubDAG(result)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "does not support call field")
				assert.Contains(t, err.Error(), tt.executorType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executorType string
		commands     []core.CommandEntry
		wantErr      bool
	}{
		// Executors that support command
		{
			name:         "CommandWithDefaultExecutor",
			executorType: "",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      false,
		},
		{
			name:         "CommandWithShellExecutor",
			executorType: "shell",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      false,
		},
		{
			name:         "CommandWithCommandExecutor",
			executorType: "command",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      false,
		},
		{
			name:         "CommandWithDockerExecutor",
			executorType: "docker",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      false,
		},
		{
			name:         "CommandWithSSHExecutor",
			executorType: "ssh",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      false,
		},
		{
			name:         "CommandWithJQExecutor",
			executorType: "jq",
			commands:     []core.CommandEntry{{Command: ".foo"}},
			wantErr:      false,
		},
		{
			name:         "CommandWithHTTPExecutor",
			executorType: "http",
			commands:     []core.CommandEntry{{Command: "GET", Args: []string{"https://example.com"}}},
			wantErr:      false,
		},
		{
			name:         "CommandWithArchiveExecutor",
			executorType: "archive",
			commands:     []core.CommandEntry{{Command: "extract"}},
			wantErr:      false,
		},
		// Executors that do not support command
		{
			name:         "CommandWithDAGExecutor",
			executorType: "dag",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      true,
		},
		{
			name:         "CommandWithSubworkflowExecutor",
			executorType: "subworkflow",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      true,
		},
		{
			name:         "CommandWithParallelExecutor",
			executorType: "parallel",
			commands:     []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			wantErr:      true,
		},
		{
			name:         "CommandWithMailExecutor",
			executorType: "mail",
			commands:     []core.CommandEntry{{Command: "send"}},
			wantErr:      true,
		},
		// Empty commands - should always pass
		{
			name:         "NoCommandsWithDAGExecutor",
			executorType: "dag",
			commands:     nil,
			wantErr:      false,
		},
		{
			name:         "EmptyCommandsWithMailExecutor",
			executorType: "mail",
			commands:     []core.CommandEntry{},
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{
				Commands: tt.commands,
				ExecutorConfig: core.ExecutorConfig{
					Type: tt.executorType,
				},
			}
			err := validateCommand(result)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "does not support command field")
				assert.Contains(t, err.Error(), tt.executorType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateWorkerSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		executorType   string
		workerSelector map[string]string
		wantErr        bool
	}{
		// Executors that support worker_selector
		{
			name:           "WorkerSelectorWithDAGExecutor",
			executorType:   "dag",
			workerSelector: map[string]string{"env": "prod"},
			wantErr:        false,
		},
		{
			name:           "WorkerSelectorWithSubworkflowExecutor",
			executorType:   "subworkflow",
			workerSelector: map[string]string{"env": "prod"},
			wantErr:        false,
		},
		{
			name:           "WorkerSelectorWithParallelExecutor",
			executorType:   "parallel",
			workerSelector: map[string]string{"env": "prod"},
			wantErr:        false,
		},
		// Executors that do not support worker_selector
		{
			name:           "WorkerSelectorWithShellExecutor",
			executorType:   "shell",
			workerSelector: map[string]string{"env": "prod"},
			wantErr:        true,
		},
		{
			name:           "WorkerSelectorWithCommandExecutor",
			executorType:   "command",
			workerSelector: map[string]string{"env": "prod"},
			wantErr:        true,
		},
		{
			name:           "WorkerSelectorWithDockerExecutor",
			executorType:   "docker",
			workerSelector: map[string]string{"env": "prod"},
			wantErr:        true,
		},
		{
			name:           "WorkerSelectorWithMailExecutor",
			executorType:   "mail",
			workerSelector: map[string]string{"env": "prod"},
			wantErr:        true,
		},
		// Empty worker_selector - should always pass
		{
			name:           "NoWorkerSelectorWithShellExecutor",
			executorType:   "shell",
			workerSelector: nil,
			wantErr:        false,
		},
		{
			name:           "EmptyWorkerSelectorWithShellExecutor",
			executorType:   "shell",
			workerSelector: map[string]string{},
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{
				WorkerSelector: tt.workerSelector,
				ExecutorConfig: core.ExecutorConfig{
					Type: tt.executorType,
				},
			}
			err := validateWorkerSelector(result)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "does not support worker_selector field")
				assert.Contains(t, err.Error(), tt.executorType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStepValidationMessagesUseYAMLTerms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  func() error
		want string
	}{
		{
			name: "unsupported command",
			err: func() error {
				return validateCommand(&core.Step{
					Commands:       []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
					ExecutorConfig: core.ExecutorConfig{Type: "dag"},
				})
			},
			want: `action "dag" does not support command field`,
		},
		{
			name: "unsupported multiple commands",
			err: func() error {
				return validateMultipleCommands(&core.Step{
					Commands: []core.CommandEntry{
						{Command: "GET", Args: []string{"https://example.com"}},
						{Command: "POST", Args: []string{"https://example.com"}},
					},
					ExecutorConfig: core.ExecutorConfig{Type: "http"},
				})
			},
			want: `action "http" supports only one command`,
		},
		{
			name: "unsupported llm",
			err: func() error {
				return validateLLM(&core.Step{
					ExecutorConfig: core.ExecutorConfig{Type: "shell"},
					LLM:            &core.LLMConfig{Provider: "openai", Model: "gpt-4"},
				})
			},
			want: `action "shell" does not support llm field`,
		},
		{
			name: "unknown type",
			err: func() error {
				result := &core.Step{ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)}}
				return buildStepExecutor(testStepBuildContext(), &step{Type: "non-existent"}, result)
			},
			want: `unknown action "non-existent"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.err()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), "executor")
			assert.NotContains(t, err.Error(), "executor_config")
		})
	}
}

func TestUnregisteredExecutorValidation(t *testing.T) {
	t.Parallel()

	yaml := `
steps:
  - name: invalid-step
    action: non-existent
`
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.yaml")
	err := os.WriteFile(tmpFile, []byte(yaml), 0644)
	assert.NoError(t, err)

	_, err = Load(context.Background(), tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown action \"non-existent\"")
	assert.NotContains(t, err.Error(), "does not support command field")
	assert.NotContains(t, err.Error(), "executor")
}

func TestBuildStepLogOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		expected    core.LogOutputMode
		wantErr     bool
		errContains string
	}{
		{
			name:     "Default_InheritFromDAG",
			yaml:     "",
			expected: "", // Empty means inherit from DAG
		},
		{
			name:     "ExplicitSeparate",
			yaml:     "log_output: separate",
			expected: core.LogOutputSeparate,
		},
		{
			name:     "Merged",
			yaml:     "log_output: merged",
			expected: core.LogOutputMerged,
		},
		{
			name:     "MergedUppercase",
			yaml:     "log_output: MERGED",
			expected: core.LogOutputMerged,
		},
		{
			name:        "InvalidValue",
			yaml:        "log_output: invalid",
			wantErr:     true,
			errContains: "invalid log_output value",
		},
		{
			name:        "InvalidValue_Both",
			yaml:        "log_output: both",
			wantErr:     true,
			errContains: "invalid log_output value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var s step
			if tt.yaml != "" {
				err := yaml.Unmarshal([]byte(tt.yaml), &s)
				if tt.wantErr {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.errContains)
					return
				}
				require.NoError(t, err)
			}

			result, err := buildStepLogOutput(testStepBuildContext(), &s)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateStdoutStderr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		stdout      string
		stderr      string
		wantErr     bool
		errContains string
	}{
		{
			name:    "BothEmpty_Valid",
			stdout:  "",
			stderr:  "",
			wantErr: false,
		},
		{
			name:    "OnlyStdout_Valid",
			stdout:  "/tmp/output.log",
			stderr:  "",
			wantErr: false,
		},
		{
			name:    "OnlyStderr_Valid",
			stdout:  "",
			stderr:  "/tmp/error.log",
			wantErr: false,
		},
		{
			name:    "DifferentFiles_Valid",
			stdout:  "/tmp/output.log",
			stderr:  "/tmp/error.log",
			wantErr: false,
		},
		{
			name:        "SameFile_Error",
			stdout:      "/tmp/combined.log",
			stderr:      "/tmp/combined.log",
			wantErr:     true,
			errContains: "stdout and stderr cannot point to the same file",
		},
		{
			name:        "SameFile_Error_ContainsFilename",
			stdout:      "/var/log/app.log",
			stderr:      "/var/log/app.log",
			wantErr:     true,
			errContains: "/var/log/app.log",
		},
		{
			name:        "SameFile_Error_SuggestsMerged",
			stdout:      "output.log",
			stderr:      "output.log",
			wantErr:     true,
			errContains: "log_output: merged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			step := &core.Step{
				Stdout: tt.stdout,
				Stderr: tt.stderr,
			}

			err := validateStdoutStderr(step)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBuildStep_StdoutStderrSameFile_Error(t *testing.T) {
	t.Parallel()

	data := []byte(`
name: test-step
run: echo hello
stdout: /tmp/combined.log
stderr: /tmp/combined.log
`)

	var s step
	err := yaml.Unmarshal(data, &s)
	require.NoError(t, err)

	_, err = s.build(testStepBuildContext())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdout and stderr cannot point to the same file")
	assert.Contains(t, err.Error(), "log_output: merged")
}

func TestBuildStepExecutorNewFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		step     *step
		ctx      StepBuildContext
		expected core.ExecutorConfig
		wantErr  bool
	}{
		{
			name: "NewFormat_TypeOnly",
			step: &step{Type: "http"},
			ctx:  testStepBuildContext(),
			expected: core.ExecutorConfig{
				Type:   "http",
				Config: make(map[string]any),
			},
		},
		{
			name: "NewFormat_TypeAndConfig",
			step: &step{
				Type: "ssh",
				Config: map[string]any{
					"host": "server.com",
					"user": "ubuntu",
				},
			},
			ctx: testStepBuildContext(),
			expected: core.ExecutorConfig{
				Type: "ssh",
				Config: map[string]any{
					"host": "server.com",
					"user": "ubuntu",
				},
			},
		},
		{
			name: "NewFormat_ConfigOnly",
			step: &step{
				Config: map[string]any{
					"timeout": 30,
				},
			},
			ctx: testStepBuildContext(),
			expected: core.ExecutorConfig{
				Type: "",
				Config: map[string]any{
					"timeout": 30,
				},
			},
		},
		{
			name: "NewFormat_TakesPrecedenceOverContainerInference",
			step: &step{
				Type: "http",
			},
			ctx: StepBuildContext{
				BuildContext: testBuildContext(),
				dag:          &core.DAG{Container: &core.Container{Image: "alpine"}},
			},
			expected: core.ExecutorConfig{
				Type:   "http",
				Config: make(map[string]any),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &core.Step{ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)}}
			err := buildStepExecutor(tt.ctx, tt.step, result)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected.Type, result.ExecutorConfig.Type)
			assert.Equal(t, tt.expected.Config, result.ExecutorConfig.Config)
		})
	}
}

func TestStepExecutorNewFormat_Integration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		wantType    string
		wantConfig  map[string]any
		wantErr     bool
		errContains string
	}{
		{
			name: "NewFormat_SSH",
			yaml: `steps:
  - name: deploy
    action: ssh.run
    with:
      command: uptime
      host: prod.example.com
      user: deploy
      port: 22
`,
			wantType: "ssh",
			wantConfig: map[string]any{
				"host": "prod.example.com",
				"user": "deploy",
				"port": uint64(22),
			},
		},
		{
			name: "NewFormat_HTTP",
			yaml: `steps:
  - name: webhook
    action: http.request
    with:
      method: POST
      url: https://api.example.com
      timeout: 30
      headers:
        Authorization: Bearer token123
`,
			wantType: "http",
			wantConfig: map[string]any{
				"method":  "POST",
				"url":     "https://api.example.com",
				"timeout": uint64(30),
				"headers": map[string]any{
					"Authorization": "Bearer token123",
				},
			},
		},
		{
			name: "NewFormat_JQ",
			yaml: `steps:
  - name: parse
    action: jq.filter
    with:
      filter: .name
      raw: true
`,
			wantType: "jq",
			wantConfig: map[string]any{
				"raw": true,
			},
		},
		{
			name: "NewFormat_SSH",
			yaml: `steps:
  - name: ssh-step
    action: ssh.run
    with:
      command: uptime
      host: example.com
`,
			wantType: "ssh",
			wantConfig: map[string]any{
				"host": "example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.yaml")
			err := os.WriteFile(tmpFile, []byte(tt.yaml), 0644)
			require.NoError(t, err)

			dag, err := Load(context.Background(), tmpFile)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}

			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)

			step := dag.Steps[0]
			assert.Equal(t, tt.wantType, step.ExecutorConfig.Type)
			for k, v := range tt.wantConfig {
				assert.Equal(t, v, step.ExecutorConfig.Config[k], "config key %q mismatch", k)
			}
		})
	}
}

func TestValidateLLM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		step    *core.Step
		wantErr bool
		errMsg  string
	}{
		{
			name:    "NilLLM",
			step:    &core.Step{},
			wantErr: false,
		},
		{
			name: "ValidChatStep",
			step: &core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "chat"},
				LLM:            &core.LLMConfig{Provider: "openai", Model: "gpt-4"},
				Messages:       []core.LLMMessage{{Role: "user", Content: "hello"}},
			},
			wantErr: false,
		},
		{
			name: "UnsupportedExecutorType",
			step: &core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "shell"},
				LLM:            &core.LLMConfig{Provider: "openai", Model: "gpt-4"},
			},
			wantErr: true,
			errMsg:  "does not support llm field",
		},
		{
			name: "MissingProvider",
			step: &core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "chat"},
				LLM:            &core.LLMConfig{Model: "gpt-4"},
				Messages:       []core.LLMMessage{{Role: "user", Content: "hello"}},
			},
			wantErr: true,
			errMsg:  "provider is required",
		},
		{
			name: "MissingModel",
			step: &core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "chat"},
				LLM:            &core.LLMConfig{Provider: "openai"},
				Messages:       []core.LLMMessage{{Role: "user", Content: "hello"}},
			},
			wantErr: true,
			errMsg:  "model is required",
		},
		{
			name: "MissingMessages",
			step: &core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "chat"},
				LLM:            &core.LLMConfig{Provider: "openai", Model: "gpt-4"},
			},
			wantErr: true,
			errMsg:  "at least one message is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateLLM(tt.step)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		step    *core.Step
		wantErr bool
	}{
		{
			name:    "NoMessages",
			step:    &core.Step{},
			wantErr: false,
		},
		{
			name: "MessagesWithChatExecutor",
			step: &core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "chat"},
				Messages:       []core.LLMMessage{{Role: "user", Content: "hello"}},
			},
			wantErr: false,
		},
		{
			name: "MessagesWithUnsupportedExecutor",
			step: &core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "shell"},
				Messages:       []core.LLMMessage{{Role: "user", Content: "hello"}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMessages(tt.step)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "does not support messages field")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildStepLLM(t *testing.T) {
	t.Parallel()

	temp := func(v float64) *float64 { return &v }
	tokens := func(v int) *int { return &v }
	model := func(s string) types.ModelValue {
		var m types.ModelValue
		_ = yaml.Unmarshal([]byte(s), &m)
		return m
	}

	tests := []struct {
		name    string
		step    *step
		dag     *core.DAG
		wantErr bool
		errMsg  string
	}{
		{
			name:    "NonLLMExecutor",
			step:    &step{},
			wantErr: false,
		},
		{
			name: "InheritFromDAG",
			step: &step{Type: "chat"},
			dag: &core.DAG{
				LLM: &core.LLMConfig{Provider: "openai", Model: "gpt-4"},
			},
			wantErr: false,
		},
		{
			name: "InvalidProvider",
			step: &step{
				Type: "chat",
				LLM:  &llmConfig{Provider: "invalid", Model: model("test")},
			},
			wantErr: true,
			errMsg:  "llm.provider",
		},
		{
			name: "MissingModel",
			step: &step{
				Type: "chat",
				LLM:  &llmConfig{Provider: "openai"},
			},
			wantErr: true,
			errMsg:  "llm.model",
		},
		{
			name: "TemperatureTooLow",
			step: &step{
				Type: "chat",
				LLM:  &llmConfig{Provider: "openai", Model: model("gpt-4"), Temperature: temp(-0.1)},
			},
			wantErr: true,
			errMsg:  "llm.temperature",
		},
		{
			name: "TemperatureTooHigh",
			step: &step{
				Type: "chat",
				LLM:  &llmConfig{Provider: "openai", Model: model("gpt-4"), Temperature: temp(2.1)},
			},
			wantErr: true,
			errMsg:  "llm.temperature",
		},
		{
			name: "MaxTokensInvalid",
			step: &step{
				Type: "chat",
				LLM:  &llmConfig{Provider: "openai", Model: model("gpt-4"), MaxTokens: tokens(0)},
			},
			wantErr: true,
			errMsg:  "llm.max_tokens",
		},
		{
			name: "TopPTooLow",
			step: &step{
				Type: "chat",
				LLM:  &llmConfig{Provider: "openai", Model: model("gpt-4"), TopP: temp(-0.1)},
			},
			wantErr: true,
			errMsg:  "llm.top_p",
		},
		{
			name: "TopPTooHigh",
			step: &step{
				Type: "chat",
				LLM:  &llmConfig{Provider: "openai", Model: model("gpt-4"), TopP: temp(1.1)},
			},
			wantErr: true,
			errMsg:  "llm.top_p",
		},
		{
			name: "ValidConfig",
			step: &step{
				Type: "chat",
				LLM: &llmConfig{
					Provider:    "openai",
					Model:       model("gpt-4"),
					Temperature: temp(0.7),
					MaxTokens:   tokens(100),
					TopP:        temp(0.9),
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)}}

			// Build executor first to set the type
			ctx := StepBuildContext{
				BuildContext: BuildContext{ctx: context.Background()},
				dag:          tt.dag,
			}
			_ = buildStepExecutor(ctx, tt.step, result)

			err := buildStepLLM(ctx, tt.step, result)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildThinkingConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *thinkingConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "NilConfig",
			cfg:     nil,
			wantErr: false,
		},
		{
			name:    "ValidLowEffort",
			cfg:     &thinkingConfig{Enabled: true, Effort: "low"},
			wantErr: false,
		},
		{
			name:    "ValidMediumEffort",
			cfg:     &thinkingConfig{Enabled: true, Effort: "medium"},
			wantErr: false,
		},
		{
			name:    "ValidHighEffort",
			cfg:     &thinkingConfig{Enabled: true, Effort: "high"},
			wantErr: false,
		},
		{
			name:    "InvalidEffort",
			cfg:     &thinkingConfig{Enabled: true, Effort: "invalid"},
			wantErr: true,
			errMsg:  "thinking.effort",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := buildThinkingConfig(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
				if tt.cfg == nil {
					assert.Nil(t, result)
				} else {
					assert.NotNil(t, result)
					assert.Equal(t, tt.cfg.Enabled, result.Enabled)
				}
			}
		})
	}
}

func TestBuildStepMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		step    *step
		wantErr bool
		errMsg  string
		wantLen int
	}{
		{
			name:    "NoMessages",
			step:    &step{},
			wantErr: false,
			wantLen: 0,
		},
		{
			name: "ValidMessages",
			step: &step{
				Messages: []llmMessage{
					{Role: "user", Content: "hello"},
					{Role: "assistant", Content: "hi"},
				},
			},
			wantErr: false,
			wantLen: 2,
		},
		{
			name: "MissingRole",
			step: &step{
				Messages: []llmMessage{{Content: "hello"}},
			},
			wantErr: true,
			errMsg:  "messages[0].role",
		},
		{
			name: "InvalidRole",
			step: &step{
				Messages: []llmMessage{{Role: "invalid", Content: "hello"}},
			},
			wantErr: true,
			errMsg:  "messages[0].role",
		},
		{
			name: "MissingContent",
			step: &step{
				Messages: []llmMessage{{Role: "user"}},
			},
			wantErr: true,
			errMsg:  "messages[0].content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := &core.Step{}
			err := buildStepMessages(tt.step, result)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
				assert.Len(t, result.Messages, tt.wantLen)
			}
		})
	}
}

func TestValidateAgent_SkillIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		skills  []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid skill IDs",
			skills:  []string{"sql-optimizer", "docker-deploy", "go-testing"},
			wantErr: false,
		},
		{
			name:    "no skills is valid",
			skills:  nil,
			wantErr: false,
		},
		{
			name:    "empty string skill ID",
			skills:  []string{""},
			wantErr: true,
			errMsg:  "invalid skill ID",
		},
		{
			name:    "uppercase not allowed",
			skills:  []string{"SQL-Optimizer"},
			wantErr: true,
			errMsg:  "invalid skill ID",
		},
		{
			name:    "spaces not allowed",
			skills:  []string{"sql optimizer"},
			wantErr: true,
			errMsg:  "invalid skill ID",
		},
		{
			name:    "special characters not allowed",
			skills:  []string{"sql_optimizer"},
			wantErr: true,
			errMsg:  "invalid skill ID",
		},
		{
			name:    "path traversal not allowed",
			skills:  []string{"../etc/passwd"},
			wantErr: true,
			errMsg:  "invalid skill ID",
		},
		{
			name:    "single segment valid",
			skills:  []string{"optimizer"},
			wantErr: false,
		},
		{
			name:    "one invalid among valid fails",
			skills:  []string{"valid-id", "INVALID!", "also-valid"},
			wantErr: true,
			errMsg:  "invalid skill ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			step := &core.Step{
				Agent: &core.AgentStepConfig{
					Skills: tt.skills,
				},
				Messages: []core.LLMMessage{{Role: "user", Content: "test"}},
			}
			err := validateAgent(step)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
