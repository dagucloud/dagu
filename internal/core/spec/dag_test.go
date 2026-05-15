// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBuildContext creates a BuildContext for testing
func testBuildContext() BuildContext {
	return BuildContext{
		file:  "/test/dag.yaml",
		opts:  BuildOpts{},
		index: 0,
	}
}

func testBuildContextWithOpts(opts BuildOpts) BuildContext {
	ctx := testBuildContext()
	ctx.opts = opts
	return ctx
}

func TestBuildContextWithOpts_InvalidatesParamsState(t *testing.T) {
	t.Parallel()

	cached := &paramsState{
		cached: true,
		result: &paramsResult{Params: []string{"name=old"}},
	}
	ctx := BuildContext{
		opts:        BuildOpts{Parameters: "name=old"},
		paramsState: cached,
	}

	next := ctx.WithOpts(BuildOpts{Parameters: "name=new"})

	require.Nil(t, next.paramsState)
	require.Same(t, cached, ctx.paramsState)
}

func TestLoadDAGToolsAqua(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
tools:
  packages:
    - name: jq
      package: jqlang/jq
      version: jq-1.7.1
      commands: [jq]
steps:
  - id: check
    run: jq --version
`))

	require.NoError(t, err)
	require.NotNil(t, dag.Tools)
	assert.Equal(t, "aqua", dag.Tools.Provider)
	require.NotNil(t, dag.Tools.Registry)
	assert.Equal(t, "standard", dag.Tools.Registry.Name)
	assert.Equal(t, "standard", dag.Tools.Registry.Type)
	assert.Equal(t, core.DefaultAquaStandardRegistryRef, dag.Tools.Registry.Ref)
	assert.Regexp(t, `^[0-9a-f]{40}$`, dag.Tools.Registry.Ref)
	require.Len(t, dag.Tools.Packages, 1)
	assert.Equal(t, "jq", dag.Tools.Packages[0].Name)
	assert.Equal(t, "jqlang/jq", dag.Tools.Packages[0].Package)
	assert.Equal(t, "jq-1.7.1", dag.Tools.Packages[0].Version)
	assert.Equal(t, []string{"jq"}, dag.Tools.Packages[0].Commands)
}

func TestLoadDAGToolsShorthandList(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
tools:
  - jqlang/jq@jq-1.7.1
  - google/pprof@d04f2422c8a17569c14e84da0fae252d9529826b
steps:
  - id: check
    run: jq --version
`))

	require.NoError(t, err)
	require.NotNil(t, dag.Tools)
	assert.Equal(t, "aqua", dag.Tools.Provider)
	require.NotNil(t, dag.Tools.Registry)
	assert.Equal(t, core.DefaultAquaStandardRegistryRef, dag.Tools.Registry.Ref)
	require.Len(t, dag.Tools.Packages, 2)
	assert.Equal(t, "jq", dag.Tools.Packages[0].Name)
	assert.Equal(t, "jqlang/jq", dag.Tools.Packages[0].Package)
	assert.Equal(t, "jq-1.7.1", dag.Tools.Packages[0].Version)
	assert.Empty(t, dag.Tools.Packages[0].Commands)
	assert.Equal(t, "pprof", dag.Tools.Packages[1].Name)
	assert.Equal(t, "google/pprof", dag.Tools.Packages[1].Package)
	assert.Equal(t, "d04f2422c8a17569c14e84da0fae252d9529826b", dag.Tools.Packages[1].Version)
	assert.Empty(t, dag.Tools.Packages[1].Commands)
}

func TestLoadDAGToolsPackagesAcceptMixedShorthandAndObject(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
tools:
  packages:
    - jqlang/jq@jq-1.7.1
    - package: google/pprof
      version: d04f2422c8a17569c14e84da0fae252d9529826b
steps:
  - id: check
    run: jq --version
`))

	require.NoError(t, err)
	require.NotNil(t, dag.Tools)
	require.Len(t, dag.Tools.Packages, 2)
	assert.Equal(t, "jqlang/jq", dag.Tools.Packages[0].Package)
	assert.Equal(t, "jq-1.7.1", dag.Tools.Packages[0].Version)
	assert.Equal(t, "google/pprof", dag.Tools.Packages[1].Package)
	assert.Equal(t, "d04f2422c8a17569c14e84da0fae252d9529826b", dag.Tools.Packages[1].Version)
}

func TestLoadDAGToolsRejectsInvalidShorthand(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
tools:
  - jqlang/jq
steps:
  - id: check
    run: jq --version
`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), `tool package shorthand must be "package@version"`)
}

func TestLoadDAGToolsAcceptsPackageCommitSHA(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
tools:
  packages:
    - package: google/pprof
      version: d04f2422c8a17569c14e84da0fae252d9529826b
steps:
  - id: check
    run: pprof --help
`))

	require.NoError(t, err)
	require.NotNil(t, dag.Tools)
	require.Len(t, dag.Tools.Packages, 1)
	assert.Equal(t, "aqua", dag.Tools.Provider)
	assert.Equal(t, "d04f2422c8a17569c14e84da0fae252d9529826b", dag.Tools.Packages[0].Version)
	assert.Empty(t, dag.Tools.Packages[0].Commands)
}

func TestLoadDAGToolsAcceptsOmittedCommands(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
tools:
  provider: aqua
  registry:
    type: standard
    ref: v4.233.0
  packages:
    - name: jq
      package: jqlang/jq
      version: jq-1.7.1
steps:
  - id: check
    run: jq
`))

	require.NoError(t, err)
	require.NotNil(t, dag.Tools)
	require.Len(t, dag.Tools.Packages, 1)
	assert.Equal(t, "jq", dag.Tools.Packages[0].Name)
	assert.Empty(t, dag.Tools.Packages[0].Commands)
}

func TestLoadDAGToolsRejectsMissingGitHubContentRegistryRef(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
tools:
  provider: aqua
  registry:
    type: github_content
    repo_owner: example
    repo_name: aqua-registry
    path: registry.yaml
  packages:
    - name: jq
      package: jqlang/jq
      version: jq-1.7.1
      commands: [jq]
steps:
  - id: check
    run: jq
`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry.ref is required")
}

func TestLoadDAGToolsRejectsFloatingLatestVersion(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
tools:
  provider: aqua
  registry:
    ref: v4.233.0
  packages:
    - package: jqlang/jq
      version: LATEST
      commands: [jq]
steps:
  - id: check
    run: jq
`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), `version must be pinned, got "LATEST"`)
}

func TestLoadDAGToolsRejectsCommandPath(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
tools:
  provider: aqua
  registry:
    ref: v4.233.0
  packages:
    - package: jqlang/jq
      version: jq-1.7.1
      commands: [bin/jq]
steps:
  - id: check
    run: jq
`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), `command "bin/jq" must be an executable name`)
}

func TestLoadDAGToolsRejectsShellFragmentCommand(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
tools:
  packages:
    - package: jqlang/jq
      version: jq-1.7.1
      commands: ["jq;echo"]
steps:
  - id: check
    run: jq
`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), `command "jq;echo" must be an executable name`)
}

// Helper to create PortValue from string
func portValue(s string) types.PortValue {
	var p types.PortValue
	_ = yaml.Unmarshal([]byte(s), &p)
	return p
}

// Helper to create StringOrArray from single string
func stringOrArray(s string) types.StringOrArray {
	var v types.StringOrArray
	_ = yaml.Unmarshal([]byte(`"`+s+`"`), &v)
	return v
}

// Helper to create StringOrArray from list
func stringOrArrayList(ss []string) types.StringOrArray {
	var v types.StringOrArray
	data, _ := yaml.Marshal(ss)
	_ = yaml.Unmarshal(data, &v)
	return v
}

// Helper to create LabelsValue from single string
func labelsValue(s string) types.LabelsValue {
	var v types.LabelsValue
	_ = yaml.Unmarshal([]byte(`"`+s+`"`), &v)
	return v
}

func TestBuildParamsJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  BuildContext
		dag  *dag
		want string
	}{
		{
			name: "DefaultsOnly",
			ctx:  testBuildContext(),
			dag:  &dag{Params: "FOO=bar BAZ=qux"},
			want: `{"FOO":"bar","BAZ":"qux"}`,
		},
		{
			name: "OverridesMergedAndSerialized",
			ctx:  testBuildContextWithOpts(BuildOpts{Parameters: "FOO=baz COUNT=2"}),
			dag:  &dag{Params: "FOO=bar COUNT=1"},
			want: `{"FOO":"baz","COUNT":"2"}`,
		},
		{
			name: "PreservesRawJSONInput",
			ctx:  testBuildContextWithOpts(BuildOpts{Parameters: `{"alpha":"one","beta":2}`}),
			dag:  &dag{},
			want: `{"alpha":"one","beta":2}`,
		},
		{
			name: "NoParamsProducesEmptyString",
			ctx:  testBuildContext(),
			dag:  &dag{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := buildParamsJSON(tt.ctx, tt.dag)
			require.NoError(t, err)

			if tt.want == "" {
				assert.Empty(t, result)
				return
			}

			assert.JSONEq(t, tt.want, result)
		})
	}
}

func TestBuildParamsJSON_JSONOverrideSkipsEval(t *testing.T) {
	t.Parallel()

	// JSON-formatted override params should skip eval (BuildFlagNoEval).
	// Values containing backticks should NOT be executed.
	ctx := testBuildContextWithOpts(BuildOpts{
		Parameters: `[{"topic":"` + "`echo pwned`" + `"}]`,
	})
	d := &dag{Params: `topic="default"`}

	result, err := buildParamsJSON(ctx, d)
	require.NoError(t, err)

	// The backtick value should be preserved as-is, not executed.
	assert.Contains(t, result, "`echo pwned`")
}

func TestBuildType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "EmptyDefaultsToChain",
			input:    "",
			expected: core.TypeChain,
		},
		{
			name:     "WhitespaceDefaultsToChain",
			input:    "  ",
			expected: core.TypeChain,
		},
		{
			name:     "GraphType",
			input:    "graph",
			expected: core.TypeGraph,
		},
		{
			name:     "ChainType",
			input:    "chain",
			expected: core.TypeChain,
		},
		{
			name:    "AgentTypeNotSupported",
			input:   "agent",
			wantErr: true,
		},
		{
			name:    "InvalidType",
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{Type: tt.input}
			result, err := buildType(testBuildContext(), d)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dag      *dag
		ctx      BuildContext
		expected string
	}{
		{
			name:     "FromDAGName",
			dag:      &dag{Name: "  my-dag  "},
			ctx:      testBuildContext(),
			expected: "my-dag",
		},
		{
			name: "FromOptionsNameOverridesDAGName",
			dag:  &dag{Name: "dag-name"},
			ctx: BuildContext{
				file:  "/test/dag.yaml",
				opts:  BuildOpts{Name: "override-name"},
				index: 0,
			},
			expected: "override-name",
		},
		{
			name:     "FallbackToFilenameForIndex0",
			dag:      &dag{},
			ctx:      BuildContext{file: "/path/to/my-workflow.yaml", index: 0},
			expected: "my-workflow",
		},
		{
			name:     "NoFallbackForIndexGreaterThan0",
			dag:      &dag{},
			ctx:      BuildContext{file: "/path/to/my-workflow.yaml", index: 1},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildName(tt.ctx, tt.dag)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimpleGroup", input: "my-group", expected: "my-group"},
		{name: "Trimmed", input: "  group  ", expected: "group"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{Group: tt.input}
			result, err := buildGroup(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "SimpleDescription", input: "My DAG description", expected: "My DAG description"},
		{name: "Trimmed", input: "  description  ", expected: "description"},
		{name: "Empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{Description: tt.input}
			result, err := buildDescription(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    int
		expected time.Duration
	}{
		{name: "Zero", input: 0, expected: 0},
		{name: "TenSeconds", input: 10, expected: 10 * time.Second},
		{name: "OneHour", input: 3600, expected: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{TimeoutSec: tt.input}
			result, err := buildTimeout(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    int
		expected time.Duration
	}{
		{name: "Zero", input: 0, expected: 0},
		{name: "FiveSeconds", input: 5, expected: 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{DelaySec: tt.input}
			result, err := buildDelay(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildRestartWait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    int
		expected time.Duration
	}{
		{name: "Zero", input: 0, expected: 0},
		{name: "ThirtySeconds", input: 30, expected: 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{RestartWaitSec: tt.input}
			result, err := buildRestartWait(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		labels   types.LabelsValue
		expected core.Labels
	}{
		{
			name:     "NilLabels",
			labels:   types.LabelsValue{},
			expected: nil,
		},
		{
			name:     "CommaSeparated",
			labels:   labelsValue("daily,weekly"),
			expected: core.Labels{{Key: "daily"}, {Key: "weekly"}},
		},
		{
			name:     "NormalizedToLowercase",
			labels:   labelsValue("Daily,WEEKLY"),
			expected: core.Labels{{Key: "daily"}, {Key: "weekly"}},
		},
		{
			name:     "TrimmedWhitespace",
			labels:   labelsValue("label1, label2"),
			expected: core.Labels{{Key: "label1"}, {Key: "label2"}},
		},
		{
			name:     "KeyValueLabels",
			labels:   labelsValue("env=prod team=platform"),
			expected: core.Labels{{Key: "env", Value: "prod"}, {Key: "team", Value: "platform"}},
		},
		{
			name:     "MixedLabels",
			labels:   labelsValue("env=prod,critical"),
			expected: core.Labels{{Key: "env", Value: "prod"}, {Key: "critical"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{Labels: tt.labels}
			result, err := buildLabels(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadYAMLLabels(t *testing.T) {
	t.Parallel()

	d, err := LoadYAML(context.Background(), []byte(`
labels:
  env: prod
  team: platform
steps:
  - name: step
    run: echo ok
`), WithoutEval())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"env=prod", "team=platform"}, d.Labels.Strings())
}

func TestLoadYAMLDeprecatedTags(t *testing.T) {
	t.Parallel()

	d, err := LoadYAML(context.Background(), []byte(`
tags:
  - env=prod
  - team=platform
steps:
  - name: step
    run: echo ok
`), WithoutEval())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"env=prod", "team=platform"}, d.Labels.Strings())
}

func TestLoadYAMLLabelsAndDeprecatedTagsRejected(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
labels:
  - env=prod
tags:
  - team=platform
steps:
  - name: step
    run: echo ok
`), WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "labels and deprecated tags cannot both be set")
}

func TestBuildMaxActiveRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{name: "ZeroDefaultsTo1", input: 0, expected: 1},
		{name: "CustomValue", input: 5, expected: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{MaxActiveRuns: tt.input}
			result, err := buildMaxActiveRuns(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildMaxActiveSteps(t *testing.T) {
	t.Parallel()

	d := &dag{MaxActiveSteps: 3}
	result, err := buildMaxActiveSteps(testBuildContext(), d)
	require.NoError(t, err)
	assert.Equal(t, 3, result)
}

func TestBuildQueue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "Empty", input: "", expected: ""},
		{name: "Simple", input: "my-queue", expected: "my-queue"},
		{name: "Trimmed", input: "  queue  ", expected: "queue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{Queue: tt.input}
			result, err := buildQueue(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildMaxOutputSize(t *testing.T) {
	t.Parallel()

	d := &dag{MaxOutputSize: 524288}
	result, err := buildMaxOutputSize(testBuildContext(), d)
	require.NoError(t, err)
	assert.Equal(t, 524288, result)
}

func TestBuildSkipIfSuccessful(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    bool
		expected bool
	}{
		{name: "False", input: false, expected: false},
		{name: "True", input: true, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{SkipIfSuccessful: tt.input}
			result, err := buildSkipIfSuccessful(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildLogDir(t *testing.T) {
	t.Parallel()

	d := &dag{LogDir: "/var/log/dagu"}
	result, err := buildLogDir(testBuildContext(), d)
	require.NoError(t, err)
	assert.Equal(t, "/var/log/dagu", result)
}

func TestBuildMailOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *mailOn
		expected *core.MailOn
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "BothTrue",
			input:    &mailOn{Failure: true, Success: true},
			expected: &core.MailOn{Failure: true, Success: true},
		},
		{
			name:     "FailureOnly",
			input:    &mailOn{Failure: true, Success: false},
			expected: &core.MailOn{Failure: true, Success: false},
		},
		{
			name:     "WaitOnly",
			input:    &mailOn{Wait: true},
			expected: &core.MailOn{Wait: true},
		},
		{
			name:     "AllTrue",
			input:    &mailOn{Failure: true, Success: true, Wait: true},
			expected: &core.MailOn{Failure: true, Success: true, Wait: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{MailOn: tt.input}
			result, err := buildMailOn(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildRunConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *runConfig
		expected *core.RunConfig
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "BothDisabled",
			input:    &runConfig{DisableParamEdit: true, DisableRunIdEdit: true},
			expected: &core.RunConfig{DisableParamEdit: true, DisableRunIdEdit: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{RunConfig: tt.input}
			result, err := buildRunConfig(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildHistRetentionDays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *int
		expected int
	}{
		{name: "NilDefaultsTo0", input: nil, expected: 0},
		{name: "CustomValue", input: new(365), expected: 365},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{HistRetentionDays: tt.input}
			result, err := buildHistRetentionDays(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildHistRetentionRuns(t *testing.T) {
	t.Parallel()

	runs := 3
	zero := 0
	neg := -1
	tests := []struct {
		name        string
		input       *int
		expected    int
		errContains string
	}{
		{name: "NilDefaultsTo0", input: nil, expected: 0},
		{name: "CustomValue", input: &runs, expected: 3},
		{name: "ZeroValueInvalid", input: &zero, errContains: "hist_retention_runs must be > 0"},
		{name: "NegativeValueInvalid", input: &neg, errContains: "hist_retention_runs must be > 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{HistRetentionRuns: tt.input}
			result, err := buildHistRetentionRuns(testBuildContext(), d)
			if tt.errContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildMaxCleanUpTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *int
		expected time.Duration
	}{
		{name: "NilDefaultsTo0", input: nil, expected: 0},
		{name: "TenSeconds", input: new(10), expected: 10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{MaxCleanUpTimeSec: tt.input}
			result, err := buildMaxCleanUpTime(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildWorkerSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       any
		expected    map[string]string
		forceLocal  bool
		expectErr   bool
		errContains string
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "EmptyMap",
			input:    map[string]string{},
			expected: nil,
		},
		{
			name:     "WithValues",
			input:    map[string]string{"region": "us-west", "type": "gpu"},
			expected: map[string]string{"region": "us-west", "type": "gpu"},
		},
		{
			name:     "TrimmedWhitespace",
			input:    map[string]string{" key ": " value "},
			expected: map[string]string{"key": "value"},
		},
		{
			name:       "StringLocal",
			input:      "local",
			forceLocal: true,
		},
		{
			name:       "StringLocalUpperCase",
			input:      "LOCAL",
			forceLocal: true,
		},
		{
			name:       "StringLocalMixedCase",
			input:      "Local",
			forceLocal: true,
		},
		{
			name:       "StringLocalWithSpaces",
			input:      "  local  ",
			forceLocal: true,
		},
		{
			name:        "UnsupportedString",
			input:       "remote",
			expectErr:   true,
			errContains: "the only allowed string value is \"local\"",
		},
		{
			name:     "MapStringAny",
			input:    map[string]any{"env": "prod"},
			expected: map[string]string{"env": "prod"},
		},
		{
			name:     "MapStringAnyBoolValue",
			input:    map[string]any{"gpu": true},
			expected: map[string]string{"gpu": "true"},
		},
		{
			name:     "MapStringAnyIntValue",
			input:    map[string]any{"memory": 64},
			expected: map[string]string{"memory": "64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{WorkerSelector: tt.input}
			result, forceLocal, err := buildWorkerSelector(testBuildContext(), d)
			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
			assert.Equal(t, tt.forceLocal, forceLocal)
		})
	}
}

func TestBuildWebhookConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *webhookConfig
		expected *core.WebhookConfig
		wantErr  string
	}{
		{
			name:     "NilReturnsNil",
			input:    nil,
			expected: nil,
		},
		{
			name: "NormalizesAndTrimsHeaderNames",
			input: &webhookConfig{
				ForwardHeaders: []string{" X-GitHub-Event ", "Stripe-Idempotency-Key"},
			},
			expected: &core.WebhookConfig{
				ForwardHeaders: []string{"x-github-event", "stripe-idempotency-key"},
			},
		},
		{
			name: "RejectsBlankHeaderName",
			input: &webhookConfig{
				ForwardHeaders: []string{"x-github-event", "  "},
			},
			wantErr: "forward_headers[1]",
		},
		{
			name: "RejectsAuthorizationHeader",
			input: &webhookConfig{
				ForwardHeaders: []string{"Authorization"},
			},
			wantErr: "authorization",
		},
		{
			name: "RejectsInvalidHeaderToken",
			input: &webhookConfig{
				ForwardHeaders: []string{"my header"},
			},
			wantErr: "invalid HTTP header name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := &dag{Webhook: tt.input}
			result, err := buildWebhookConfig(testBuildContext(), d)

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

func TestBuildSSH(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     *ssh
		expected  *core.SSHConfig
		expectErr bool
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name: "BasicConfig",
			input: &ssh{
				User: "admin",
				Host: "server.example.com",
				Key:  "/path/to/key",
			},
			expected: &core.SSHConfig{
				User:          "admin",
				Host:          "server.example.com",
				Port:          "22",
				Key:           "/path/to/key",
				StrictHostKey: true,
			},
		},
		{
			name: "WithCustomPort",
			input: &ssh{
				User: "admin",
				Host: "server.example.com",
				Port: portValue("2222"),
			},
			expected: &core.SSHConfig{
				User:          "admin",
				Host:          "server.example.com",
				Port:          "2222",
				StrictHostKey: true,
			},
		},
		{
			name: "StrictHostKeyDisabled",
			input: &ssh{
				User:          "admin",
				Host:          "server.example.com",
				StrictHostKey: new(false),
			},
			expected: &core.SSHConfig{
				User:          "admin",
				Host:          "server.example.com",
				Port:          "22",
				StrictHostKey: false,
			},
		},
		{
			name: "ShellStringWithArgs",
			input: &ssh{
				User:  "admin",
				Host:  "server.example.com",
				Shell: shellValue("/bin/bash -e"),
			},
			expected: &core.SSHConfig{
				User:          "admin",
				Host:          "server.example.com",
				Port:          "22",
				StrictHostKey: true,
				Shell:         "/bin/bash",
				ShellArgs:     []string{"-e"},
			},
		},
		{
			name: "ShellArrayWithArgs",
			input: &ssh{
				User:  "admin",
				Host:  "server.example.com",
				Shell: shellValueArray([]string{"/bin/bash", "-e", "-o", "pipefail"}),
			},
			expected: &core.SSHConfig{
				User:          "admin",
				Host:          "server.example.com",
				Port:          "22",
				StrictHostKey: true,
				Shell:         "/bin/bash",
				ShellArgs:     []string{"-e", "-o", "pipefail"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{SSH: tt.input}
			result, err := buildSSH(testBuildContext(), d)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildS3(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *s3Config
		expected *core.S3Config
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name: "BasicConfig",
			input: &s3Config{
				Region: "us-east-1",
				Bucket: "my-bucket",
			},
			expected: &core.S3Config{
				Region: "us-east-1",
				Bucket: "my-bucket",
			},
		},
		{
			name: "FullConfig",
			input: &s3Config{
				Region:          "us-west-2",
				Endpoint:        "http://localhost:9000",
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
				SessionToken:    "test-token",
				Profile:         "test-profile",
				Bucket:          "test-bucket",
				ForcePathStyle:  true,
				DisableSSL:      true,
			},
			expected: &core.S3Config{
				Region:          "us-west-2",
				Endpoint:        "http://localhost:9000",
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
				SessionToken:    "test-token",
				Profile:         "test-profile",
				Bucket:          "test-bucket",
				ForcePathStyle:  true,
				DisableSSL:      true,
			},
		},
		{
			name: "MinIOConfig",
			input: &s3Config{
				Endpoint:        "http://minio:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
				Bucket:          "data",
				ForcePathStyle:  true,
			},
			expected: &core.S3Config{
				Endpoint:        "http://minio:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
				Bucket:          "data",
				ForcePathStyle:  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := &dag{S3: tt.input}
			result, err := buildS3(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildRedis(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     *redisConfig
		expected  *core.RedisConfig
		expectErr bool
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name: "BasicConfigWithURL",
			input: &redisConfig{
				URL: "redis://localhost:6379/0",
			},
			expected: &core.RedisConfig{
				URL: "redis://localhost:6379/0",
			},
		},
		{
			name: "BasicConfigWithHost",
			input: &redisConfig{
				Host:     "localhost",
				Port:     6379,
				Password: "secret",
			},
			expected: &core.RedisConfig{
				Host:     "localhost",
				Port:     6379,
				Password: "secret",
			},
		},
		{
			name: "FullConfig",
			input: &redisConfig{
				URL:           "redis://user:pass@host:6380/1",
				Host:          "redis.example.com",
				Port:          6380,
				Password:      "secret",
				Username:      "admin",
				DB:            1,
				TLS:           true,
				TLSSkipVerify: true,
				Mode:          "standalone",
				MaxRetries:    5,
			},
			expected: &core.RedisConfig{
				URL:           "redis://user:pass@host:6380/1",
				Host:          "redis.example.com",
				Port:          6380,
				Password:      "secret",
				Username:      "admin",
				DB:            1,
				TLS:           true,
				TLSSkipVerify: true,
				Mode:          "standalone",
				MaxRetries:    5,
			},
		},
		{
			name: "SentinelMode",
			input: &redisConfig{
				Mode:           "sentinel",
				SentinelMaster: "mymaster",
				SentinelAddrs:  []string{"sentinel1:26379", "sentinel2:26379"},
			},
			expected: &core.RedisConfig{
				Mode:           "sentinel",
				SentinelMaster: "mymaster",
				SentinelAddrs:  []string{"sentinel1:26379", "sentinel2:26379"},
			},
		},
		{
			name: "ClusterMode",
			input: &redisConfig{
				Mode:         "cluster",
				ClusterAddrs: []string{"node1:6379", "node2:6379", "node3:6379"},
			},
			expected: &core.RedisConfig{
				Mode:         "cluster",
				ClusterAddrs: []string{"node1:6379", "node2:6379", "node3:6379"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{Redis: tt.input}
			result, err := buildRedis(testBuildContext(), d)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildDotenv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    types.StringOrArray
		expected []string
	}{
		{
			name:     "EmptyDefaultsToDotEnv",
			input:    types.StringOrArray{},
			expected: []string{".env"},
		},
		{
			name:     "SingleFile",
			input:    stringOrArray(".env.local"),
			expected: []string{".env.local"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{Dotenv: tt.input}
			result, err := buildDotenv(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildSMTPConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    smtpConfig
		expected *core.SMTPConfig
	}{
		{
			name:     "Empty",
			input:    smtpConfig{},
			expected: nil,
		},
		{
			name: "FullConfig",
			input: smtpConfig{
				Host:     "smtp.example.com",
				Port:     portValue("587"),
				Username: "user",
				Password: "pass",
			},
			expected: &core.SMTPConfig{
				Host:     "smtp.example.com",
				Port:     "587",
				Username: "user",
				Password: "pass",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{SMTP: tt.input}
			result, err := buildSMTPConfig(testBuildContext(), d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildContainer(t *testing.T) {
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
				Name:          "my-container",
				Image:         "nginx:latest",
				PullPolicy:    "always",
				Volumes:       []string{"/host:/container"},
				User:          "nginx",
				WorkingDir:    "/app",
				Platform:      "linux/amd64",
				Ports:         []string{"8080:80"},
				Network:       "bridge",
				KeepContainer: true,
				Startup:       "command",
				Command:       []string{"nginx", "-g", "daemon off;"},
				WaitFor:       "healthy",
				LogPattern:    "ready",
				RestartPolicy: "always",
			},
			expected: &core.Container{
				Name:          "my-container",
				Image:         "nginx:latest",
				PullPolicy:    core.PullPolicyAlways,
				Volumes:       []string{"/host:/container"},
				User:          "nginx",
				WorkingDir:    "/app",
				Platform:      "linux/amd64",
				Ports:         []string{"8080:80"},
				Network:       "bridge",
				KeepContainer: true,
				Startup:       core.StartupCommand,
				Command:       []string{"nginx", "-g", "daemon off;"},
				WaitFor:       core.WaitForHealthy,
				LogPattern:    "ready",
				RestartPolicy: "always",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{Container: tt.input}
			result, err := buildContainer(testBuildContext(), d)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildRegistryAuths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected map[string]*core.AuthConfig
		wantErr  bool
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name:  "JSONString",
			input: `{"auths":{"registry.example.com":{"auth":"base64encoded"}}}`,
			expected: map[string]*core.AuthConfig{
				"_json": {Auth: `{"auths":{"registry.example.com":{"auth":"base64encoded"}}}`},
			},
		},
		{
			name: "MapWithStringAuth",
			input: map[string]any{
				"registry.example.com": "base64encoded",
			},
			expected: map[string]*core.AuthConfig{
				"registry.example.com": {Auth: "base64encoded"},
			},
		},
		{
			name: "MapWithUsernamePassword",
			input: map[string]any{
				"registry.example.com": map[string]any{
					"username": "user",
					"password": "pass",
				},
			},
			expected: map[string]*core.AuthConfig{
				"registry.example.com": {Username: "user", Password: "pass"},
			},
		},
		{
			name:    "InvalidType",
			input:   123,
			wantErr: true,
		},
		{
			name: "MapWithAuthField",
			input: map[string]any{
				"registry.example.com": map[string]any{
					"auth": "base64authstring",
				},
			},
			expected: map[string]*core.AuthConfig{
				"registry.example.com": {Auth: "base64authstring"},
			},
		},
		{
			name: "MapWithAllFields",
			input: map[string]any{
				"registry.example.com": map[string]any{
					"username": "user",
					"password": "pass",
					"auth":     "authtoken",
				},
			},
			expected: map[string]*core.AuthConfig{
				"registry.example.com": {Username: "user", Password: "pass", Auth: "authtoken"},
			},
		},
		{
			name: "MapAnyAnyType",
			input: map[any]any{
				"registry.example.com": map[any]any{
					"username": "user",
					"password": "pass",
				},
			},
			expected: map[string]*core.AuthConfig{
				"registry.example.com": {Username: "user", Password: "pass"},
			},
		},
		{
			name: "MapAnyAnyWithStringAuth",
			input: map[any]any{
				"registry.example.com": "base64encoded",
			},
			expected: map[string]*core.AuthConfig{
				"registry.example.com": {Auth: "base64encoded"},
			},
		},
		{
			name: "MultipleRegistries",
			input: map[string]any{
				"registry1.example.com": "auth1",
				"registry2.example.com": map[string]any{
					"username": "user2",
					"password": "pass2",
				},
			},
			expected: map[string]*core.AuthConfig{
				"registry1.example.com": {Auth: "auth1"},
				"registry2.example.com": {Username: "user2", Password: "pass2"},
			},
		},
		{
			name: "UnknownAuthType",
			input: map[string]any{
				"registry.example.com": 12345, // neither string nor map
			},
			expected: map[string]*core.AuthConfig{
				"registry.example.com": {}, // empty AuthConfig
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := &dag{RegistryAuths: tt.input}
			result, err := buildRegistryAuths(testBuildContext(), d)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildRegistryAuths_NoExpansion(t *testing.T) {
	// RegistryAuths are no longer expanded at build time - expansion happens at runtime
	// See runtime/agent/agent.go where credentials are evaluated before use
	t.Setenv("TEST_USER", "testuser")
	t.Setenv("TEST_PASS", "testpass")

	d := &dag{
		RegistryAuths: map[string]any{
			"registry.example.com": map[string]any{
				"username": "$TEST_USER",
				"password": "$TEST_PASS",
			},
		},
	}

	result, err := buildRegistryAuths(testBuildContext(), d)
	require.NoError(t, err)

	// Expects unexpanded values (expansion deferred to runtime)
	expected := map[string]*core.AuthConfig{
		"registry.example.com": {Username: "$TEST_USER", Password: "$TEST_PASS"},
	}
	assert.Equal(t, expected, result)
}

func TestBuildRegistryAuths_NoEval(t *testing.T) {
	t.Setenv("TEST_USER", "testuser")

	ctx := BuildContext{
		file: "/test/dag.yaml",
		opts: BuildOpts{Flags: BuildFlagNoEval},
	}

	d := &dag{
		RegistryAuths: map[string]any{
			"registry.example.com": map[string]any{
				"username": "$TEST_USER",
			},
		},
	}

	result, err := buildRegistryAuths(ctx, d)
	require.NoError(t, err)

	// Should NOT expand env vars when NoEval is set
	assert.Equal(t, "$TEST_USER", result["registry.example.com"].Username)
}

func TestBuildWorkingDir(t *testing.T) {
	t.Parallel()

	explicitAbsolutePath := "/custom/path"
	if runtime.GOOS == "windows" {
		explicitAbsolutePath = filepath.Join(filepath.Dir(testBuildContext().file), filepath.FromSlash("/custom/path"))
	}

	tests := []struct {
		name     string
		dag      *dag
		ctx      BuildContext
		expected string
	}{
		{
			name:     "ExplicitAbsolutePath",
			dag:      &dag{WorkingDir: "/custom/path"},
			ctx:      testBuildContext(),
			expected: explicitAbsolutePath,
		},
		{
			name:     "EmptyWhenNoExplicitValue",
			dag:      &dag{},
			ctx:      BuildContext{file: "/path/to/dag.yaml"},
			expected: "",
		},
		{
			name: "FromOptionsDefault",
			dag:  &dag{},
			ctx: BuildContext{
				file: "",
				opts: BuildOpts{DefaultWorkingDir: "/default/dir"},
			},
			expected: "/default/dir",
		},
		{
			name:     "EmptyWithNoFileOrDefault",
			dag:      &dag{},
			ctx:      BuildContext{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildWorkingDir(tt.ctx, tt.dag)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildWorkingDir_Relative(t *testing.T) {
	// Create a temp directory for testing relative paths
	tmpDir := t.TempDir()
	dagFile := filepath.Join(tmpDir, "dag.yaml")

	ctx := BuildContext{file: dagFile}
	d := &dag{WorkingDir: "subdir"}

	result, err := buildWorkingDir(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, "subdir"), result)
}

func TestBuildWorkingDir_NoExpansion(t *testing.T) {
	// WorkingDir is no longer expanded at build time - expansion happens at runtime
	// See runtime/env.go resolveWorkingDir()
	t.Setenv("WORK_DIR", "/expanded/path")

	ctx := testBuildContext()
	d := &dag{WorkingDir: "$WORK_DIR"}

	result, err := buildWorkingDir(ctx, d)
	require.NoError(t, err)
	// Expects unexpanded value (starts with $, so stored as-is for runtime evaluation)
	assert.Equal(t, "$WORK_DIR", result)
}

func TestBuildWorkingDir_NoEval(t *testing.T) {
	t.Setenv("TEST_PATH", "/expanded/path")

	ctx := BuildContext{
		file: "/test/dag.yaml",
		opts: BuildOpts{Flags: BuildFlagNoEval},
	}
	d := &dag{WorkingDir: "$TEST_PATH/subdir"}

	result, err := buildWorkingDir(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, "$TEST_PATH/subdir", result)
}

func TestBuildWorkingDir_TildePreserved(t *testing.T) {
	// ~ prefix is preserved at build time, expansion happens at runtime
	// See runtime/env.go resolveWorkingDir()
	t.Parallel()

	ctx := testBuildContext()
	d := &dag{WorkingDir: "~/mydir"}

	result, err := buildWorkingDir(ctx, d)
	require.NoError(t, err)
	// ~ prefix is preserved for runtime expansion
	assert.Equal(t, "~/mydir", result)
}

func TestBuildWorkingDir_DefaultWorkingDir(t *testing.T) {
	t.Parallel()

	ctx := BuildContext{
		opts: BuildOpts{DefaultWorkingDir: "/default/work/dir"},
	}
	d := &dag{} // No WorkingDir specified

	result, err := buildWorkingDir(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, "/default/work/dir", result)
}

func TestBuildWorkingDir_RelativeNoFileContext(t *testing.T) {
	t.Parallel()

	// Relative path without file context is stored as-is
	// (no file context means we can't resolve the relative path at build time)
	ctx := BuildContext{} // No file
	d := &dag{WorkingDir: "subdir"}

	result, err := buildWorkingDir(ctx, d)
	require.NoError(t, err)
	// Without file context, relative path is stored as-is
	assert.Equal(t, "subdir", result)
}

func TestBuildShell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		shell         types.ShellValue
		expectedShell string
		expectedArgs  []string
	}{
		{
			name:          "SimpleShell",
			shell:         shellValue("bash"),
			expectedShell: "bash",
			expectedArgs:  nil,
		},
		{
			name:          "ShellWithArgs",
			shell:         shellValue("bash -e -x"),
			expectedShell: "bash",
			expectedArgs:  []string{"-e", "-x"},
		},
		{
			name:          "EmptyShell",
			shell:         types.ShellValue{},
			expectedShell: "", // Will get default shell
			expectedArgs:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := &dag{Shell: tt.shell}
			shell, err := buildShell(testBuildContext(), d)
			require.NoError(t, err)
			if tt.expectedShell != "" {
				assert.Equal(t, tt.expectedShell, shell)
			} else {
				assert.NotEmpty(t, shell) // Default shell
			}

			args, err := buildShellArgs(testBuildContext(), d)
			require.NoError(t, err)
			if tt.expectedArgs == nil {
				assert.Empty(t, args)
			} else {
				assert.Equal(t, tt.expectedArgs, args)
			}
		})
	}
}

func TestBuildShell_ArrayForm(t *testing.T) {
	t.Parallel()

	d := &dag{Shell: shellValueArray([]string{"bash", "-e", "-x"})}

	shell, err := buildShell(testBuildContext(), d)
	require.NoError(t, err)
	assert.Equal(t, "bash", shell)

	args, err := buildShellArgs(testBuildContext(), d)
	require.NoError(t, err)
	assert.Equal(t, []string{"-e", "-x"}, args)
}

func TestBuildShell_ArrayFormEmptyArray(t *testing.T) {
	t.Parallel()

	// Empty array should fall back to default shell
	d := &dag{Shell: shellValueArray([]string{})}

	shell, err := buildShell(testBuildContext(), d)
	require.NoError(t, err)
	assert.NotEmpty(t, shell) // Default shell

	args, err := buildShellArgs(testBuildContext(), d)
	require.NoError(t, err)
	assert.Empty(t, args)
}

func TestBuildShell_ArrayFormNoEval(t *testing.T) {
	t.Parallel()

	// With NoEval, env var references should be preserved as-is
	d := &dag{Shell: shellValueArray([]string{"$SHELL_CMD", "$SHELL_ARG", "-x"})}

	ctx := BuildContext{
		file: "/test/dag.yaml",
		opts: BuildOpts{Flags: BuildFlagNoEval},
	}
	shell, err := buildShell(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, "$SHELL_CMD", shell)

	args, err := buildShellArgs(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, []string{"$SHELL_ARG", "-x"}, args)
}

func TestBuildPreconditions(t *testing.T) {
	t.Parallel()

	t.Run("NilPreconditions", func(t *testing.T) {
		t.Parallel()
		d := &dag{}
		result, err := buildPreconditions(testBuildContext(), d)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("SinglePrecondition", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			Preconditions: []any{
				map[string]any{"condition": "test -f /file"},
			},
		}
		result, err := buildPreconditions(testBuildContext(), d)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "test -f /file", result[0].Condition)
	})

	t.Run("MultiplePreconditions", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			Preconditions: []any{
				map[string]any{"condition": "test -f /file1"},
				map[string]any{"condition": "test -f /file2"},
			},
		}
		result, err := buildPreconditions(testBuildContext(), d)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("InvalidPreconditionsType", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			Preconditions: []any{
				map[string]any{"condition": 123}, // Invalid type
			},
		}
		_, err := buildPreconditions(testBuildContext(), d)
		require.Error(t, err)
	})
}

func TestBuildSteps(t *testing.T) {
	t.Parallel()

	t.Run("NilSteps", func(t *testing.T) {
		t.Parallel()
		d := &dag{Steps: nil}
		result := &core.DAG{}
		steps, err := buildSteps(testBuildContext(), d, result)
		require.NoError(t, err)
		assert.Nil(t, steps)
	})

	t.Run("ArrayOfSteps", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			Steps: []any{
				map[string]any{"name": "step1", "command": "echo 1"},
				map[string]any{"name": "step2", "command": "echo 2"},
			},
		}
		result := &core.DAG{}
		steps, err := buildSteps(testBuildContext(), d, result)
		require.NoError(t, err)
		assert.Len(t, steps, 2)
		assert.Equal(t, "step1", steps[0].Name)
		assert.Equal(t, "step2", steps[1].Name)
	})

	t.Run("MapOfSteps", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			Steps: map[string]any{
				"step1": map[string]any{"command": "echo 1"},
				"step2": map[string]any{"command": "echo 2"},
			},
		}
		result := &core.DAG{}
		steps, err := buildSteps(testBuildContext(), d, result)
		require.NoError(t, err)
		assert.Len(t, steps, 2)
	})

	t.Run("NestedParallelSteps", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			Steps: []any{
				[]any{
					map[string]any{"name": "parallel1", "command": "echo p1"},
					map[string]any{"name": "parallel2", "command": "echo p2"},
				},
			},
		}
		result := &core.DAG{}
		steps, err := buildSteps(testBuildContext(), d, result)
		require.NoError(t, err)
		assert.Len(t, steps, 2)
	})

	t.Run("InvalidStepType", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			Steps: []any{
				123, // Numbers are invalid step types (strings get normalized to maps)
			},
		}
		result := &core.DAG{}
		_, err := buildSteps(testBuildContext(), d, result)
		require.Error(t, err)
	})

	t.Run("InvalidNestedStepType", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			Steps: []any{
				[]any{456}, // Numbers are invalid even when nested
			},
		}
		result := &core.DAG{}
		_, err := buildSteps(testBuildContext(), d, result)
		require.Error(t, err)
	})
}

func TestBuildOTel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected *core.OTelConfig
		wantErr  bool
	}{
		{
			name:     "Nil",
			input:    nil,
			expected: nil,
		},
		{
			name: "FullConfig",
			input: map[string]any{
				"enabled":  true,
				"endpoint": "http://localhost:4317",
				"headers": map[string]any{
					"Authorization": "Bearer token",
				},
				"insecure": true,
				"timeout":  "30s",
				"resource": map[string]any{
					"service.name":    "dagu-test",
					"service.version": "1.0.0",
				},
			},
			expected: &core.OTelConfig{
				Enabled:  true,
				Endpoint: "http://localhost:4317",
				Headers: map[string]string{
					"Authorization": "Bearer token",
				},
				Insecure: true,
				Timeout:  30 * time.Second,
				Resource: map[string]any{
					"service.name":    "dagu-test",
					"service.version": "1.0.0",
				},
			},
		},
		{
			name:    "InvalidTimeout",
			input:   map[string]any{"timeout": "invalid"},
			wantErr: true,
		},
		{
			name:    "InvalidType",
			input:   "not a map",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &dag{OTel: tt.input}
			result, err := buildOTel(testBuildContext(), d)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildSecrets(t *testing.T) {
	t.Parallel()

	t.Run("success cases", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			input    []secretRef
			expected []core.SecretRef
		}{
			{
				name:     "Nil",
				input:    nil,
				expected: nil,
			},
			{
				name:     "Empty",
				input:    []secretRef{},
				expected: nil,
			},
			{
				name: "SingleSecret",
				input: []secretRef{
					{Name: "API_KEY", Provider: "env", Key: "MY_API_KEY"},
				},
				expected: []core.SecretRef{
					{Name: "API_KEY", Provider: "env", Key: "MY_API_KEY"},
				},
			},
			{
				name: "MultipleSecrets",
				input: []secretRef{
					{Name: "DB_PASSWORD", Provider: "vault", Key: "secret/data/prod/db"},
					{Name: "API_KEY", Provider: "env", Key: "API_KEY"},
				},
				expected: []core.SecretRef{
					{Name: "DB_PASSWORD", Provider: "vault", Key: "secret/data/prod/db"},
					{Name: "API_KEY", Provider: "env", Key: "API_KEY"},
				},
			},
			{
				name: "WithOptions",
				input: []secretRef{
					{
						Name:     "DB_PASSWORD",
						Provider: "vault",
						Key:      "secret/data/prod/db",
						Options: map[string]string{
							"field":         "password",
							"vault_address": "http://127.0.0.1:8200",
						},
					},
				},
				expected: []core.SecretRef{
					{
						Name:     "DB_PASSWORD",
						Provider: "vault",
						Key:      "secret/data/prod/db",
						Options: map[string]string{
							"field":         "password",
							"vault_address": "http://127.0.0.1:8200",
						},
					},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				d := &dag{Secrets: tt.input}
				result, err := buildSecrets(testBuildContext(), d)
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	t.Run("error cases", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name        string
			input       []secretRef
			errContains string
		}{
			{
				name: "MissingName",
				input: []secretRef{
					{Provider: "vault", Key: "secret/data/test"},
				},
				errContains: "'name' field is required",
			},
			{
				name: "MissingProvider",
				input: []secretRef{
					{Name: "MY_SECRET", Key: "secret/data/test"},
				},
				errContains: "'provider' field is required",
			},
			{
				name: "MissingKey",
				input: []secretRef{
					{Name: "MY_SECRET", Provider: "vault"},
				},
				errContains: "'key' field is required",
			},
			{
				name: "DuplicateNames",
				input: []secretRef{
					{Name: "API_KEY", Provider: "vault", Key: "secret/v1"},
					{Name: "API_KEY", Provider: "env", Key: "API_KEY"},
				},
				errContains: "duplicate secret name",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				d := &dag{Secrets: tt.input}
				_, err := buildSecrets(testBuildContext(), d)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			})
		}
	})
}

func TestBuildMailConfigInternal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    mailConfig
		expected *core.MailConfig
	}{
		{
			name:     "Empty",
			input:    mailConfig{},
			expected: nil,
		},
		{
			name: "FullConfig",
			input: mailConfig{
				From:       "sender@example.com",
				To:         stringOrArray("recipient@example.com"),
				Prefix:     "[DAG]",
				AttachLogs: true,
			},
			expected: &core.MailConfig{
				From:       "sender@example.com",
				To:         []string{"recipient@example.com"},
				Prefix:     "[DAG]",
				AttachLogs: true,
			},
		},
		{
			name: "MultipleRecipients",
			input: mailConfig{
				From: "sender@example.com",
				To:   stringOrArrayList([]string{"a@example.com", "b@example.com"}),
			},
			expected: &core.MailConfig{
				From: "sender@example.com",
				To:   []string{"a@example.com", "b@example.com"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildMailConfigInternal(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildHandlers(t *testing.T) {
	t.Parallel()

	t.Run("AllHandlers", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			HandlerOn: handlerOn{
				Init:    &step{Command: "echo init"},
				Exit:    &step{Command: "echo exit"},
				Success: &step{Command: "echo success"},
				Failure: &step{Command: "echo failure"},
				Abort:   &step{Command: "echo abort"},
				Wait:    &step{Command: "echo wait"},
			},
		}
		result := &core.DAG{}
		handlerOn, err := buildHandlers(testBuildContext(), d, result)
		require.NoError(t, err)
		require.NotNil(t, handlerOn.Init)
		require.Len(t, handlerOn.Init.Commands, 1)
		assert.Equal(t, "echo init", handlerOn.Init.Commands[0].CmdWithArgs)
		require.NotNil(t, handlerOn.Exit)
		require.Len(t, handlerOn.Exit.Commands, 1)
		assert.Equal(t, "echo exit", handlerOn.Exit.Commands[0].CmdWithArgs)
		require.NotNil(t, handlerOn.Success)
		require.Len(t, handlerOn.Success.Commands, 1)
		assert.Equal(t, "echo success", handlerOn.Success.Commands[0].CmdWithArgs)
		require.NotNil(t, handlerOn.Failure)
		require.Len(t, handlerOn.Failure.Commands, 1)
		assert.Equal(t, "echo failure", handlerOn.Failure.Commands[0].CmdWithArgs)
		require.NotNil(t, handlerOn.Abort)
		require.Len(t, handlerOn.Abort.Commands, 1)
		assert.Equal(t, "echo abort", handlerOn.Abort.Commands[0].CmdWithArgs)
		require.NotNil(t, handlerOn.Wait)
		require.Len(t, handlerOn.Wait.Commands, 1)
		assert.Equal(t, "echo wait", handlerOn.Wait.Commands[0].CmdWithArgs)
	})

	t.Run("NoHandlers", func(t *testing.T) {
		t.Parallel()
		d := &dag{}
		result := &core.DAG{}
		handlerOn, err := buildHandlers(testBuildContext(), d, result)
		require.NoError(t, err)
		assert.Nil(t, handlerOn.Init)
		assert.Nil(t, handlerOn.Exit)
		assert.Nil(t, handlerOn.Success)
		assert.Nil(t, handlerOn.Failure)
		assert.Nil(t, handlerOn.Abort)
		assert.Nil(t, handlerOn.Wait)
	})

	t.Run("InitHandlerError", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			HandlerOn: handlerOn{
				Init: &step{Command: "   "}, // Empty command after trim causes error
			},
		}
		result := &core.DAG{}
		_, err := buildHandlers(testBuildContext(), d, result)
		require.Error(t, err)
	})

	t.Run("ExitHandlerError", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			HandlerOn: handlerOn{
				Exit: &step{Command: "   "}, // Empty command after trim causes error
			},
		}
		result := &core.DAG{}
		_, err := buildHandlers(testBuildContext(), d, result)
		require.Error(t, err)
	})

	t.Run("SuccessHandlerError", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			HandlerOn: handlerOn{
				Success: &step{Command: "   "}, // Empty command after trim causes error
			},
		}
		result := &core.DAG{}
		_, err := buildHandlers(testBuildContext(), d, result)
		require.Error(t, err)
	})

	t.Run("FailureHandlerError", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			HandlerOn: handlerOn{
				Failure: &step{Command: "   "}, // Empty command after trim causes error
			},
		}
		result := &core.DAG{}
		_, err := buildHandlers(testBuildContext(), d, result)
		require.Error(t, err)
	})

	t.Run("AbortHandlerError", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			HandlerOn: handlerOn{
				Abort: &step{Command: "   "}, // Empty command after trim causes error
			},
		}
		result := &core.DAG{}
		_, err := buildHandlers(testBuildContext(), d, result)
		require.Error(t, err)
	})

	t.Run("WaitHandler", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			HandlerOn: handlerOn{
				Wait: &step{Command: "echo wait"},
			},
		}
		result := &core.DAG{}
		handlerOn, err := buildHandlers(testBuildContext(), d, result)
		require.NoError(t, err)
		require.NotNil(t, handlerOn.Wait)
		require.Len(t, handlerOn.Wait.Commands, 1)
		assert.Equal(t, "echo wait", handlerOn.Wait.Commands[0].CmdWithArgs)
		assert.Equal(t, "onWait", handlerOn.Wait.Name)
	})

	t.Run("WaitHandlerError", func(t *testing.T) {
		t.Parallel()
		d := &dag{
			HandlerOn: handlerOn{
				Wait: &step{Command: "   "}, // Empty command after trim causes error
			},
		}
		result := &core.DAG{}
		_, err := buildHandlers(testBuildContext(), d, result)
		require.Error(t, err)
	})
}

func TestBuildLogOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		expected    core.LogOutputMode
		wantErr     bool
		errContains string
	}{
		{
			name:     "Default_Empty",
			yaml:     "",
			expected: "", // Empty allows inheritance; default applied in InitializeDefaults
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

			var d dag
			if tt.yaml != "" {
				err := yaml.Unmarshal([]byte(tt.yaml), &d)
				if tt.wantErr {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.errContains)
					return
				}
				require.NoError(t, err)
			}

			result, err := buildLogOutput(testBuildContext(), &d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildArtifacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yaml     string
		expected *core.ArtifactsConfig
	}{
		{
			name:     "Default_Empty",
			yaml:     "",
			expected: nil,
		},
		{
			name: "EnabledOnly",
			yaml: `
artifacts:
  enabled: true
`,
			expected: &core.ArtifactsConfig{Enabled: true},
		},
		{
			name: "DisabledExplicit",
			yaml: `
artifacts:
  enabled: false
`,
			expected: &core.ArtifactsConfig{Enabled: false},
		},
		{
			name: "DirOnly",
			yaml: `
artifacts:
  dir: /var/lib/dagu/artifacts
`,
			expected: &core.ArtifactsConfig{Dir: "/var/lib/dagu/artifacts"},
		},
		{
			name: "EnabledAndDir",
			yaml: `
artifacts:
  enabled: true
  dir: /var/lib/dagu/artifacts
`,
			expected: &core.ArtifactsConfig{
				Enabled: true,
				Dir:     "/var/lib/dagu/artifacts",
			},
		},
		{
			name: "AutoEnableWhenCommandReferencesArtifactsDir",
			yaml: `
steps:
  - name: write
    run: printf 'artifact' > "$DAG_RUN_ARTIFACTS_DIR/out.txt"
`,
			expected: &core.ArtifactsConfig{Enabled: true},
		},
		{
			name: "AutoEnableWhenNestedExecutorConfigReferencesArtifactsDir",
			yaml: `
steps:
  - name: render
    action: template.render
    with:
      output: ${DAG_RUN_ARTIFACTS_DIR}/greeting.txt
      data:
        name: tom
      template: |
        Hello, {{ .name }}!
`,
			expected: &core.ArtifactsConfig{Enabled: true},
		},
		{
			name: "AutoEnableWhenPowerShellEnvReferenceArtifactsDir",
			yaml: `
steps:
  - name: write
    run: Write-Output $env:DAG_RUN_ARTIFACTS_DIR
`,
			expected: &core.ArtifactsConfig{Enabled: true},
		},
		{
			name: "LiteralMentionWithoutEnvReferenceDoesNotAutoEnable",
			yaml: `
steps:
  - name: write
    run: printf 'DAG_RUN_ARTIFACTS_DIR'
`,
			expected: nil,
		},
		{
			name: "ExplicitDisableWinsOverAutoEnable",
			yaml: `
artifacts:
  enabled: false
steps:
  - name: write
    run: printf 'artifact' > "$DAG_RUN_ARTIFACTS_DIR/out.txt"
`,
			expected: &core.ArtifactsConfig{Enabled: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var d dag
			if tt.yaml != "" {
				err := yaml.Unmarshal([]byte(tt.yaml), &d)
				require.NoError(t, err)
			}

			result, err := buildArtifacts(testBuildContext(), &d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildApprovalStepsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dag         *dag
		expectErr   bool
		errContains string
	}{
		{
			name: "NoApprovalSteps",
			dag: &dag{
				Name:           "test-dag",
				WorkerSelector: map[string]string{"region": "us-west"},
				Steps: []any{
					map[string]any{"name": "step1", "script": "echo hello"},
				},
			},
			expectErr: false,
		},
		{
			name: "NoWorkerSelector",
			dag: &dag{
				Name: "test-dag",
				Steps: []any{
					map[string]any{"name": "step1", "command": "true", "approval": map[string]any{}},
				},
			},
			expectErr: false,
		},
		{
			name: "Conflict",
			dag: &dag{
				Name:           "test-dag",
				WorkerSelector: map[string]string{"region": "us-west"},
				Steps: []any{
					map[string]any{"name": "step1", "command": "true", "approval": map[string]any{}},
				},
			},
			expectErr:   true,
			errContains: "DAG with approval steps cannot be dispatched to workers",
		},
		{
			name: "ConflictMultipleSteps",
			dag: &dag{
				Name:           "test-dag",
				WorkerSelector: map[string]string{"region": "us-west"},
				Steps: []any{
					map[string]any{"name": "step1", "script": "echo hello"},
					map[string]any{"name": "step2", "command": "true", "approval": map[string]any{}},
					map[string]any{"name": "step3", "script": "echo done"},
				},
			},
			expectErr:   true,
			errContains: "DAG with approval steps cannot be dispatched to workers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := tt.dag.build(testBuildContext())
			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestParseHealthcheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   *healthcheck
		wantErr string
	}{
		{
			name: "valid CMD healthcheck",
			input: &healthcheck{
				Test:     []string{"CMD", "pg_isready"},
				Interval: "5s",
				Timeout:  "3s",
				Retries:  3,
			},
			wantErr: "",
		},
		{
			name: "valid CMD-SHELL healthcheck",
			input: &healthcheck{
				Test:        []string{"CMD-SHELL", "pg_isready -U postgres"},
				Interval:    "2s",
				StartPeriod: "10s",
			},
			wantErr: "",
		},
		{
			name: "valid NONE healthcheck",
			input: &healthcheck{
				Test: []string{"NONE"},
			},
			wantErr: "",
		},
		{
			name:    "nil healthcheck",
			input:   nil,
			wantErr: "",
		},
		{
			name: "empty test",
			input: &healthcheck{
				Test: []string{},
			},
			wantErr: "test is required",
		},
		{
			name: "invalid test prefix",
			input: &healthcheck{
				Test: []string{"INVALID", "command"},
			},
			wantErr: "must start with NONE, CMD, or CMD-SHELL",
		},
		{
			name: "NONE with extra args",
			input: &healthcheck{
				Test: []string{"NONE", "extra"},
			},
			wantErr: "NONE healthcheck should not have additional arguments",
		},
		{
			name: "CMD without command",
			input: &healthcheck{
				Test: []string{"CMD"},
			},
			wantErr: "CMD healthcheck requires a command",
		},
		{
			name: "CMD-SHELL without command",
			input: &healthcheck{
				Test: []string{"CMD-SHELL"},
			},
			wantErr: "CMD-SHELL healthcheck requires a command",
		},
		{
			name: "negative retries",
			input: &healthcheck{
				Test:    []string{"CMD", "true"},
				Retries: -1,
			},
			wantErr: "retries must be non-negative",
		},
		{
			name: "invalid interval duration",
			input: &healthcheck{
				Test:     []string{"CMD", "true"},
				Interval: "invalid",
			},
			wantErr: "invalid interval",
		},
		{
			name: "invalid timeout duration",
			input: &healthcheck{
				Test:    []string{"CMD", "true"},
				Timeout: "5",
			},
			wantErr: "invalid timeout",
		},
		{
			name: "invalid start_period duration",
			input: &healthcheck{
				Test:        []string{"CMD", "true"},
				StartPeriod: "bad",
			},
			wantErr: "invalid start_period",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseHealthcheck(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				if tt.input == nil {
					assert.Nil(t, result)
				} else {
					assert.NotNil(t, result)
					assert.Equal(t, tt.input.Test, result.Test)
				}
			}
		})
	}
}

func TestParseHealthcheck_DurationsCorrect(t *testing.T) {
	t.Parallel()

	input := &healthcheck{
		Test:        []string{"CMD", "pg_isready"},
		Interval:    "5s",
		Timeout:     "3s",
		StartPeriod: "10s",
		Retries:     5,
	}

	result, err := parseHealthcheck(input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 5*time.Second, result.Interval)
	assert.Equal(t, 3*time.Second, result.Timeout)
	assert.Equal(t, 10*time.Second, result.StartPeriod)
	assert.Equal(t, 5, result.Retries)
}

func TestBuildContainerFromSpec_HealthcheckInExecMode(t *testing.T) {
	t.Parallel()

	c := &container{
		Exec: "my-container",
		Healthcheck: &healthcheck{
			Test: []string{"CMD", "true"},
		},
	}

	_, err := buildContainerFromSpec(testBuildContext(), c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "healthcheck")
	assert.Contains(t, err.Error(), "cannot be used with 'exec'")
}

func TestBuildContainerFromSpec_HealthcheckInImageMode(t *testing.T) {
	t.Parallel()

	c := &container{
		Image: "postgres:alpine",
		Healthcheck: &healthcheck{
			Test:        []string{"CMD-SHELL", "pg_isready -U postgres"},
			Interval:    "2s",
			Timeout:     "5s",
			StartPeriod: "10s",
			Retries:     5,
		},
		WaitFor: "healthy",
	}

	result, err := buildContainerFromSpec(testBuildContext(), c)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Healthcheck)

	assert.Equal(t, []string{"CMD-SHELL", "pg_isready -U postgres"}, result.Healthcheck.Test)
	assert.Equal(t, 2*time.Second, result.Healthcheck.Interval)
	assert.Equal(t, 5*time.Second, result.Healthcheck.Timeout)
	assert.Equal(t, 10*time.Second, result.Healthcheck.StartPeriod)
	assert.Equal(t, 5, result.Healthcheck.Retries)
	assert.Equal(t, core.WaitForHealthy, result.WaitFor)
}

func TestChainTypeDependsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dag         *dag
		expectErr   bool
		errContains string
	}{
		{
			name: "ChainTypeWithExplicitDependsShouldError",
			dag: &dag{
				Type: "chain",
				Steps: []any{
					map[string]any{"name": "step1", "command": "echo 1"},
					map[string]any{"name": "step2", "command": "echo 2", "depends": []string{"step1"}},
				},
			},
			expectErr:   true,
			errContains: "depends field is not allowed for DAGs with type 'chain'",
		},
		{
			name: "ChainTypeWithEmptyDependsShouldError",
			dag: &dag{
				Type: "chain",
				Steps: []any{
					map[string]any{"name": "step1", "command": "echo 1"},
					map[string]any{"name": "step2", "command": "echo 2", "depends": []string{}},
				},
			},
			expectErr:   true,
			errContains: "depends field is not allowed for DAGs with type 'chain'",
		},
		{
			name: "ChainTypeWithoutDependsShouldWork",
			dag: &dag{
				Type: "chain",
				Steps: []any{
					map[string]any{"name": "step1", "command": "echo 1"},
					map[string]any{"name": "step2", "command": "echo 2"},
				},
			},
			expectErr: false,
		},
		{
			name: "GraphTypeWithDependsShouldWork",
			dag: &dag{
				Type: "graph",
				Steps: []any{
					map[string]any{"name": "step1", "command": "echo 1"},
					map[string]any{"name": "step2", "command": "echo 2", "depends": []string{"step1"}},
				},
			},
			expectErr: false,
		},
		{
			name: "GraphTypeWithEmptyDependsShouldWork",
			dag: &dag{
				Type: "graph",
				Steps: []any{
					map[string]any{"name": "step1", "command": "echo 1"},
					map[string]any{"name": "step2", "command": "echo 2", "depends": []string{}},
				},
			},
			expectErr: false,
		},
		{
			name: "DefaultTypeWithDependsShouldError",
			dag: &dag{
				// Default type is chain, so depends should not be allowed
				Steps: []any{
					map[string]any{"name": "step1", "command": "echo 1"},
					map[string]any{"name": "step2", "command": "echo 2", "depends": []string{"step1"}},
				},
			},
			expectErr:   true,
			errContains: "depends field is not allowed for DAGs with type 'chain'",
		},
		{
			name: "ChainTypeNestedParallelWithDependsShouldError",
			dag: &dag{
				Type: "chain",
				Steps: []any{
					map[string]any{"name": "step1", "command": "echo 1"},
					[]any{
						map[string]any{"name": "parallel1", "command": "echo p1", "depends": []string{"step1"}},
						map[string]any{"name": "parallel2", "command": "echo p2"},
					},
				},
			},
			expectErr:   true,
			errContains: "depends field is not allowed for DAGs with type 'chain'",
		},
		{
			name: "ChainTypeMapFormWithDependsShouldError",
			dag: &dag{
				Type: "chain",
				Steps: map[string]any{
					"step1": map[string]any{"command": "echo 1"},
					"step2": map[string]any{"command": "echo 2", "depends": []string{"step1"}},
				},
			},
			expectErr:   true,
			errContains: "depends field is not allowed for DAGs with type 'chain'",
		},
		{
			name: "GraphTypeMapFormWithDependsShouldWork",
			dag: &dag{
				Type: "graph",
				Steps: map[string]any{
					"step1": map[string]any{"command": "echo 1"},
					"step2": map[string]any{"command": "echo 2", "depends": []string{"step1"}},
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := tt.dag.build(testBuildContext())
			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestBuildEnvReferencesParams(t *testing.T) {
	t.Parallel()

	yamlData := `
params:
  data_dir: /tmp/foo
env:
  - FULL_PATH: "${data_dir}/output"
steps:
  - run: echo test
`
	d, err := LoadYAML(context.Background(), []byte(yamlData))
	require.NoError(t, err)

	found := false
	for _, e := range d.Env {
		if strings.HasPrefix(e, "FULL_PATH=") {
			require.Equal(t, "FULL_PATH=/tmp/foo/output", e)
			found = true
		}
	}
	require.True(t, found, "FULL_PATH env var not found")
}

func TestBuildEnvReferencesParamsWithoutEval(t *testing.T) {
	t.Parallel()

	yamlData := `
params:
  data_dir: /tmp/foo
env:
  - FULL_PATH: "${data_dir}/output"
steps:
  - run: echo test
`
	d, err := LoadYAML(context.Background(), []byte(yamlData), WithoutEval())
	require.NoError(t, err)

	found := false
	for _, e := range d.Env {
		if strings.HasPrefix(e, "FULL_PATH=") {
			require.Equal(t, "FULL_PATH=${data_dir}/output", e)
			found = true
		}
	}
	require.True(t, found, "FULL_PATH env var not found")
}

func TestBuildEnvReferencesParamsOnlyMetadata(t *testing.T) {
	t.Parallel()

	yamlData := `
params:
  data_dir: /tmp/foo
env:
  - FULL_PATH: "${data_dir}/output"
steps:
  - run: echo test
`
	d, err := LoadYAML(context.Background(), []byte(yamlData), OnlyMetadata())
	require.NoError(t, err)

	found := false
	for _, e := range d.Env {
		if strings.HasPrefix(e, "FULL_PATH=") {
			require.Equal(t, "FULL_PATH=/tmp/foo/output", e)
			found = true
		}
	}
	require.True(t, found, "FULL_PATH env var not found")
}

func TestRouterNotAllowedInChainType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dag         *dag
		expectErr   bool
		errContains string
	}{
		{
			name: "ChainTypeWithRouterShouldError",
			dag: &dag{
				Type: "chain",
				Steps: []any{
					map[string]any{
						"name":  "router",
						"type":  "router",
						"value": "${MODE}",
						"routes": map[string]any{
							"a": []string{"step_a"},
						},
					},
					map[string]any{"name": "step_a", "command": "echo A"},
				},
			},
			expectErr:   true,
			errContains: "router steps require type 'graph'",
		},
		{
			name: "DefaultTypeWithRouterShouldError",
			dag: &dag{
				// Default type is chain, so router should not be allowed
				Steps: []any{
					map[string]any{
						"name":  "router",
						"type":  "router",
						"value": "${MODE}",
						"routes": map[string]any{
							"a": []string{"step_a"},
						},
					},
					map[string]any{"name": "step_a", "command": "echo A"},
				},
			},
			expectErr:   true,
			errContains: "router steps require type 'graph'",
		},
		{
			name: "GraphTypeWithRouterShouldWork",
			dag: &dag{
				Type: "graph",
				Steps: []any{
					map[string]any{
						"name":  "router",
						"type":  "router",
						"value": "${MODE}",
						"routes": map[string]any{
							"a": []string{"step_a"},
						},
					},
					map[string]any{"name": "step_a", "command": "echo A"},
				},
			},
			expectErr: false,
		},
		{
			name: "ChainTypeNestedParallelWithRouterShouldError",
			dag: &dag{
				Type: "chain",
				Steps: []any{
					map[string]any{"name": "step1", "command": "echo 1"},
					[]any{
						map[string]any{
							"name":  "router",
							"type":  "router",
							"value": "${MODE}",
							"routes": map[string]any{
								"a": []string{"step_a"},
							},
						},
						map[string]any{"name": "step_a", "command": "echo A"},
					},
				},
			},
			expectErr:   true,
			errContains: "router steps require type 'graph'",
		},
		{
			name: "ChainTypeMapFormWithRouterShouldError",
			dag: &dag{
				Type: "chain",
				Steps: map[string]any{
					"router": map[string]any{
						"type":  "router",
						"value": "${MODE}",
						"routes": map[string]any{
							"a": []string{"step_a"},
						},
					},
					"step_a": map[string]any{"command": "echo A"},
				},
			},
			expectErr:   true,
			errContains: "router steps require type 'graph'",
		},
		{
			name: "GraphTypeMapFormWithRouterShouldWork",
			dag: &dag{
				Type: "graph",
				Steps: map[string]any{
					"router": map[string]any{
						"type":  "router",
						"value": "${MODE}",
						"routes": map[string]any{
							"a": []string{"step_a"},
						},
					},
					"step_a": map[string]any{"command": "echo A"},
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := tt.dag.build(testBuildContext())
			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}
