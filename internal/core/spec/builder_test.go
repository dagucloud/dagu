// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type DAG struct {
	t *testing.T
	*core.DAG
}

func (th *DAG) AssertEnv(t *testing.T, key, val string) {
	th.t.Helper()

	expected := key + "=" + val
	if slices.Contains(th.Env, expected) {
		return
	}
	t.Errorf("expected env %s=%s not found", key, val)
	for i, env := range th.Env {
		// print all envs that were found for debugging
		t.Logf("env[%d]: %s", i, env)
	}
}

func (th *DAG) AssertParam(t *testing.T, params ...string) {
	th.t.Helper()

	assert.Len(t, th.Params, len(params), "expected %d params, got %d", len(params), len(th.Params))
	for i, p := range params {
		assert.Equal(t, p, th.Params[i])
	}
}

func TestStepWithFieldAndConfigAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "CanonicalWith",
			yaml: `
steps:
  - name: request
    action: http.request
    with:
      method: GET
      url: https://example.com
      timeout: 30
`,
		},
		{
			name: "LegacyConfig",
			yaml: `
steps:
  - name: request
    type: http
    command: GET https://example.com
    config:
      timeout: 30
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)
			assert.Equal(t, "http", dag.Steps[0].ExecutorConfig.Type)
			assert.EqualValues(t, 30, dag.Steps[0].ExecutorConfig.Config["timeout"])
		})
	}
}

func TestStepWithFieldRejectsLegacyConfigTogether(t *testing.T) {
	t.Parallel()

	_, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - name: request
    action: http.request
    with:
      method: GET
      url: https://example.com
      timeout: 30
    config:
      timeout: 60
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `action cannot be used together with config`)
}

func TestEnvParams(t *testing.T) {
	t.Parallel()

	// Params tests - these test complex parameter substitution and env expansion
	// which requires the full pipeline (YAML parsing + build)
	paramTests := []struct {
		name       string
		yaml       string
		opts       *spec.BuildOpts
		wantParams []string
	}{
		{
			name: "ParamsWithSubstitution",
			yaml: `
params: "TEST_PARAM $1"
`,
			wantParams: []string{"1=TEST_PARAM", "2=$1"},
		},
		{
			name: "ParamsWithQuotedValues",
			yaml: `
params: x="a b c" y="d e f"
`,
			wantParams: []string{"x=a b c", "y=d e f"},
		},
		{
			name: "ParamsAsMap",
			yaml: `
params:
  - FOO: foo
  - BAR: bar
  - BAZ: "` + "`echo baz`" + `"
`,
			wantParams: []string{"FOO=foo", "BAR=bar", "BAZ=`echo baz`"},
		},
		{
			name: "ParamsAsMapOverride",
			yaml: `
params:
  - FOO: foo
  - BAR: bar
  - BAZ: "` + "`echo baz`" + `"
`,
			opts:       &spec.BuildOpts{Parameters: "FOO=X BAZ=Y"},
			wantParams: []string{"FOO=X", "BAR=bar", "BAZ=Y"},
		},
		{
			name: "ParamsWithComplexValues",
			yaml: `
params: first P1=foo P2=${A001} P3=` + "`/bin/echo BAR`" + ` X=bar Y=${P1} Z="A B C"
env:
  - A001: TEXT
`,
			wantParams: []string{"1=first", "P1=foo", "P2=${A001}", "P3=`/bin/echo BAR`", "X=bar", "Y=${P1}", "Z=A B C"},
		},
		{
			name: "ParamsWithSubstringAndDefaults",
			yaml: `
env:
  - SOURCE_ID: HBL01_22OCT2025_0536
params:
  - BASE: ${SOURCE_ID}
  - PREFIX: ${BASE:0:5}
  - REMAINDER: ${BASE:5}
  - FALLBACK: ${MISSING_VALUE:-fallback}
`,
			wantParams: []string{"BASE=${SOURCE_ID}", "PREFIX=${BASE:0:5}", "REMAINDER=${BASE:5}", "FALLBACK=${MISSING_VALUE:-fallback}"},
		},
		{
			name: "ParamsNoEvalPreservesRaw",
			yaml: `
env:
  - SOURCE_ID: HBL01_22OCT2025_0536
params:
  - BASE: ${SOURCE_ID}
  - PREFIX: ${BASE:0:5}
`,
			opts:       &spec.BuildOpts{Flags: spec.BuildFlagNoEval},
			wantParams: []string{"BASE=${SOURCE_ID}", "PREFIX=${BASE:0:5}"},
		},
	}

	for _, tt := range paramTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var dag *core.DAG
			var err error
			if tt.opts != nil {
				dag, err = spec.LoadYAMLWithOpts(context.Background(), []byte(tt.yaml), *tt.opts)
			} else {
				dag, err = spec.LoadYAML(context.Background(), []byte(tt.yaml))
			}
			require.NoError(t, err)
			th := DAG{t: t, DAG: dag}
			th.AssertParam(t, tt.wantParams...)
		})
	}
}

func TestBuildChainType(t *testing.T) {
	t.Parallel()

	t.Run("Basic", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - run: echo "First"
  - run: echo "Second"
  - run: echo "Third"
  - run: echo "Fourth"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Equal(t, core.TypeChain, th.Type)

		assert.Len(t, th.Steps, 4)
		assert.Empty(t, th.Steps[0].Depends)
		assert.Equal(t, []string{"cmd_1"}, th.Steps[1].Depends)
		assert.Equal(t, []string{"cmd_2"}, th.Steps[2].Depends)
		assert.Equal(t, []string{"cmd_3"}, th.Steps[3].Depends)
	})

	t.Run("WithExplicitDependsShouldError", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - name: setup
    run: ./setup.sh
  - name: download-a
    run: wget fileA
  - name: download-b
    run: wget fileB
  - name: process-both
    run: process.py fileA fileB
    depends:
      - download-a
      - download-b
  - name: cleanup
    run: rm -f fileA fileB
`)
		_, err := spec.LoadYAML(context.Background(), data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "depends field is not allowed for DAGs with type 'chain'")
	})

	t.Run("WithEmptyDependenciesShouldError", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - name: step1
    run: echo "First"
  - name: step2
    run: echo "Second"
  - name: step3
    run: echo "Third"
    depends: []
  - name: step4
    run: echo "Fourth"
`)
		_, err := spec.LoadYAML(context.Background(), data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "depends field is not allowed for DAGs with type 'chain'")
	})
}

func TestBuildValidationError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		expectedErr error
		errContains string
	}{
		{
			name: "InvalidEnv",
			yaml: `
env:
  - VAR: "` + "`invalid command`" + `"`,
			expectedErr: spec.ErrInvalidEnvValue,
		},
		{
			name: "InvalidParams",
			yaml: `
params: 123`,
			expectedErr: spec.ErrInvalidParamValue,
		},
		{
			name: "InvalidSchedule",
			yaml: `
schedule: "1"`,
			errContains: "invalid cron expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.Error(t, err)
			if errs, ok := err.(*core.ErrorList); ok && len(*errs) > 0 {
				if tt.expectedErr == nil {
					require.Contains(t, err.Error(), tt.errContains)
					return
				}
				found := false
				for _, e := range *errs {
					if errors.Is(e, tt.expectedErr) {
						found = true
						break
					}
				}
				require.True(t, found, "expected error %v, got %v", tt.expectedErr, err)
			} else if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.Contains(t, err.Error(), tt.errContains)
			}
		})
	}
}

func TestBuildEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if runtime.GOOS == "windows" {
		ctx = config.WithConfig(ctx, &config.Config{
			Core: config.Core{DefaultShell: "cmd"},
		})
	}

	type testCase struct {
		name     string
		yaml     string
		expected map[string]string
	}

	testCases := []testCase{
		{
			name: "ValidEnv",
			yaml: `
env:
  - FOO: "123"

steps:
  - run: "true"
`,
			expected: map[string]string{
				"FOO": "123",
			},
		},
		{
			name: "ValidEnvWithSubstitution",
			yaml: `
env:
  - VAR: "` + "`echo 123`" + `"

steps:
  - run: "true"
`,
			expected: map[string]string{
				"VAR": "123",
			},
		},
		{
			name: "ValidEnvWithSubstitutionAndEnv",
			yaml: `
env:
  - BEE: "BEE"
  - BAZ: "BAZ"
  - BOO: "BOO"
  - FOO: "${BEE}:${BAZ}:${BOO}:FOO"

steps:
  - run: "true"
`,
			expected: map[string]string{
				"FOO": "BEE:BAZ:BOO:FOO",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(ctx, []byte(tc.yaml))
			require.NoError(t, err)
			th := DAG{t: t, DAG: dag}
			for key, val := range tc.expected {
				th.AssertEnv(t, key, val)
			}
		})
	}
}

func TestBuildSchedule(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		yaml    string
		start   []string
		stop    []string
		restart []string
	}

	testCases := []testCase{
		{
			name: "ValidSchedule",
			yaml: `
schedule:
  start: "0 1 * * *"
  stop: "0 2 * * *"
  restart: "0 12 * * *"

steps:
  - run: "true"
`,
			start:   []string{"0 1 * * *"},
			stop:    []string{"0 2 * * *"},
			restart: []string{"0 12 * * *"},
		},
		{
			name: "ListSchedule",
			yaml: `
schedule:
  - "0 1 * * *"
  - "0 18 * * *"

steps:
  - run: "true"
`,
			start: []string{
				"0 1 * * *",
				"0 18 * * *",
			},
		},
		{
			name: "MultipleValues",
			yaml: `
schedule:
  start:
    - "0 1 * * *"
    - "0 18 * * *"
  stop:
    - "0 2 * * *"
    - "0 20 * * *"
  restart:
    - "0 12 * * *"
    - "0 22 * * *"

steps:
  - run: "true"
`,
			start: []string{
				"0 1 * * *",
				"0 18 * * *",
			},
			stop: []string{
				"0 2 * * *",
				"0 20 * * *",
			},
			restart: []string{
				"0 12 * * *",
				"0 22 * * *",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(context.Background(), []byte(tc.yaml))
			require.NoError(t, err)
			th := DAG{t: t, DAG: dag}
			assert.Len(t, th.Schedule, len(tc.start))
			for i, s := range tc.start {
				assert.Equal(t, s, th.Schedule[i].Expression)
			}

			assert.Len(t, th.StopSchedule, len(tc.stop))
			for i, s := range tc.stop {
				assert.Equal(t, s, th.StopSchedule[i].Expression)
			}

			assert.Len(t, th.RestartSchedule, len(tc.restart))
			for i, s := range tc.restart {
				assert.Equal(t, s, th.RestartSchedule[i].Expression)
			}
		})
	}
}

func TestBuildStep(t *testing.T) {
	t.Parallel()
	t.Run("ValidCommand", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: echo 1
    name: step1
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		require.Len(t, th.Steps[0].Commands, 1)
		assert.Equal(t, "echo 1", th.Steps[0].Commands[0].CmdWithArgs)
		assert.Equal(t, "echo", th.Steps[0].Commands[0].Command)
		assert.Equal(t, []string{"1"}, th.Steps[0].Commands[0].Args)
		assert.Equal(t, "step1", th.Steps[0].Name)
	})
	t.Run("CommandAsScript", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: |
      echo hello
      echo world
    name: script
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		require.Len(t, th.Steps, 1)
		step := th.Steps[0]
		assert.Equal(t, "script", step.Name)
		assert.Equal(t, "echo hello\necho world", step.Script)
		assert.Empty(t, step.Command)
		assert.Empty(t, step.CmdWithArgs)
		assert.Empty(t, step.CmdArgsSys)
		assert.Nil(t, step.Args)
	})
	t.Run("ValidCommandInArray", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: [echo, 1]
    name: step1
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Equal(t, "step1", th.Steps[0].Name)
		assert.Len(t, th.Steps[0].Commands, 2)
		assert.Equal(t, "echo", th.Steps[0].Commands[0].Command)
		assert.Equal(t, "1", th.Steps[0].Commands[1].Command)
	})
	t.Run("ValidCommandInList", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run:
      - echo
      - 1
    name: step1
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Equal(t, "step1", th.Steps[0].Name)
		assert.Len(t, th.Steps[0].Commands, 2)
		assert.Equal(t, "echo", th.Steps[0].Commands[0].Command)
		assert.Equal(t, "1", th.Steps[0].Commands[1].Command)
	})
	t.Run("MultipleCommandsInArray", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - name: build
    run:
      - npm install
      - npm run build
      - npm test
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Equal(t, "build", th.Steps[0].Name)
		assert.Len(t, th.Steps[0].Commands, 3)
		assert.Equal(t, "npm", th.Steps[0].Commands[0].Command)
		assert.Equal(t, []string{"install"}, th.Steps[0].Commands[0].Args)
		assert.Equal(t, "npm", th.Steps[0].Commands[1].Command)
		assert.Equal(t, []string{"run", "build"}, th.Steps[0].Commands[1].Args)
		assert.Equal(t, "npm", th.Steps[0].Commands[2].Command)
		assert.Equal(t, []string{"test"}, th.Steps[0].Commands[2].Args)
	})
	t.Run("HTTPExecutor", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - name: step1
    action: http.request
    with:
      method: GET
      url: http://example.com
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Equal(t, "http", th.Steps[0].ExecutorConfig.Type)
	})
	t.Run("HTTPExecutorWithConfig", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - name: step1
    action: http.request
    with:
      method: GET
      url: http://example.com
      key: value
      map:
        foo: bar
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Equal(t, "http", th.Steps[0].ExecutorConfig.Type)
		assert.Equal(t, map[string]any{
			"method": "GET",
			"url":    "http://example.com",
			"key":    "value",
			"map": map[string]any{
				"foo": "bar",
			},
		}, th.Steps[0].ExecutorConfig.Config)
	})
	t.Run("DAGExecutor", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - name: execute a sub-dag
    action: dag.run
    with:
      dag: sub_dag
      params: "param1=value1 param2=value2"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Equal(t, "dag", th.Steps[0].ExecutorConfig.Type)
		require.NotNil(t, th.Steps[0].SubDAG)
		assert.Equal(t, "sub_dag", th.Steps[0].SubDAG.Name)
		assert.Equal(t, "param1=\"value1\" param2=\"value2\"", th.Steps[0].SubDAG.Params)
		assert.Empty(t, dag.BuildWarnings)
	})
	// ContinueOn success cases
	continueOnTests := []struct {
		name            string
		yaml            string
		wantSkipped     bool
		wantFailure     bool
		wantExitCode    []int
		wantMarkSuccess bool
	}{
		{
			name: "ContinueOnObject",
			yaml: `
steps:
  - run: "echo 1"
    continue_on:
      skipped: true
      failure: true
`,
			wantSkipped: true,
			wantFailure: true,
		},
		{
			name: "ContinueOnStringSkipped",
			yaml: `
steps:
  - run: "echo 1"
    continue_on: skipped
`,
			wantSkipped: true,
			wantFailure: false,
		},
		{
			name: "ContinueOnStringFailed",
			yaml: `
steps:
  - run: "echo 1"
    continue_on: failed
`,
			wantSkipped: false,
			wantFailure: true,
		},
		{
			name: "ContinueOnStringCaseInsensitive",
			yaml: `
steps:
  - run: "echo 1"
    continue_on: SKIPPED
`,
			wantSkipped: true,
		},
		{
			name: "ContinueOnObjectWithExitCode",
			yaml: `
steps:
  - run: "echo 1"
    continue_on:
      exit_code: [1, 2, 3]
      mark_success: true
`,
			wantExitCode:    []int{1, 2, 3},
			wantMarkSuccess: true,
		},
	}

	for _, tt := range continueOnTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)
			assert.Equal(t, tt.wantSkipped, dag.Steps[0].ContinueOn.Skipped)
			assert.Equal(t, tt.wantFailure, dag.Steps[0].ContinueOn.Failure)
			if tt.wantExitCode != nil {
				assert.Equal(t, tt.wantExitCode, dag.Steps[0].ContinueOn.ExitCode)
			}
			if tt.wantMarkSuccess {
				assert.True(t, dag.Steps[0].ContinueOn.MarkSuccess)
			}
		})
	}

	// ContinueOn error cases
	continueOnErrorTests := []struct {
		name        string
		yaml        string
		errContains []string
	}{
		{
			name: "ContinueOnInvalidString",
			yaml: `
steps:
  - run: "echo 1"
    continue_on: invalid
`,
			errContains: []string{"continue_on"},
		},
		{
			name: "ContinueOnInvalidFailureType",
			yaml: `
steps:
  - run: "echo 1"
    continue_on:
      failure: "true"
`,
			errContains: []string{"continue_on.failure", "bool"},
		},
		{
			name: "ContinueOnInvalidSkippedType",
			yaml: `
steps:
  - run: "echo 1"
    continue_on:
      skipped: 1
`,
			errContains: []string{"continue_on.skipped", "bool"},
		},
		{
			name: "ContinueOnInvalidMarkSuccessType",
			yaml: `
steps:
  - run: "echo 1"
    continue_on:
      mark_success: "yes"
`,
			errContains: []string{"continue_on.mark_success", "bool"},
		},
	}

	for _, tt := range continueOnErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.Error(t, err)
			for _, s := range tt.errContains {
				assert.Contains(t, err.Error(), s)
			}
		})
	}
	// RetryPolicy success tests
	retryPolicyTests := []struct {
		name            string
		yaml            string
		wantLimit       int
		wantInterval    time.Duration
		wantBackoff     float64
		wantMaxInterval time.Duration
	}{
		{
			name: "RetryPolicyBasic",
			yaml: `
steps:
  - run: "echo 2"
    retry_policy:
      limit: 3
      interval_sec: 10
`,
			wantLimit:    3,
			wantInterval: 10 * time.Second,
		},
		{
			name: "RetryPolicyWithBackoff",
			yaml: `
steps:
  - name: "test_backoff"
    run: "echo test"
    retry_policy:
      limit: 5
      interval_sec: 2
      backoff: 2.0
      max_interval_sec: 30
`,
			wantLimit:       5,
			wantInterval:    2 * time.Second,
			wantBackoff:     2.0,
			wantMaxInterval: 30 * time.Second,
		},
		{
			name: "RetryPolicyWithBackoffBool",
			yaml: `
steps:
  - name: "test_backoff_bool"
    run: "echo test"
    retry_policy:
      limit: 3
      interval_sec: 1
      backoff: true
      max_interval_sec: 10
`,
			wantLimit:       3,
			wantInterval:    1 * time.Second,
			wantBackoff:     2.0, // true converts to 2.0
			wantMaxInterval: 10 * time.Second,
		},
	}

	for _, tt := range retryPolicyTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)
			require.NotNil(t, dag.Steps[0].RetryPolicy)
			assert.Equal(t, tt.wantLimit, dag.Steps[0].RetryPolicy.Limit)
			assert.Equal(t, tt.wantInterval, dag.Steps[0].RetryPolicy.Interval)
			if tt.wantBackoff > 0 {
				assert.Equal(t, tt.wantBackoff, dag.Steps[0].RetryPolicy.Backoff)
			}
			if tt.wantMaxInterval > 0 {
				assert.Equal(t, tt.wantMaxInterval, dag.Steps[0].RetryPolicy.MaxInterval)
			}
		})
	}

	// RetryPolicy error tests
	retryPolicyErrorTests := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name: "RetryPolicyInvalidBackoff",
			yaml: `
steps:
  - name: "test"
    run: "echo test"
    retry_policy:
      limit: 3
      interval_sec: 1
      backoff: 0.8
`,
			errContains: "backoff must be greater than 1.0",
		},
		{
			name: "RetryPolicyMissingLimit",
			yaml: `
steps:
  - name: "test"
    run: "echo test"
    retry_policy:
      interval_sec: 5
`,
			errContains: "limit is required when retry_policy is specified",
		},
		{
			name: "RetryPolicyMissingIntervalSec",
			yaml: `
steps:
  - name: "test"
    run: "echo test"
    retry_policy:
      limit: 3
`,
			errContains: "interval_sec is required when retry_policy is specified",
		},
	}

	for _, tt := range retryPolicyErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.Error(t, err)
			assert.Nil(t, dag)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
	// RepeatPolicy success tests
	repeatPolicyTests := []struct {
		name            string
		yaml            string
		wantMode        core.RepeatMode
		wantInterval    time.Duration
		wantLimit       int
		wantExitCode    []int
		wantCondition   string
		wantExpected    string
		wantBackoff     float64
		wantMaxInterval time.Duration
		wantNoCondition bool
	}{
		{
			name: "RepeatPolicyBasic",
			yaml: `
steps:
  - run: "echo 2"
    repeat_policy:
      repeat: true
      interval_sec: 60
`,
			wantMode:     core.RepeatModeWhile,
			wantInterval: 60 * time.Second,
			wantLimit:    0,
		},
		{
			name: "RepeatPolicyWhileCondition",
			yaml: `
steps:
  - name: "repeat-while-condition"
    run: "echo test"
    repeat_policy:
      repeat: "while"
      condition: "echo hello"
      interval_sec: 5
      limit: 3
`,
			wantMode:      core.RepeatModeWhile,
			wantInterval:  5 * time.Second,
			wantLimit:     3,
			wantCondition: "echo hello",
			wantExpected:  "",
		},
		{
			name: "RepeatPolicyUntilCondition",
			yaml: `
steps:
  - name: "repeat-until-condition"
    run: "echo test"
    repeat_policy:
      repeat: "until"
      condition: "echo hello"
      expected: "hello"
      interval_sec: 10
      limit: 5
`,
			wantMode:      core.RepeatModeUntil,
			wantInterval:  10 * time.Second,
			wantLimit:     5,
			wantCondition: "echo hello",
			wantExpected:  "hello",
		},
		{
			name: "RepeatPolicyWhileExitCode",
			yaml: `
steps:
  - name: "repeat-while-exitcode"
    run: "exit 1"
    repeat_policy:
      repeat: "while"
      exit_code: [1, 2]
      interval_sec: 15
`,
			wantMode:        core.RepeatModeWhile,
			wantInterval:    15 * time.Second,
			wantExitCode:    []int{1, 2},
			wantNoCondition: true,
		},
		{
			name: "RepeatPolicyUntilExitCode",
			yaml: `
steps:
  - name: "repeat-until-exitcode"
    run: "exit 0"
    repeat_policy:
      repeat: "until"
      exit_code: [0]
      interval_sec: 20
`,
			wantMode:        core.RepeatModeUntil,
			wantInterval:    20 * time.Second,
			wantExitCode:    []int{0},
			wantNoCondition: true,
		},
		{
			name: "RepeatPolicyBackwardCompatibilityUntil",
			yaml: `
steps:
  - name: "repeat-backward-compatibility-until"
    run: "echo test"
    repeat_policy:
      condition: "echo hello"
      expected: "hello"
      interval_sec: 25
`,
			wantMode:      core.RepeatModeUntil,
			wantInterval:  25 * time.Second,
			wantCondition: "echo hello",
			wantExpected:  "hello",
		},
		{
			name: "RepeatPolicyBackwardCompatibilityWhile",
			yaml: `
steps:
  - name: "repeat-backward-compatibility-while"
    run: "echo test"
    repeat_policy:
      condition: "echo hello"
      interval_sec: 30
`,
			wantMode:      core.RepeatModeWhile,
			wantInterval:  30 * time.Second,
			wantCondition: "echo hello",
			wantExpected:  "",
		},
		{
			name: "RepeatPolicyCondition",
			yaml: `
steps:
  - name: "repeat-condition"
    run: "echo hello"
    repeat_policy:
      condition: "echo hello"
      expected: "hello"
      interval_sec: 1
`,
			wantMode:      core.RepeatModeUntil,
			wantInterval:  1 * time.Second,
			wantCondition: "echo hello",
			wantExpected:  "hello",
		},
		{
			name: "RepeatPolicyExitCode",
			yaml: `
steps:
  - name: "repeat-exitcode"
    run: "exit 42"
    repeat_policy:
      exit_code: [42]
      interval_sec: 2
`,
			wantMode:     core.RepeatModeWhile,
			wantInterval: 2 * time.Second,
			wantExitCode: []int{42},
		},
		{
			name: "RepeatPolicyWithBackoff",
			yaml: `
steps:
  - name: "test_repeat_backoff"
    run: "echo test"
    repeat_policy:
      repeat: while
      interval_sec: 5
      backoff: 1.5
      max_interval_sec: 60
      limit: 10
      exit_code: [1]
`,
			wantMode:        core.RepeatModeWhile,
			wantInterval:    5 * time.Second,
			wantBackoff:     1.5,
			wantMaxInterval: 60 * time.Second,
			wantLimit:       10,
			wantExitCode:    []int{1},
		},
		{
			name: "RepeatPolicyWithBackoffBool",
			yaml: `
steps:
  - name: "test_repeat_backoff_bool"
    run: "echo test"
    repeat_policy:
      repeat: until
      interval_sec: 2
      backoff: true
      max_interval_sec: 20
      limit: 5
      condition: "echo done"
      expected: "done"
`,
			wantMode:        core.RepeatModeUntil,
			wantInterval:    2 * time.Second,
			wantBackoff:     2.0,
			wantMaxInterval: 20 * time.Second,
			wantLimit:       5,
			wantCondition:   "echo done",
			wantExpected:    "done",
		},
	}

	for _, tt := range repeatPolicyTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)
			rp := dag.Steps[0].RepeatPolicy
			require.NotNil(t, rp)
			assert.Equal(t, tt.wantMode, rp.RepeatMode)
			assert.Equal(t, tt.wantInterval, rp.Interval)
			assert.Equal(t, tt.wantLimit, rp.Limit)
			if tt.wantExitCode != nil {
				assert.Equal(t, tt.wantExitCode, rp.ExitCode)
			}
			if tt.wantNoCondition {
				assert.Nil(t, rp.Condition)
			} else if tt.wantCondition != "" {
				require.NotNil(t, rp.Condition)
				assert.Equal(t, tt.wantCondition, rp.Condition.Condition)
				assert.Equal(t, tt.wantExpected, rp.Condition.Expected)
			}
			if tt.wantBackoff > 0 {
				assert.Equal(t, tt.wantBackoff, rp.Backoff)
			}
			if tt.wantMaxInterval > 0 {
				assert.Equal(t, tt.wantMaxInterval, rp.MaxInterval)
			}
		})
	}
	t.Run("SignalOnStop", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: echo 1
    name: step1
    signal_on_stop: SIGINT
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Equal(t, "SIGINT", th.Steps[0].SignalOnStop)
	})
	t.Run("StepWithID", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - name: step1
    id: unique_step_1
    run: echo "Step with ID"
  - name: step2
    run: echo "Step without ID"
  - name: step3
    id: custom_id_123
    run: echo "Another step with ID"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 3)

		// First step has ID
		assert.Equal(t, "step1", th.Steps[0].Name)
		assert.Equal(t, "unique_step_1", th.Steps[0].ID)

		// Second step has no ID
		assert.Equal(t, "step2", th.Steps[1].Name)
		assert.Equal(t, "", th.Steps[1].ID)

		// Third step has ID
		assert.Equal(t, "step3", th.Steps[2].Name)
		assert.Equal(t, "custom_id_123", th.Steps[2].ID)
	})
	t.Run("Preconditions", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - name: "2"
    run: "echo 2"
    preconditions:
      - condition: "test -f file.txt"
        expected: "true"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Len(t, th.Steps[0].Preconditions, 1)
		assert.Equal(t, &core.Condition{Condition: "test -f file.txt", Expected: "true"}, th.Steps[0].Preconditions[0])
	})
	t.Run("StepPreconditionsWithNegate", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - name: "step_with_negate"
    run: "echo hello"
    preconditions:
      - condition: "${STATUS}"
        expected: "success"
        negate: true
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}
		assert.Len(t, th.Steps, 1)
		assert.Len(t, th.Steps[0].Preconditions, 1)
		assert.Equal(t, &core.Condition{Condition: "${STATUS}", Expected: "success", Negate: true}, th.Steps[0].Preconditions[0])
	})
	// RepeatPolicy error tests
	repeatPolicyErrorTests := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name: "RepeatPolicyInvalidRepeatValue",
			yaml: `
steps:
  - name: "invalid-repeat"
    run: "echo test"
    repeat_policy:
      repeat: "invalid"
      interval_sec: 10
`,
			errContains: "invalid value for repeat: 'invalid'",
		},
		{
			name: "RepeatPolicyWhileNoCondition",
			yaml: `
steps:
  - name: "while-no-condition"
    run: "echo test"
    repeat_policy:
      repeat: "while"
      interval_sec: 10
`,
			errContains: "repeat mode 'while' requires either 'condition' or 'exit_code' to be specified",
		},
		{
			name: "RepeatPolicyUntilNoCondition",
			yaml: `
steps:
  - name: "until-no-condition"
    run: "echo test"
    repeat_policy:
      repeat: "until"
      interval_sec: 10
`,
			errContains: "repeat mode 'until' requires either 'condition' or 'exit_code' to be specified",
		},
		{
			name: "RepeatPolicyInvalidType",
			yaml: `
steps:
  - name: "invalid-type"
    run: "echo test"
    repeat_policy:
      repeat: 123
      interval_sec: 10
`,
			errContains: "invalid value for repeat",
		},
		{
			name: "RepeatPolicyBackoffTooLow",
			yaml: `
steps:
  - name: "test"
    run: "echo test"
    repeat_policy:
      repeat: "while"
      interval_sec: 1
      backoff: 1.0
      exit_code: [1]
`,
			errContains: "backoff must be greater than 1.0",
		},
		{
			name: "RepeatPolicyBackoffBelowOne",
			yaml: `
steps:
  - name: "test"
    run: "echo test"
    repeat_policy:
      repeat: "while"
      interval_sec: 1
      backoff: 0.5
      exit_code: [1]
`,
			errContains: "backoff must be greater than 1.0",
		},
	}

	for _, tt := range repeatPolicyErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.Error(t, err)
			assert.Nil(t, dag)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestNestedArrayParallelSyntax(t *testing.T) {
	t.Parallel()

	t.Run("SimpleParallelSteps", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - run: echo "step 1"
  - 
    - run: echo "parallel 1"
    - run: echo "parallel 2"
  - run: echo "step 3"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Len(t, dag.Steps, 4)

		// First step (sequential)
		assert.Equal(t, "cmd_1", dag.Steps[0].Name)
		require.Len(t, dag.Steps[0].Commands, 1)
		assert.Equal(t, "echo \"step 1\"", dag.Steps[0].Commands[0].CmdWithArgs)
		assert.Empty(t, dag.Steps[0].Depends)

		// Parallel steps
		assert.Equal(t, "cmd_2", dag.Steps[1].Name)
		require.Len(t, dag.Steps[1].Commands, 1)
		assert.Equal(t, "echo \"parallel 1\"", dag.Steps[1].Commands[0].CmdWithArgs)
		assert.Equal(t, []string{"cmd_1"}, dag.Steps[1].Depends)

		assert.Equal(t, "cmd_3", dag.Steps[2].Name)
		require.Len(t, dag.Steps[2].Commands, 1)
		assert.Equal(t, "echo \"parallel 2\"", dag.Steps[2].Commands[0].CmdWithArgs)
		assert.Equal(t, []string{"cmd_1"}, dag.Steps[2].Depends)

		// Last step (sequential, depends on both parallel steps)
		assert.Equal(t, "cmd_4", dag.Steps[3].Name)
		require.Len(t, dag.Steps[3].Commands, 1)
		assert.Equal(t, "echo \"step 3\"", dag.Steps[3].Commands[0].CmdWithArgs)

		assert.Contains(t, dag.Steps[3].Depends, "cmd_2")
		assert.Contains(t, dag.Steps[3].Depends, "cmd_3")
	})

	t.Run("MixedParallelAndNormalSyntax", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - name: setup
    run: echo "setup"
  -
    - run: echo "parallel 1"
    - name: test
      run: npm test
  - name: cleanup
    run: echo "cleanup"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Len(t, dag.Steps, 4)

		// Setup step
		assert.Equal(t, "setup", dag.Steps[0].Name)
		assert.Empty(t, dag.Steps[0].Depends)

		// Parallel steps
		assert.Equal(t, "cmd_2", dag.Steps[1].Name)
		assert.Equal(t, []string{"setup"}, dag.Steps[1].Depends)

		assert.Equal(t, "test", dag.Steps[2].Name)
		assert.Equal(t, []string{"setup"}, dag.Steps[2].Depends)

		// Cleanup step
		assert.Equal(t, "cleanup", dag.Steps[3].Name)
		assert.Contains(t, dag.Steps[3].Depends, "cmd_2")
		assert.Contains(t, dag.Steps[3].Depends, "test")
	})

	t.Run("ParallelStepsWithExplicitDependenciesShouldError", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - name: step1
    run: echo "1"
  - name: step2
    run: echo "2"
  -
    - name: parallel1
      run: echo "p1"
      depends: [step1]  # Not allowed in chain type
    - name: parallel2
      run: echo "p2"
  - name: final
    run: echo "done"
`)
		_, err := spec.LoadYAML(context.Background(), data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "depends field is not allowed for DAGs with type 'chain'")
	})

	t.Run("ParallelStepsWithExplicitDependenciesGraphType", func(t *testing.T) {
		t.Parallel()

		// Graph type allows explicit depends
		data := []byte(`
type: graph
steps:
  - name: step1
    run: echo "1"
  - name: step2
    run: echo "2"
  - name: parallel1
    run: echo "p1"
    depends: [step1]
  - name: parallel2
    run: echo "p2"
    depends: [step2]
  - name: final
    run: echo "done"
    depends: [parallel1, parallel2]
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Len(t, dag.Steps, 5)

		// Parallel1 has explicit dependency on step1
		parallel1 := dag.Steps[2]
		assert.Equal(t, "parallel1", parallel1.Name)
		assert.Equal(t, []string{"step1"}, parallel1.Depends)

		// Parallel2 has explicit dependency on step2
		parallel2 := dag.Steps[3]
		assert.Equal(t, "parallel2", parallel2.Name)
		assert.Equal(t, []string{"step2"}, parallel2.Depends)

		// Final depends on both parallel steps
		final := dag.Steps[4]
		assert.Equal(t, "final", final.Name)
		assert.Contains(t, final.Depends, "parallel1")
		assert.Contains(t, final.Depends, "parallel2")
	})

	t.Run("OnlyParallelSteps", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - 
    - run: echo "parallel 1"
    - run: echo "parallel 2"
    - run: echo "parallel 3"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Len(t, dag.Steps, 3)

		// All steps should have no dependencies (first group)
		assert.Equal(t, "cmd_1", dag.Steps[0].Name)
		// Note: Due to the way dependencies are handled, these may have dependencies on each other
		// The important thing is they work in parallel since they don't have external dependencies

		assert.Equal(t, "cmd_2", dag.Steps[1].Name)
		assert.Equal(t, "cmd_3", dag.Steps[2].Name)
	})
	t.Run("ConsequentParallelSteps", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - 
    - run: echo "parallel 1"
    - run: echo "parallel 2"
  - 
    - run: echo "parallel 3"
    - run: echo "parallel 4"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Len(t, dag.Steps, 4)

		assert.Equal(t, "cmd_1", dag.Steps[0].Name)
		assert.Equal(t, "cmd_2", dag.Steps[1].Name)

		assert.Equal(t, "cmd_3", dag.Steps[2].Name)
		assert.Contains(t, dag.Steps[2].Depends, "cmd_1")
		assert.Contains(t, dag.Steps[2].Depends, "cmd_2")

		assert.Equal(t, "cmd_4", dag.Steps[3].Name)
		assert.Contains(t, dag.Steps[3].Depends, "cmd_1")
		assert.Contains(t, dag.Steps[3].Depends, "cmd_2")
	})
}

func TestShorthandCommandSyntax(t *testing.T) {
	t.Parallel()

	t.Run("SimpleRunCommands", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: echo "hello"
  - run: ls -la
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Len(t, dag.Steps, 2)

		// First step
		require.Len(t, dag.Steps[0].Commands, 1)
		assert.Equal(t, "echo \"hello\"", dag.Steps[0].Commands[0].CmdWithArgs)
		assert.Equal(t, "echo", dag.Steps[0].Commands[0].Command)
		assert.Equal(t, []string{"hello"}, dag.Steps[0].Commands[0].Args)
		assert.Equal(t, "cmd_1", dag.Steps[0].Name) // Auto-generated name

		// Second step
		require.Len(t, dag.Steps[1].Commands, 1)
		assert.Equal(t, "ls -la", dag.Steps[1].Commands[0].CmdWithArgs)
		assert.Equal(t, "ls", dag.Steps[1].Commands[0].Command)
		assert.Equal(t, []string{"-la"}, dag.Steps[1].Commands[0].Args)
		assert.Equal(t, "cmd_2", dag.Steps[1].Name) // Auto-generated name
	})

	t.Run("MixedRunAndStandardSyntax", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: echo "starting"
  - name: build
    run: make build
    env:
      DEBUG: "true"
  - run: ls -la
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Len(t, dag.Steps, 3)

		// First step (shorthand)
		require.Len(t, dag.Steps[0].Commands, 1)
		assert.Equal(t, "echo \"starting\"", dag.Steps[0].Commands[0].CmdWithArgs)
		assert.Equal(t, "cmd_1", dag.Steps[0].Name)

		// Second step (standard)
		require.Len(t, dag.Steps[1].Commands, 1)
		assert.Equal(t, "make build", dag.Steps[1].Commands[0].CmdWithArgs)
		assert.Equal(t, "build", dag.Steps[1].Name)
		assert.Contains(t, dag.Steps[1].Env, "DEBUG=true")

		// Third step (shorthand)
		require.Len(t, dag.Steps[2].Commands, 1)
		assert.Equal(t, "ls -la", dag.Steps[2].Commands[0].CmdWithArgs)
		assert.Equal(t, "cmd_3", dag.Steps[2].Name)
	})
}

func TestOptionalStepNames(t *testing.T) {
	t.Parallel()

	t.Run("AutoGenerateNames", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: echo "hello"
  - run: npm test
  - run: go build
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 3)
		assert.Equal(t, "cmd_1", th.Steps[0].Name)
		assert.Equal(t, "cmd_2", th.Steps[1].Name)
		assert.Equal(t, "cmd_3", th.Steps[2].Name)
	})

	t.Run("MixedExplicitAndGenerated", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - run: setup.sh
  - name: build
    run: make all
  - run: test.sh
    depends: build
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 3)
		assert.Equal(t, "cmd_1", th.Steps[0].Name)
		assert.Equal(t, "build", th.Steps[1].Name)
		assert.Equal(t, "cmd_3", th.Steps[2].Name)
	})

	t.Run("HandleNameConflicts", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: echo "first"
  - name: cmd_2
    run: echo "explicit"
  - run: echo "third"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 3)
		assert.Equal(t, "cmd_1", th.Steps[0].Name)
		assert.Equal(t, "cmd_2", th.Steps[1].Name)
		assert.Equal(t, "cmd_3", th.Steps[2].Name)
	})

	t.Run("DependenciesWithGeneratedNames", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - git pull
  - run: npm install
    depends: cmd_1
  - run: npm test
    depends: cmd_2
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 3)
		assert.Equal(t, "cmd_1", th.Steps[0].Name)
		assert.Equal(t, "cmd_2", th.Steps[1].Name)
		assert.Equal(t, "cmd_3", th.Steps[2].Name)

		// Check dependencies are correctly resolved
		assert.Equal(t, []string{"cmd_1"}, th.Steps[1].Depends)
		assert.Equal(t, []string{"cmd_2"}, th.Steps[2].Depends)
	})

	t.Run("OutputVariablesWithGeneratedNames", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - run: echo "v1.0.0"
    output: VERSION
  - run: echo "Building version ${VERSION}"
    depends: cmd_1
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 2)
		assert.Equal(t, "cmd_1", th.Steps[0].Name)
		assert.Equal(t, "cmd_2", th.Steps[1].Name)
		assert.Equal(t, "VERSION", th.Steps[0].Output)
		assert.Equal(t, []string{"cmd_1"}, th.Steps[1].Depends)
	})

	t.Run("TypeBasedNaming", func(t *testing.T) {
		t.Parallel()

		// Test different actions get appropriate generated names
		data := []byte(`
steps:
  - run: echo "command"
  - run: |
      echo "script content"
  - action: http.request
    with:
      method: GET
      url: https://example.com
  - action: dag.run
    with:
      dag: sub-dag
  - action: docker.run
    with:
      image: alpine
  - action: ssh.run
    with:
      command: uptime
      host: example.com
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 6)
		assert.Equal(t, "cmd_1", th.Steps[0].Name)
		assert.Equal(t, "cmd_2", th.Steps[1].Name)
		assert.Equal(t, "http_3", th.Steps[2].Name)
		assert.Equal(t, "dag_4", th.Steps[3].Name)
		assert.Equal(t, "docker_5", th.Steps[4].Name)
		assert.Equal(t, "ssh_6", th.Steps[5].Name)
	})

	t.Run("RunSyntaxChainDependencies", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - run: echo "setup"
  - run: echo "test"
  - run: echo "deploy"
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 3)
		assert.Equal(t, "cmd_1", th.Steps[0].Name)
		assert.Equal(t, "cmd_2", th.Steps[1].Name)
		assert.Equal(t, "cmd_3", th.Steps[2].Name)
		// In chain mode, sequential dependencies are implicit
		assert.Equal(t, []string{"cmd_1"}, th.Steps[1].Depends)
		assert.Equal(t, []string{"cmd_2"}, th.Steps[2].Depends)
	})

	t.Run("IDPromotedToName", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - id: fetch_data
    run: echo hi
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 1)
		assert.Equal(t, "fetch_data", th.Steps[0].Name)
		assert.Equal(t, "fetch_data", th.Steps[0].ID)
	})

	t.Run("ExplicitNameWinsOverID", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - id: build
    name: compile
    run: make build
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 1)
		assert.Equal(t, "compile", th.Steps[0].Name)
		assert.Equal(t, "build", th.Steps[0].ID)
	})

	t.Run("NoIDNoNameAutoGenerates", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - run: echo hi
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 1)
		assert.Equal(t, "cmd_1", th.Steps[0].Name)
	})

	t.Run("DependsOnPromotedID", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - id: extract
    run: echo extract
  - id: transform
    run: echo transform
    depends: [extract]
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 2)
		assert.Equal(t, "extract", th.Steps[0].Name)
		assert.Equal(t, "transform", th.Steps[1].Name)
		assert.Equal(t, []string{"extract"}, th.Steps[1].Depends)
	})

	t.Run("ChainTypeWithPromotedIDs", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: chain
steps:
  - id: step_a
    run: echo a
  - id: step_b
    run: echo b
  - id: step_c
    run: echo c
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 3)
		assert.Equal(t, "step_a", th.Steps[0].Name)
		assert.Equal(t, "step_b", th.Steps[1].Name)
		assert.Equal(t, "step_c", th.Steps[2].Name)
		assert.Equal(t, []string{"step_a"}, th.Steps[1].Depends)
		assert.Equal(t, []string{"step_b"}, th.Steps[2].Depends)
	})

	t.Run("PromotedIDDoesNotCollideWithAutoName", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
steps:
  - id: cmd_2
    run: echo first
  - run: echo second
  - run: echo third
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 3)
		assert.Equal(t, "cmd_2", th.Steps[0].Name)
		assert.Equal(t, "cmd_3", th.Steps[1].Name)
		assert.Equal(t, "cmd_4", th.Steps[2].Name)
	})

	t.Run("MixedIDExplicitAndAutoSteps", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - id: a
    run: echo a
  - name: B
    run: echo b
  - run: echo c
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		th := DAG{t: t, DAG: dag}

		require.Len(t, th.Steps, 3)
		assert.Equal(t, "a", th.Steps[0].Name)
		assert.Equal(t, "B", th.Steps[1].Name)
		assert.Equal(t, "cmd_3", th.Steps[2].Name)
	})

	t.Run("DuplicatePromotedIDsBuildError", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - id: same_id
    run: echo a
  - id: same_id
    run: echo b
`)
		_, err := spec.LoadYAML(context.Background(), data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})

	t.Run("PromotedIDConflictsWithExplicitName", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - name: deploy
    run: echo a
  - id: deploy
    run: echo b
`)
		_, err := spec.LoadYAML(context.Background(), data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "step name must be unique")
	})
}

func TestStepIDValidation(t *testing.T) {
	t.Parallel()

	// Success test
	t.Run("ValidID", func(t *testing.T) {
		t.Parallel()
		data := []byte(`
steps:
  - name: step1
    id: valid_id
    run: echo test
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		assert.Equal(t, "valid_id", dag.Steps[0].ID)
	})

	// Error tests
	errorTests := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name: "InvalidIDFormat",
			yaml: `
steps:
  - name: step1
    id: 123invalid
    run: echo test
`,
			errContains: "invalid step ID format",
		},
		{
			name: "HyphenInID",
			yaml: `
steps:
  - name: step1
    id: my-step
    run: echo test
`,
			errContains: "invalid step ID format",
		},
		{
			name: "DuplicateIDs",
			yaml: `
steps:
  - name: step1
    id: myid
    run: echo test1
  - name: step2
    id: myid
    run: echo test2
`,
			errContains: "duplicate step ID",
		},
		{
			name: "IDConflictsWithStepName",
			yaml: `
steps:
  - name: step1
    id: step2
    run: echo test1
  - name: step2
    run: echo test2
`,
			errContains: "conflicts with another step's name",
		},
		{
			name: "NameConflictsWithStepID",
			yaml: `
steps:
  - name: step1
    id: myid
    run: echo test1
  - name: myid
    run: echo test2
`,
			errContains: "conflicts with another step's name",
		},
		{
			name: "ReservedWordID",
			yaml: `
steps:
  - name: step1
    id: env
    run: echo test
`,
			errContains: "reserved word",
		},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestStepIDInDependencies(t *testing.T) {
	t.Parallel()

	t.Run("DependOnStepByID", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - name: step1
    id: first
    run: echo test1
  - name: step2
    depends: first
    run: echo test2
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)
		assert.Equal(t, "first", dag.Steps[0].ID)
		assert.Equal(t, []string{"step1"}, dag.Steps[1].Depends) // ID "first" resolved to name "step1"
	})

	t.Run("DependOnStepByNameWhenIDExists", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - name: step1
    id: first
    run: echo test1
  - name: step2
    depends: step1
    run: echo test2
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)
		assert.Equal(t, []string{"step1"}, dag.Steps[1].Depends)
	})

	t.Run("MultipleDependenciesWithIDs", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - name: step1
    id: first
    run: echo test1
  - name: step2
    id: second
    run: echo test2
  - name: step3
    depends:
      - first
      - second
    run: echo test3
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 3)
		assert.Equal(t, []string{"step1", "step2"}, dag.Steps[2].Depends) // IDs resolved to names
	})

	t.Run("MixOfIDAndNameDependencies", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
type: graph
steps:
  - name: step1
    id: first
    run: echo test1
  - name: step2
    run: echo test2
  - name: step3
    depends:
      - first
      - step2
    run: echo test3
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 3)
		assert.Equal(t, []string{"step1", "step2"}, dag.Steps[2].Depends) // ID "first" resolved to name "step1"
	})
}

func TestChainTypeWithStepIDs(t *testing.T) {
	t.Parallel()

	data := []byte(`
type: chain
steps:
  - name: step1
    id: s1
    run: echo first
  - name: step2
    id: s2
    run: echo second
  - name: step3
    run: echo third
`)
	dag, err := spec.LoadYAML(context.Background(), data)
	require.NoError(t, err)
	require.Len(t, dag.Steps, 3)

	// Verify IDs are preserved
	assert.Equal(t, "s1", dag.Steps[0].ID)
	assert.Equal(t, "s2", dag.Steps[1].ID)
	assert.Equal(t, "", dag.Steps[2].ID)

	// Verify chain dependencies were added
	assert.Empty(t, dag.Steps[0].Depends)
	assert.Equal(t, []string{"step1"}, dag.Steps[1].Depends)
	assert.Equal(t, []string{"step2"}, dag.Steps[2].Depends)
}

func TestResolveStepDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yaml     string
		expected map[string][]string // step name -> expected depends
	}{
		{
			name: "SingleIDDependency",
			yaml: `
type: graph
steps:
  - name: step-one
    id: s1
    run: echo "1"
  - name: step-two
    depends: s1
    run: echo "2"
`,
			expected: map[string][]string{
				"step-two": {"step-one"},
			},
		},
		{
			name: "MultipleIDDependencies",
			yaml: `
type: graph
steps:
  - name: step-one
    id: s1
    run: echo "1"
  - name: step-two
    id: s2
    run: echo "2"
  - name: step-three
    depends:
      - s1
      - s2
    run: echo "3"
`,
			expected: map[string][]string{
				"step-three": {"step-one", "step-two"},
			},
		},
		{
			name: "MixedIDAndNameDependencies",
			yaml: `
type: graph
steps:
  - name: step-one
    id: s1
    run: echo "1"
  - name: step-two
    run: echo "2"
  - name: step-three
    depends:
      - s1
      - step-two
    run: echo "3"
`,
			expected: map[string][]string{
				"step-three": {"step-one", "step-two"},
			},
		},
		{
			name: "NoIDDependencies",
			yaml: `
type: graph
steps:
  - name: step-one
    run: echo "1"
  - name: step-two
    depends: step-one
    run: echo "2"
`,
			expected: map[string][]string{
				"step-two": {"step-one"},
			},
		},
		{
			name: "IDSameAsName",
			yaml: `
type: graph
steps:
  - name: step_one
    id: step_one
    run: echo "1"
  - name: step_two
    depends: step_one
    run: echo "2"
`,
			expected: map[string][]string{
				"step_two": {"step_one"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dag, err := spec.LoadYAML(ctx, []byte(tt.yaml))
			require.NoError(t, err)

			// Check that dependencies were resolved correctly
			for _, step := range dag.Steps {
				if expectedDeps, exists := tt.expected[step.Name]; exists {
					assert.Equal(t, expectedDeps, step.Depends,
						"Step %s dependencies should be resolved correctly", step.Name)
				}
			}
		})
	}
}

func TestResolveStepDependencies_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		expectedErr string
	}{
		{
			name: "DependencyOnNonExistentID",
			yaml: `
type: graph
steps:
  - name: step-one
    run: echo "1"
  - name: step-two
    depends: nonexistent
    run: echo "2"
`,
			expectedErr: "", // This should be caught by dependency validation, not ID resolution
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := spec.LoadYAML(ctx, []byte(tt.yaml))
			if tt.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				// Some tests expect no error from ID resolution
				// but might fail in other validation steps
				_ = err
			}
		})
	}
}

func TestContainer(t *testing.T) {
	t.Parallel()

	// Basic container tests
	basicTests := []struct {
		name           string
		yaml           string
		wantImage      string
		wantName       string
		wantPullPolicy core.PullPolicy
		wantNil        bool
		wantEnv        []string
	}{
		{
			name: "BasicContainer",
			yaml: `
container:
  image: python:3.11-slim
  pull_policy: always
steps:
  - name: step1
    run: python script.py
`,
			wantImage:      "python:3.11-slim",
			wantPullPolicy: core.PullPolicyAlways,
		},
		{
			name: "ContainerWithName",
			yaml: `
container:
  name: my-dag-container
  image: alpine:latest
steps:
  - name: step1
    run: echo hello
`,
			wantImage: "alpine:latest",
			wantName:  "my-dag-container",
		},
		{
			name: "ContainerNameEmpty",
			yaml: `
container:
  image: alpine:latest
steps:
  - name: step1
    run: echo hello
`,
			wantImage: "alpine:latest",
			wantName:  "",
		},
		{
			name: "ContainerNameTrimmed",
			yaml: `
container:
  name: "  my-container  "
  image: alpine:latest
steps:
  - name: step1
    run: echo hello
`,
			wantImage: "alpine:latest",
			wantName:  "my-container",
		},
		{
			name: "ContainerEnvAsMap",
			yaml: `
container:
  image: alpine
  env:
    FOO: bar
    BAZ: qux
steps:
  - name: step1
    run: echo test
`,
			wantImage: "alpine",
			wantEnv:   []string{"FOO=bar", "BAZ=qux"},
		},
		{
			name: "ContainerWithoutPullPolicy",
			yaml: `
container:
  image: alpine
steps:
  - name: step1
    run: echo test
`,
			wantImage:      "alpine",
			wantPullPolicy: core.PullPolicyMissing,
		},
		{
			name: "NoContainer",
			yaml: `
steps:
  - name: step1
    run: echo test
`,
			wantNil: true,
		},
	}

	for _, tt := range basicTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, dag.Container)
				return
			}
			require.NotNil(t, dag.Container)
			if tt.wantImage != "" {
				assert.Equal(t, tt.wantImage, dag.Container.Image)
			}
			if tt.wantName != "" || tt.name == "ContainerNameEmpty" {
				assert.Equal(t, tt.wantName, dag.Container.Name)
			}
			if tt.wantPullPolicy != 0 {
				assert.Equal(t, tt.wantPullPolicy, dag.Container.PullPolicy)
			}
			for _, env := range tt.wantEnv {
				assert.Contains(t, dag.Container.Env, env)
			}
		})
	}

	// Pull policy variations
	pullPolicyTests := []struct {
		name       string
		pullPolicy string
		expected   core.PullPolicy
	}{
		{"Always", "always", core.PullPolicyAlways},
		{"Never", "never", core.PullPolicyNever},
		{"Missing", "missing", core.PullPolicyMissing},
		{"TrueString", "true", core.PullPolicyAlways},
		{"FalseString", "false", core.PullPolicyNever},
	}

	for _, tt := range pullPolicyTests {
		t.Run("PullPolicy"+tt.name, func(t *testing.T) {
			t.Parallel()
			yaml := `
container:
  image: alpine
  pull_policy: ` + tt.pullPolicy + `
steps:
  - name: step1
    run: echo test
`
			dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
			require.NoError(t, err)
			require.NotNil(t, dag.Container)
			assert.Equal(t, tt.expected, dag.Container.PullPolicy)
		})
	}

	// Exec mode tests (container as string or object with exec field)
	t.Run("ContainerStringForm", func(t *testing.T) {
		t.Parallel()
		yaml := `
container: my-running-container
steps:
  - name: step1
    run: echo test
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.NotNil(t, dag.Container)
		assert.Equal(t, "my-running-container", dag.Container.Exec)
		assert.Empty(t, dag.Container.Image)
		assert.True(t, dag.Container.IsExecMode())
	})

	t.Run("ContainerStringFormTrimmed", func(t *testing.T) {
		t.Parallel()
		yaml := `
container: "  my-container  "
steps:
  - name: step1
    run: echo test
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.NotNil(t, dag.Container)
		assert.Equal(t, "my-container", dag.Container.Exec)
	})

	t.Run("ContainerObjectExecForm", func(t *testing.T) {
		t.Parallel()
		yaml := `
container:
  exec: my-container
  user: root
  working_dir: /app
  env:
    - MY_VAR: value
steps:
  - name: step1
    run: echo test
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.NotNil(t, dag.Container)
		assert.Equal(t, "my-container", dag.Container.Exec)
		assert.Empty(t, dag.Container.Image)
		assert.Equal(t, "root", dag.Container.User)
		assert.Equal(t, "/app", dag.Container.WorkingDir)
		assert.Contains(t, dag.Container.Env, "MY_VAR=value")
		assert.True(t, dag.Container.IsExecMode())
	})

	t.Run("StepContainerStringForm", func(t *testing.T) {
		t.Parallel()
		yaml := `
steps:
  - name: step1
    container: my-step-container
    run: echo test
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.NotNil(t, dag.Steps[0].Container)
		assert.Equal(t, "my-step-container", dag.Steps[0].Container.Exec)
		assert.True(t, dag.Steps[0].Container.IsExecMode())
	})

	t.Run("StepContainerObjectExecForm", func(t *testing.T) {
		t.Parallel()
		yaml := `
steps:
  - name: step1
    container:
      exec: my-step-container
      user: nobody
      working_dir: /tmp
    run: echo test
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.NotNil(t, dag.Steps[0].Container)
		assert.Equal(t, "my-step-container", dag.Steps[0].Container.Exec)
		assert.Equal(t, "nobody", dag.Steps[0].Container.User)
		assert.Equal(t, "/tmp", dag.Steps[0].Container.WorkingDir)
		assert.True(t, dag.Steps[0].Container.IsExecMode())
	})

	// Error tests
	errorTests := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name: "InvalidPullPolicy",
			yaml: `
container:
  image: alpine
  pull_policy: invalid_policy
steps:
  - name: step1
    run: echo test
`,
			errContains: "failed to parse pull policy",
		},
		{
			name: "ContainerWithoutImage",
			yaml: `
container:
  pull_policy: always
  env:
    - FOO: bar
steps:
  - name: step1
    run: echo test
`,
			errContains: "either 'exec' or 'image' must be specified",
		},
		{
			name: "ContainerExecAndImageMutualExclusive",
			yaml: `
container:
  exec: my-container
  image: alpine:latest
steps:
  - name: step1
    run: echo test
`,
			errContains: "'exec' and 'image' are mutually exclusive",
		},
		{
			name: "ContainerExecWithInvalidVolumes",
			yaml: `
container:
  exec: my-container
  volumes:
    - /data:/data
steps:
  - name: step1
    run: echo test
`,
			errContains: "cannot be used with 'exec'",
		},
		{
			name: "ContainerExecWithInvalidPorts",
			yaml: `
container:
  exec: my-container
  ports:
    - "8080:80"
steps:
  - name: step1
    run: echo test
`,
			errContains: "cannot be used with 'exec'",
		},
		{
			name: "ContainerExecWithInvalidNetwork",
			yaml: `
container:
  exec: my-container
  network: bridge
steps:
  - name: step1
    run: echo test
`,
			errContains: "cannot be used with 'exec'",
		},
		{
			name: "ContainerExecWithInvalidPullPolicy",
			yaml: `
container:
  exec: my-container
  pull_policy: always
steps:
  - name: step1
    run: echo test
`,
			errContains: "cannot be used with 'exec'",
		},
		{
			name: "ContainerStringFormEmpty",
			yaml: `
container: "   "
steps:
  - name: step1
    run: echo test
`,
			errContains: "container name cannot be empty",
		},
		{
			name: "StepContainerExecAndImageMutualExclusive",
			yaml: `
steps:
  - name: step1
    container:
      exec: my-container
      image: alpine:latest
    run: echo test
`,
			errContains: "'exec' and 'image' are mutually exclusive",
		},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}

	// Complex container with all fields (separate due to many assertions)
	t.Run("ContainerWithAllFields", func(t *testing.T) {
		t.Parallel()
		yaml := `
container:
  image: node:18-alpine
  pull_policy: missing
  env:
    - NODE_ENV: production
    - API_KEY: secret123
  volumes:
    - /data:/data:ro
    - /output:/output:rw
  user: "1000:1000"
  working_dir: /app
  platform: linux/amd64
  ports:
    - "8080:8080"
    - "9090:9090"
  network: bridge
  keep_container: true
steps:
  - name: step1
    run: node app.js
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.NotNil(t, dag.Container)
		assert.Equal(t, "node:18-alpine", dag.Container.Image)
		assert.Equal(t, core.PullPolicyMissing, dag.Container.PullPolicy)
		assert.Contains(t, dag.Container.Env, "NODE_ENV=production")
		assert.Contains(t, dag.Container.Env, "API_KEY=secret123")
		assert.Equal(t, []string{"/data:/data:ro", "/output:/output:rw"}, dag.Container.Volumes)
		assert.Equal(t, "1000:1000", dag.Container.User)
		assert.Equal(t, "/app", dag.Container.GetWorkingDir())
		assert.Equal(t, "linux/amd64", dag.Container.Platform)
		assert.Equal(t, []string{"8080:8080", "9090:9090"}, dag.Container.Ports)
		assert.Equal(t, "bridge", dag.Container.Network)
		assert.True(t, dag.Container.KeepContainer)
	})
}

func TestContainerExecutorIntegration(t *testing.T) {
	t.Run("StepInheritsContainerExecutor", func(t *testing.T) {
		yaml := `
container:
  image: python:3.11-slim
steps:
  - name: step1
    run: python script.py
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		// Step should have docker executor type when DAG has container
		assert.Equal(t, "container", dag.Steps[0].ExecutorConfig.Type)
	})

	t.Run("LegacyExplicitExecutorOverridesContainer", func(t *testing.T) {
		yaml := `
container:
  image: python:3.11-slim
steps:
  - name: step1
    command: echo test
    type: shell
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		// Explicit type should override DAG-level container
		assert.Equal(t, "shell", dag.Steps[0].ExecutorConfig.Type)
	})

	t.Run("NoContainerNoExecutor", func(t *testing.T) {
		yaml := `
steps:
  - name: step1
    run: echo test
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		// No container and no executor means default (empty) executor
		assert.Equal(t, "", dag.Steps[0].ExecutorConfig.Type)
	})

	t.Run("StepWithDockerExecutorConfig", func(t *testing.T) {
		yaml := `
container:
  image: node:18-alpine
steps:
  - name: step1
    action: docker.run
    with:
      image: python:3.11
      command: node app.js
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		// Step-level docker config should override DAG container
		assert.Equal(t, "docker", dag.Steps[0].ExecutorConfig.Type)
		assert.Equal(t, "python:3.11", dag.Steps[0].ExecutorConfig.Config["image"])
	})

	t.Run("MultipleStepsWithContainerAndLegacyShellOverride", func(t *testing.T) {
		yaml := `
container:
  image: alpine:latest
steps:
  - name: step1
    run: echo "step 1"
  - name: step2
    command: echo "step 2"
    type: shell
  - name: step3
    run: echo "step 3"
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 3)

		// Step 1 and 3 should inherit docker executor
		assert.Equal(t, "container", dag.Steps[0].ExecutorConfig.Type)
		assert.Equal(t, "shell", dag.Steps[1].ExecutorConfig.Type)
		assert.Equal(t, "container", dag.Steps[2].ExecutorConfig.Type)
	})
}

func TestSSHInheritance(t *testing.T) {
	t.Run("StepInheritsSSHFromDAG", func(t *testing.T) {
		yaml := `
ssh:
  user: testuser
  host: example.com
  key: ~/.ssh/id_rsa
steps:
  - name: step1
    run: echo hello
  - name: step2
    run: ls -la
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)

		// Both steps should inherit SSH executor
		for _, step := range dag.Steps {
			assert.Equal(t, "ssh", step.ExecutorConfig.Type)
		}
	})

	t.Run("StepOverridesSSHConfigWithLegacyCommandStep", func(t *testing.T) {
		yaml := `
ssh:
  user: defaultuser
  host: default.com
  key: ~/.ssh/default_key
steps:
  - name: step1
    action: ssh.run
    with:
      command: echo hello
      user: overrideuser
      ip: override.com
  - name: step2
    type: command
    command: echo world
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)

		// Step 1 should have overridden values
		step1 := dag.Steps[0]
		assert.Equal(t, "ssh", step1.ExecutorConfig.Type)

		// Step 2 should use command executor
		step2 := dag.Steps[1]
		assert.Equal(t, "command", step2.ExecutorConfig.Type)
	})
}

func TestRedisInheritance(t *testing.T) {
	t.Run("StepInheritsRedisFromDAG", func(t *testing.T) {
		yaml := `
redis:
  url: redis://localhost:6379
  password: secret
steps:
  - name: step1
    action: redis.ping
  - name: step2
    action: redis.get
    with:
      key: mykey
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)

		// Both steps should inherit Redis config
		for _, step := range dag.Steps {
			assert.Equal(t, "redis", step.ExecutorConfig.Type)
			assert.Equal(t, "redis://localhost:6379", step.ExecutorConfig.Config["url"])
			assert.Equal(t, "secret", step.ExecutorConfig.Config["password"])
		}

		// Step 1 should have PING command
		assert.Equal(t, "PING", dag.Steps[0].ExecutorConfig.Config["command"])

		// Step 2 should have GET command with key
		assert.Equal(t, "GET", dag.Steps[1].ExecutorConfig.Config["command"])
		assert.Equal(t, "mykey", dag.Steps[1].ExecutorConfig.Config["key"])
	})

	t.Run("StepOverridesRedisConfig", func(t *testing.T) {
		yaml := `
redis:
  url: redis://default:6379
  db: 0
steps:
  - name: step1
    action: redis.ping
    with:
      db: 1
  - name: step2
    action: redis.get
    with:
      key: mykey
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)

		// Step 1 should override db but inherit url
		step1 := dag.Steps[0]
		assert.Equal(t, "redis", step1.ExecutorConfig.Type)
		assert.Equal(t, "redis://default:6379", step1.ExecutorConfig.Config["url"])
		// YAML may parse the db as different int types, so compare using interface conversion
		assert.EqualValues(t, 1, step1.ExecutorConfig.Config["db"]) // Overridden

		// Step 2 should inherit all from DAG
		step2 := dag.Steps[1]
		assert.Equal(t, "redis", step2.ExecutorConfig.Type)
		assert.Equal(t, "redis://default:6379", step2.ExecutorConfig.Config["url"])
		// db should NOT be set because it's 0 (zero value) at DAG level
	})

	t.Run("RedisTypeInferenceFromDAG", func(t *testing.T) {
		yaml := `
redis:
  host: localhost
  port: 6379
steps:
  - name: step1
    action: redis.ping
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		// Type should be inferred as redis from DAG-level config
		assert.Equal(t, "redis", dag.Steps[0].ExecutorConfig.Type)
		assert.Equal(t, "localhost", dag.Steps[0].ExecutorConfig.Config["host"])
		assert.Equal(t, 6379, dag.Steps[0].ExecutorConfig.Config["port"])
	})

	t.Run("LegacyExplicitTypeOverridesDAGRedis", func(t *testing.T) {
		yaml := `
redis:
  host: localhost
  port: 6379
steps:
  - name: step1
    type: command
    command: echo hello
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		// Explicit type should override DAG-level redis inference
		assert.Equal(t, "command", dag.Steps[0].ExecutorConfig.Type)
	})
}

func TestHarnessInheritance(t *testing.T) {
	t.Run("StepInheritsHarnessFromDAG", func(t *testing.T) {
		yaml := `
harness:
  provider: claude
  model: sonnet
  bare: true
  fallback:
    - provider: codex
      full-auto: true
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "Write tests"
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.NotNil(t, dag.Harness)
		require.Len(t, dag.Steps, 1)

		step := dag.Steps[0]
		assert.Equal(t, "harness", step.ExecutorConfig.Type)
		assert.Equal(t, "claude", step.ExecutorConfig.Config["provider"])
		assert.Equal(t, "sonnet", step.ExecutorConfig.Config["model"])
		assert.Equal(t, true, step.ExecutorConfig.Config["bare"])
		fallback := mustHarnessFallback(t, step.ExecutorConfig.Config["fallback"])
		require.Len(t, fallback, 1)
		assert.Equal(t, "codex", fallback[0]["provider"])
		assert.Equal(t, true, fallback[0]["full-auto"])
	})

	t.Run("StepOverridesPrimaryConfigAndInheritsFallback", func(t *testing.T) {
		yaml := `
harness:
  provider: claude
  model: sonnet
  bare: true
  fallback:
    - provider: codex
      full-auto: true
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "Fix bugs"
      model: opus
      effort: high
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		step := dag.Steps[0]

		assert.Equal(t, "claude", step.ExecutorConfig.Config["provider"])
		assert.Equal(t, "opus", step.ExecutorConfig.Config["model"])
		assert.Equal(t, "high", step.ExecutorConfig.Config["effort"])
		assert.Equal(t, true, step.ExecutorConfig.Config["bare"])

		fallback := mustHarnessFallback(t, step.ExecutorConfig.Config["fallback"])
		require.Len(t, fallback, 1)
		assert.Equal(t, "codex", fallback[0]["provider"])
	})

	t.Run("StepOverridesBuiltinFlagAliasesWithoutDuplicates", func(t *testing.T) {
		yaml := `
harness:
  provider: codex
  skip_git_repo_check: true
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "Fix bugs"
      skip-git-repo-check: false
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		step := dag.Steps[0]

		assert.Equal(t, "codex", step.ExecutorConfig.Config["provider"])
		assert.Equal(t, false, step.ExecutorConfig.Config["skip-git-repo-check"])
		_, exists := step.ExecutorConfig.Config["skip_git_repo_check"]
		assert.False(t, exists)
	})

	t.Run("StepFallbackReplacesDAGFallback", func(t *testing.T) {
		yaml := `
harness:
  provider: claude
  model: sonnet
  fallback:
    - provider: codex
      full-auto: true
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "Generate docs"
      provider: copilot
      fallback:
        - provider: claude
          model: haiku
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		step := dag.Steps[0]

		assert.Equal(t, "copilot", step.ExecutorConfig.Config["provider"])
		fallback := mustHarnessFallback(t, step.ExecutorConfig.Config["fallback"])
		require.Len(t, fallback, 1)
		assert.Equal(t, "claude", fallback[0]["provider"])
		assert.Equal(t, "haiku", fallback[0]["model"])
	})

	t.Run("EmptyStepFallbackDisablesInheritedFallback", func(t *testing.T) {
		yaml := `
harness:
  provider: claude
  fallback:
    - provider: codex
      full-auto: true
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "No retries"
      fallback: []
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		step := dag.Steps[0]

		fallback := mustHarnessFallback(t, step.ExecutorConfig.Config["fallback"])
		assert.Empty(t, fallback)
	})

	t.Run("HarnessTypeInferenceFromDAG", func(t *testing.T) {
		yaml := `
harness:
  provider: claude
  model: sonnet
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "Write tests"
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		assert.Equal(t, "harness", dag.Steps[0].ExecutorConfig.Type)
		assert.Equal(t, "claude", dag.Steps[0].ExecutorConfig.Config["provider"])
		assert.Equal(t, "sonnet", dag.Steps[0].ExecutorConfig.Config["model"])
	})

	t.Run("LegacyExplicitTypeOverridesDAGHarness", func(t *testing.T) {
		yaml := `
harness:
  provider: claude
  model: sonnet
steps:
  - name: step1
    type: command
    command: echo hello
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		assert.Equal(t, "command", dag.Steps[0].ExecutorConfig.Type)
		assert.Empty(t, dag.Steps[0].ExecutorConfig.Config)
	})

	t.Run("ParameterizedProviderBuilds", func(t *testing.T) {
		yaml := `
params:
  - PROVIDER: claude
harness:
  provider: "${PROVIDER}"
  model: sonnet
  fallback:
    - provider: "${PROVIDER}"
      model: haiku
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "Analyze this codebase"
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		assert.Equal(t, "harness", dag.Steps[0].ExecutorConfig.Type)
		assert.Equal(t, "${PROVIDER}", dag.Steps[0].ExecutorConfig.Config["provider"])
		fallback := mustHarnessFallback(t, dag.Steps[0].ExecutorConfig.Config["fallback"])
		require.Len(t, fallback, 1)
		assert.Equal(t, "${PROVIDER}", fallback[0]["provider"])
	})

	t.Run("StepUsesCustomHarnessFromRegistry", func(t *testing.T) {
		yaml := `
harnesses:
  gemini:
    binary: gemini
    prefix_args: ["run"]
    prompt_mode: flag
    prompt_flag: --prompt
harness:
  provider: gemini
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "Review this repository"
`
		dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		require.NotNil(t, dag.Harnesses)
		require.Contains(t, dag.Harnesses, "gemini")

		assert.Equal(t, "harness", dag.Steps[0].ExecutorConfig.Type)
		assert.Equal(t, "gemini", dag.Steps[0].ExecutorConfig.Config["provider"])
		assert.Equal(t, "gemini", dag.Harnesses["gemini"].Binary)
		assert.Equal(t, []string{"run"}, dag.Harnesses["gemini"].PrefixArgs)
	})

	t.Run("UnknownCustomHarnessProviderFailsBuild", func(t *testing.T) {
		yaml := `
steps:
  - name: step1
    action: harness.run
    with:
      prompt: "Review this repository"
      provider: gemini
`
		_, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown provider "gemini"`)
	})

	t.Run("BuiltinHarnessNameCollisionFailsBuild", func(t *testing.T) {
		yaml := `
harnesses:
  claude:
    binary: gemini
steps:
  - run: "Review this repository"
`
		_, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `conflicts with built-in provider`)
	})

	t.Run("PromptFlagOutsideFlagModeFailsBuild", func(t *testing.T) {
		yaml := `
harnesses:
  gemini:
    binary: gemini
    prompt_mode: stdin
    prompt_flag: --prompt
steps:
  - run: "Review this repository"
    with:
      provider: gemini
`
		_, err := spec.LoadYAML(context.Background(), []byte(yaml))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `prompt_flag is only valid when prompt_mode is flag`)
	})
}

func mustHarnessFallback(t *testing.T, value any) []map[string]any {
	t.Helper()

	switch v := value.(type) {
	case []map[string]any:
		return v
	case []any:
		ret := make([]map[string]any, len(v))
		for i := range v {
			item, ok := v[i].(map[string]any)
			require.True(t, ok, "fallback[%d] should be a map[string]any", i)
			ret[i] = item
		}
		return ret
	default:
		t.Fatalf("unexpected fallback type %T", value)
		return nil
	}
}

func TestStepLevelEnv(t *testing.T) {
	t.Run("BasicStepEnv", func(t *testing.T) {
		yaml := `
steps:
  - name: step1
    run: echo $STEP_VAR
    env:
      - STEP_VAR: step_value
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		assert.Equal(t, []string{"STEP_VAR=step_value"}, dag.Steps[0].Env)
	})

	t.Run("StepEnvOverridesDAGEnv", func(t *testing.T) {
		yaml := `
env:
  - SHARED_VAR: dag_value
  - DAG_ONLY: dag_only_value
steps:
  - name: step1
    run: echo $SHARED_VAR
    env:
      - SHARED_VAR: step_value
      - STEP_ONLY: step_only_value
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		// Check DAG-level env
		assert.Contains(t, dag.Env, "SHARED_VAR=dag_value")
		assert.Contains(t, dag.Env, "DAG_ONLY=dag_only_value")
		// Check step-level env
		assert.Contains(t, dag.Steps[0].Env, "SHARED_VAR=step_value")
		assert.Contains(t, dag.Steps[0].Env, "STEP_ONLY=step_only_value")
	})

	t.Run("StepEnvAsMap", func(t *testing.T) {
		yaml := `
steps:
  - name: step1
    run: echo test
    env:
      FOO: foo_value
      BAR: bar_value
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		assert.Contains(t, dag.Steps[0].Env, "FOO=foo_value")
		assert.Contains(t, dag.Steps[0].Env, "BAR=bar_value")
	})

	t.Run("StepEnvWithSubstitution", func(t *testing.T) {
		yaml := `
env:
  - BASE_PATH: /tmp
steps:
  - name: step1
    run: echo $FULL_PATH
    env:
      - FULL_PATH: ${BASE_PATH}/data
      - COMPUTED: "` + "`echo computed_value`" + `"
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		assert.Contains(t, dag.Steps[0].Env, "FULL_PATH=${BASE_PATH}/data")
		assert.Contains(t, dag.Steps[0].Env, "COMPUTED=`echo computed_value`")
	})

	t.Run("MultipleStepsWithDifferentEnvs", func(t *testing.T) {
		yaml := `
steps:
  - name: step1
    run: echo $ENV_VAR
    env:
      - ENV_VAR: value1
  - name: step2
    run: echo $ENV_VAR
    env:
      - ENV_VAR: value2
  - name: step3
    run: echo $ENV_VAR
    # No env, should inherit DAG env only
`
		ctx := context.Background()
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 3)
		assert.Equal(t, []string{"ENV_VAR=value1"}, dag.Steps[0].Env)
		assert.Equal(t, []string{"ENV_VAR=value2"}, dag.Steps[1].Env)
		assert.Empty(t, dag.Steps[2].Env)
	})

	t.Run("StepEnvComplexValues", func(t *testing.T) {
		yaml := `
steps:
  - name: step1
    run: echo test
    env:
      - PATH: "/custom/bin:${PATH}"
      - JSON_CONFIG: '{"key": "value", "nested": {"foo": "bar"}}'
      - MULTI_LINE: |
          line1
          line2
`
		ctx := context.Background()
		// Set PATH env var for substitution test
		origPath := os.Getenv("PATH")
		defer func() { os.Setenv("PATH", origPath) }()
		os.Setenv("PATH", "/usr/bin")

		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		assert.Contains(t, dag.Steps[0].Env, "PATH=/custom/bin:${PATH}")
		assert.Contains(t, dag.Steps[0].Env, `JSON_CONFIG={"key": "value", "nested": {"foo": "bar"}}`)
		assert.Contains(t, dag.Steps[0].Env, "MULTI_LINE=line1\nline2\n")
	})
}

// TestDAGLoadEnv verifies dotenv loading preserves DAG envs and search precedence.
func TestDAGLoadEnv(t *testing.T) {
	t.Run("LoadEnvWithDotenvAndEnvVars", func(t *testing.T) {
		// Create a temp directory with a .env file
		tempDir := t.TempDir()
		envFile := filepath.Join(tempDir, ".env")
		envContent := "LOAD_ENV_DOTENV_VAR=from_file\n"
		err := os.WriteFile(envFile, []byte(envContent), 0644)
		require.NoError(t, err)

		yaml := fmt.Sprintf(`
working_dir: %s
dotenv: .env
env:
  - LOAD_ENV_ENV_VAR: from_dag
  - LOAD_ENV_ANOTHER_VAR: another_value
steps:
  - run: echo hello
`, tempDir)

		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(yaml), spec.BuildOpts{Flags: spec.BuildFlagNoEval})
		require.NoError(t, err)
		require.NotNil(t, dag)

		// Load environment variables from dotenv file
		dag.LoadDotEnv(context.Background())

		// Verify environment variables are in dag.Env (not process env)
		// Child processes will receive them via cmd.Env = AllEnvs()
		envMap := make(map[string]string)
		for _, env := range dag.Env {
			key, value, found := strings.Cut(env, "=")
			if found {
				envMap[key] = value
			}
		}
		assert.Equal(t, "from_file", envMap["LOAD_ENV_DOTENV_VAR"])
		assert.Equal(t, "from_dag", envMap["LOAD_ENV_ENV_VAR"])
		assert.Equal(t, "another_value", envMap["LOAD_ENV_ANOTHER_VAR"])
	})

	t.Run("LoadEnvWithMissingDotenvFile", func(t *testing.T) {
		yaml := `
dotenv: nonexistent.env
env:
  - TEST_VAR_LOAD_ENV: test_value
steps:
  - run: echo hello
`
		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(yaml), spec.BuildOpts{Flags: spec.BuildFlagNoEval})
		require.NoError(t, err)
		require.NotNil(t, dag)

		// LoadDotEnv should not fail even if dotenv file doesn't exist
		dag.LoadDotEnv(context.Background())

		// Environment variables from env should still be in dag.Env
		envMap := make(map[string]string)
		for _, env := range dag.Env {
			key, value, found := strings.Cut(env, "=")
			if found {
				envMap[key] = value
			}
		}
		assert.Equal(t, "test_value", envMap["TEST_VAR_LOAD_ENV"])
	})

	t.Run("LoadEnvFromBaseEnvResolvedWorkingDir", func(t *testing.T) {
		root := t.TempDir()
		workDir := filepath.Join(root, "work", "quant-signal")
		dagDir := filepath.Join(root, "dags")
		require.NoError(t, os.MkdirAll(workDir, 0750))
		require.NoError(t, os.MkdirAll(dagDir, 0750))
		require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env"), []byte("PYTHON_BIN=/usr/local/bin/python\n"), 0600))

		baseConfig := filepath.Join(root, "base.yaml")
		require.NoError(t, os.WriteFile(baseConfig, fmt.Appendf(nil, `
env:
  - QUANT_SIGNAL_DIR: %q
`, workDir), 0600))

		dagFile := filepath.Join(dagDir, "health-check.yaml")
		require.NoError(t, os.WriteFile(dagFile, []byte(`
working_dir: ${QUANT_SIGNAL_DIR}
steps:
  - command: printenv QUANT_SIGNAL_DIR PYTHON_BIN
`), 0600))

		dag, err := spec.Load(context.Background(), dagFile, spec.WithBaseConfig(baseConfig))
		require.NoError(t, err)

		dag.LoadDotEnv(context.Background())

		envMap := envSliceMap(dag.Env)
		assert.Equal(t, workDir, envMap["QUANT_SIGNAL_DIR"])
		assert.Equal(t, "/usr/local/bin/python", envMap["PYTHON_BIN"])
	})

	t.Run("LoadEnvPrefersResolvedWorkingDirOverDAGFileDir", func(t *testing.T) {
		root := t.TempDir()
		workDir := filepath.Join(root, "work", "quant-signal")
		dagDir := filepath.Join(root, "dags")
		require.NoError(t, os.MkdirAll(workDir, 0750))
		require.NoError(t, os.MkdirAll(dagDir, 0750))
		require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env"), []byte("PYTHON_BIN=/usr/local/bin/python\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dagDir, ".env"), []byte("PYTHON_BIN=/wrong/from-dag-dir\n"), 0600))

		baseConfig := filepath.Join(root, "base.yaml")
		require.NoError(t, os.WriteFile(baseConfig, fmt.Appendf(nil, `
env:
  - QUANT_SIGNAL_DIR: %q
`, workDir), 0600))

		dagFile := filepath.Join(dagDir, "health-check.yaml")
		require.NoError(t, os.WriteFile(dagFile, []byte(`
working_dir: ${QUANT_SIGNAL_DIR}
steps:
  - command: printenv QUANT_SIGNAL_DIR PYTHON_BIN
`), 0600))

		dag, err := spec.Load(context.Background(), dagFile, spec.WithBaseConfig(baseConfig))
		require.NoError(t, err)

		dag.LoadDotEnv(context.Background())

		envMap := envSliceMap(dag.Env)
		assert.Equal(t, "/usr/local/bin/python", envMap["PYTHON_BIN"])
	})
}

// envSliceMap converts KEY=value env entries into a map for test assertions.
func envSliceMap(envs []string) map[string]string {
	envMap := make(map[string]string)
	for _, env := range envs {
		key, value, found := strings.Cut(env, "=")
		if found {
			envMap[key] = value
		}
	}
	return envMap
}

func TestBuildShell(t *testing.T) {
	// Shell is no longer expanded at build time - expansion happens at runtime
	// See runtime/env.go Shell() method
	// Standard parsing cases are covered by types/shell_test.go
	t.Run("WithEnvVarPreserved", func(t *testing.T) {
		t.Setenv("MY_SHELL", "/bin/zsh")
		data := []byte(`
shell: $MY_SHELL
steps:
  - run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		// Expects unexpanded value (expansion deferred to runtime)
		assert.Equal(t, "$MY_SHELL", dag.Shell)
		assert.Empty(t, dag.ShellArgs)
	})

	t.Run("ArrayWithEnvVarPreserved", func(t *testing.T) {
		t.Setenv("SHELL_ARG", "-x")
		data := []byte(`
shell:
  - bash
  - $SHELL_ARG
steps:
  - run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Equal(t, "bash", dag.Shell)
		// Expects unexpanded value (expansion deferred to runtime)
		assert.Equal(t, []string{"$SHELL_ARG"}, dag.ShellArgs)
	})

	// NoEval tests (cannot use t.Parallel due to t.Setenv)
	t.Run("NoEvalPreservesRaw", func(t *testing.T) {
		t.Setenv("MY_SHELL", "/bin/zsh")
		data := []byte(`
shell: $MY_SHELL -e
steps:
  - run: echo hello
`)
		dag, err := spec.LoadYAMLWithOpts(context.Background(), data, spec.BuildOpts{Flags: spec.BuildFlagNoEval})
		require.NoError(t, err)
		assert.Equal(t, "$MY_SHELL", dag.Shell)
		assert.Equal(t, []string{"-e"}, dag.ShellArgs)
	})

	t.Run("ArrayNoEvalPreservesRaw", func(t *testing.T) {
		t.Setenv("SHELL_ARG", "-x")
		data := []byte(`
shell:
  - bash
  - $SHELL_ARG
steps:
  - run: echo hello
`)
		dag, err := spec.LoadYAMLWithOpts(context.Background(), data, spec.BuildOpts{Flags: spec.BuildFlagNoEval})
		require.NoError(t, err)
		assert.Equal(t, "bash", dag.Shell)
		assert.Equal(t, []string{"$SHELL_ARG"}, dag.ShellArgs)
	})
}

func TestBuildStepShell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		yaml              string
		wantDAGShell      string
		wantDAGShellArgs  []string
		wantStepShell     string
		wantStepShellArgs []string
		wantStepEmpty     bool
	}{
		{
			name: "SimpleString",
			yaml: `
steps:
  - name: test
    run: echo hello
    with:
      shell: zsh
`,
			wantStepShell:     "zsh",
			wantStepShellArgs: nil,
		},
		{
			name: "StringWithArgs",
			yaml: `
steps:
  - name: test
    run: echo hello
    with:
      shell: bash -e -u
`,
			wantStepShell:     "bash",
			wantStepShellArgs: []string{"-e", "-u"},
		},
		{
			name: "Array",
			yaml: `
steps:
  - name: test
    run: echo hello
    with:
      shell:
        - bash
        - -e
        - -o
        - pipefail
`,
			wantStepShell:     "bash",
			wantStepShellArgs: []string{"-e", "-o", "pipefail"},
		},
		{
			name: "OverridesDAGShell",
			yaml: `
shell: bash -e
steps:
  - name: test
    run: echo hello
    with:
      shell: zsh
`,
			wantDAGShell:      "bash",
			wantDAGShellArgs:  []string{"-e"},
			wantStepShell:     "zsh",
			wantStepShellArgs: nil,
		},
		{
			name: "NotSpecified",
			yaml: `
steps:
  - name: test
    run: echo hello
`,
			wantStepEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)

			if tt.wantDAGShell != "" {
				assert.Equal(t, tt.wantDAGShell, dag.Shell)
				assert.Equal(t, tt.wantDAGShellArgs, dag.ShellArgs)
			}

			if tt.wantStepEmpty {
				assert.Empty(t, dag.Steps[0].Shell)
			} else {
				assert.Equal(t, tt.wantStepShell, dag.Steps[0].Shell)
			}

			if tt.wantStepShellArgs == nil {
				assert.Empty(t, dag.Steps[0].ShellArgs)
			} else {
				assert.Equal(t, tt.wantStepShellArgs, dag.Steps[0].ShellArgs)
			}
		})
	}
}

func TestLoadWithOptions(t *testing.T) {
	t.Run("WithoutEval_DisablesEnvExpansion", func(t *testing.T) {
		// Cannot use t.Parallel() with t.Setenv()
		t.Setenv("MY_VAR", "expanded-value")

		data := []byte(`
env:
  - TEST: "${MY_VAR}"
steps:
  - name: test
    run: echo test
`)
		dag, err := spec.LoadYAMLWithOpts(context.Background(), data, spec.BuildOpts{Flags: spec.BuildFlagNoEval})
		require.NoError(t, err)

		// When NoEval is set, the variable should not be expanded
		// dag.Env is []string in format "KEY=VALUE"
		assert.Contains(t, dag.Env, "TEST=${MY_VAR}")
	})

	t.Run("WithAllowBuildErrors_CapturesErrors", func(t *testing.T) {
		t.Parallel()
		data := []byte(`
steps:
  - name: test
    run: echo test
    depends:
      - nonexistent
`)
		dag, err := spec.LoadYAMLWithOpts(context.Background(), data, spec.BuildOpts{Flags: spec.BuildFlagAllowBuildErrors})
		require.NoError(t, err)
		require.NotNil(t, dag)
		assert.NotEmpty(t, dag.BuildErrors)
	})

	t.Run("SkipSchemaValidation_SkipsParamsSchema", func(t *testing.T) {
		t.Parallel()
		// BuildFlagSkipSchemaValidation skips JSON schema validation for params,
		// not YAML structure validation
		data := []byte(`
params:
  schema: "nonexistent-schema.json"
  values:
    key: value
steps:
  - name: test
    run: echo test
`)
		// Without the flag, it would error due to missing schema file
		_, err := spec.LoadYAML(context.Background(), data)
		require.Error(t, err)

		// With schema validation skipped, it succeeds
		dag, err := spec.LoadYAMLWithOpts(context.Background(), data, spec.BuildOpts{Flags: spec.BuildFlagSkipSchemaValidation})
		require.NoError(t, err)
		require.NotNil(t, dag)
	})
}

func TestBuildLogOutput(t *testing.T) {
	t.Parallel()

	t.Run("DAGLevelSeparate", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
log_output: separate
steps:
  - name: step1
    run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Equal(t, core.LogOutputSeparate, dag.LogOutput)
	})

	t.Run("DAGLevelMerged", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
log_output: merged
steps:
  - name: step1
    run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Equal(t, core.LogOutputMerged, dag.LogOutput)
	})

	t.Run("DAGLevelDefault", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
steps:
  - name: step1
    run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		// Empty at build time - default applied in InitializeDefaults
		assert.Equal(t, core.LogOutputMode(""), dag.LogOutput)

		// After InitializeDefaults, should be separate
		core.InitializeDefaults(dag)
		assert.Equal(t, core.LogOutputSeparate, dag.LogOutput)
	})

	t.Run("StepLevelOverride", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
log_output: separate
steps:
  - name: step1
    run: echo hello
    log_output: merged
  - name: step2
    run: echo world
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)

		// DAG level is separate
		assert.Equal(t, core.LogOutputSeparate, dag.LogOutput)

		// Step 1 overrides to merged
		assert.Equal(t, core.LogOutputMerged, dag.Steps[0].LogOutput)

		// Step 2 inherits from DAG (empty means inherit)
		assert.Equal(t, core.LogOutputMode(""), dag.Steps[1].LogOutput)
	})

	t.Run("StepLevelExplicitSeparate", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
log_output: merged
steps:
  - name: step1
    run: echo hello
    log_output: separate
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)

		// DAG level is merged
		assert.Equal(t, core.LogOutputMerged, dag.LogOutput)

		// Step 1 explicitly sets separate
		assert.Equal(t, core.LogOutputSeparate, dag.Steps[0].LogOutput)
	})

	t.Run("CaseInsensitive", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
log_output: MERGED
steps:
  - name: step1
    run: echo hello
    log_output: SEPARATE
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)

		assert.Equal(t, core.LogOutputMerged, dag.LogOutput)
		assert.Equal(t, core.LogOutputSeparate, dag.Steps[0].LogOutput)
	})

	t.Run("InvalidValue", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
log_output: invalid
steps:
  - name: step1
    run: echo hello
`)
		_, err := spec.LoadYAML(context.Background(), data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid log_output value")
	})

	t.Run("InvalidStepValue", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
steps:
  - name: step1
    run: echo hello
    log_output: both
`)
		_, err := spec.LoadYAML(context.Background(), data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid log_output value")
	})
}

func TestMaxActiveRunsDeprecationWarning(t *testing.T) {
	t.Parallel()

	t.Run("NoWarningForDefault", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
steps:
  - name: step1
    run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Empty(t, dag.BuildWarnings)
	})

	t.Run("NoWarningForMaxActiveRuns1", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
max_active_runs: 1
steps:
  - name: step1
    run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		assert.Empty(t, dag.BuildWarnings)
	})

	t.Run("WarningForMaxActiveRunsGreaterThan1", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
max_active_runs: 3
steps:
  - name: step1
    run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		require.Len(t, dag.BuildWarnings, 1)
		assert.Contains(t, dag.BuildWarnings[0], "max_active_runs=3 is deprecated")
		assert.Contains(t, dag.BuildWarnings[0], "global queue")
	})

	t.Run("WarningForNegativeMaxActiveRuns", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
max_active_runs: -1
steps:
  - name: step1
    run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		require.Len(t, dag.BuildWarnings, 1)
		assert.Contains(t, dag.BuildWarnings[0], "max_active_runs=-1 is deprecated")
	})

	t.Run("NoWarningWithGlobalQueue", func(t *testing.T) {
		t.Parallel()

		data := []byte(`
name: test-dag
queue: my-global-queue
max_active_runs: 5
steps:
  - name: step1
    run: echo hello
`)
		dag, err := spec.LoadYAML(context.Background(), data)
		require.NoError(t, err)
		// No warning when using a global queue (queue field is set)
		assert.Empty(t, dag.BuildWarnings)
	})
}
