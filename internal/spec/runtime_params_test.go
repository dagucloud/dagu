// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeParamsRetainWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dag, err := LoadYAML(ctx, []byte("name: parent\nlabels: [workspace=ops]\nsteps:\n  - run: echo parent\n---\nname: child\nsteps:\n  - run: echo child\n"))
	require.NoError(t, err)
	child, err := ResolveRuntimeParams(ctx, dag.LocalDAGs["child"], "", ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	baseDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(baseDir, "ops"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "ops", "base.yaml"), []byte("smtp:\n  host: ops.example\n"), 0600))
	child, err = RefreshBaseSMTP(child, WithWorkspaceBaseConfigDir(baseDir))
	require.NoError(t, err)
	child, err = RebuildFromYAML(ctx, child)
	require.NoError(t, err)
	require.NotNil(t, child.SMTP)
	assert.Equal(t, "ops.example", child.SMTP.Host)
}

func TestResolveRuntimeParams(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: runtime-params
params:
  - name: region
    type: string
    enum: [us-east-1, us-west-2]
    required: true
  - name: count
    default: 3
    type: integer
    minimum: 1
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	resolved, err := ResolveRuntimeParams(context.Background(), dag, "region=us-west-2 count=5", ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"region=us-west-2", "count=5"}, resolved.Params)
	assert.JSONEq(t, `{"region":"us-west-2","count":"5"}`, resolved.ParamsJSON)
}

func TestResolveRuntimeParams_ValidatesEmptyRuntimeInput(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: runtime-params
params:
  - name: region
    type: string
    required: true
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	_, err = ResolveRuntimeParams(context.Background(), dag, "", ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region")
}

func TestResolveRuntimeParams_RequiresSource(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "runtime-params"}

	_, err := ResolveRuntimeParams(context.Background(), dag, "", ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source")
}

func TestResolveRuntimeParams_RejectsInvalidEnum(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: runtime-params
params:
  - name: region
    type: string
    enum: [us-east-1, us-west-2]
    required: true
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	_, err = ResolveRuntimeParams(context.Background(), dag, "region=eu-central-1", ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region")
}

func TestResolveRuntimeParams_RejectsCoercionError(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: runtime-params
params:
  - name: count
    type: integer
    required: true
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	_, err = ResolveRuntimeParams(context.Background(), dag, "count=abc", ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `cannot coerce "abc" to integer`)
}

func TestResolveRuntimeParams_RejectsBoundaryViolation(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: runtime-params
params:
  - name: count
    type: integer
    minimum: 1
    maximum: 5
    required: true
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	_, err = ResolveRuntimeParams(context.Background(), dag, "count=6", ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "count")
}

func TestResolveRuntimeParams_PrefersLocationOverYamlData(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "properties": {
    "region": {
      "type": "string",
      "enum": ["us-east-1", "us-west-2"]
    }
  },
  "required": ["region"]
}`), 0o600))

	dagPath := filepath.Join(dir, "runtime-params.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: runtime-params
params:
  schema: params.schema.json
`), 0o600))

	dag, err := Load(context.Background(), dagPath, WithoutEval())
	require.NoError(t, err)
	require.NotEmpty(t, dag.Location)
	require.NotEmpty(t, dag.YamlData)
	dag.SourceFile = filepath.Join(dir, "authored.yaml")

	resolved, err := ResolveRuntimeParams(context.Background(), dag, "region=us-east-1", ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"region=us-east-1"}, resolved.Params)
	assert.Equal(t, dag.SourceFile, resolved.SourceFile)
}

func TestResolveRuntimeParams_LegacyNamedParamsPreservePositionalOverrides(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: runtime-params
params:
  - TAG: ""
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	resolved, err := ResolveRuntimeParams(context.Background(), dag, []string{"simple"}, ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"TAG=", "1=simple"}, resolved.Params)
}

func TestResolveRuntimeParams_EvaluatesParamEvalOnReload(t *testing.T) {
	t.Setenv("WORK_DIR", "/tmp/work")

	yaml := []byte(`
name: runtime-eval
params:
  - name: base_dir
    eval: "$WORK_DIR/pipeline"
  - name: output_dir
    eval: "$base_dir/output"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	resolved, err := ResolveRuntimeParams(context.Background(), dag, "", ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"base_dir=/tmp/work/pipeline",
		"output_dir=/tmp/work/pipeline/output",
	}, resolved.Params)
	assert.Equal(t, `base_dir="/tmp/work/pipeline" output_dir="/tmp/work/pipeline/output"`, resolved.DefaultParams)
}

func TestResolveRuntimeParams_OverrideBeatsEvalAndFeedsLaterParams(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: runtime-eval-override
params:
  - name: base_dir
    eval: "/default/base"
  - name: output_dir
    eval: "$base_dir/output"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	resolved, err := ResolveRuntimeParams(context.Background(), dag, "base_dir=/custom/base", ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"base_dir=/custom/base",
		"output_dir=/custom/base/output",
	}, resolved.Params)
	assert.Equal(t, `base_dir="/custom/base" output_dir="/custom/base/output"`, resolved.DefaultParams)
	assert.JSONEq(t, `{"base_dir":"/custom/base","output_dir":"/custom/base/output"}`, resolved.ParamsJSON)
}

func TestResolveRuntimeParams_RejectsUnknownNamedParam_InlineTyped(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: reject-unknown
params:
  - name: region
    type: string
    required: true
steps:
  - name: echo
    run: echo "$region"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	_, err = ResolveRuntimeParams(context.Background(), dag, "region=us-west-2 regoin=typo", ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "regoin")
}

func TestResolveRuntimeParams_AcceptsKnownNamedParam_InlineTyped(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: accept-known
params:
  - name: region
    type: string
    required: true
steps:
  - name: echo
    run: echo "$region"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	resolved, err := ResolveRuntimeParams(context.Background(), dag, "region=us-west-2", ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"region=us-west-2"}, resolved.Params)
}

func TestResolveRuntimeParams_AcceptsWebhookPayload_InlineTyped(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: accept-webhook-payload
params:
  - name: idea
    type: string
    required: true
steps:
  - name: echo
    run: echo "$idea"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	resolved, err := ResolveRuntimeParams(
		context.Background(),
		dag,
		`idea=ship WEBHOOK_PAYLOAD="{\"event\":\"push\"}"`,
		ResolveRuntimeParamsOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"idea=ship",
		`WEBHOOK_PAYLOAD={"event":"push"}`,
	}, resolved.Params)
	assert.JSONEq(t, `{"idea":"ship","WEBHOOK_PAYLOAD":"{\"event\":\"push\"}"}`, resolved.ParamsJSON)
}

func TestResolveRuntimeParams_AcceptsWebhookHeaders_InlineTyped(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: accept-webhook-headers
params:
  - name: idea
    type: string
    required: true
steps:
  - name: echo
    run: echo "$idea"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	resolved, err := ResolveRuntimeParams(
		context.Background(),
		dag,
		`idea=ship WEBHOOK_HEADERS="{\"x-github-event\":[\"push\"]}"`,
		ResolveRuntimeParamsOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"idea=ship",
		`WEBHOOK_HEADERS={"x-github-event":["push"]}`,
	}, resolved.Params)
	assert.JSONEq(t, `{"idea":"ship","WEBHOOK_HEADERS":"{\"x-github-event\":[\"push\"]}"}`, resolved.ParamsJSON)
}

func TestResolveLegacyRuntimePairs_InternalOverridesLastWriteWins(t *testing.T) {
	t.Parallel()

	pairs, err := resolveLegacyRuntimePairs(
		nil,
		nil,
		`WEBHOOK_PAYLOAD=first`,
		[]string{`WEBHOOK_PAYLOAD=second`},
	)
	require.NoError(t, err)
	assert.Equal(t, []paramPair{
		{Name: "WEBHOOK_PAYLOAD", Value: "second"},
	}, pairs)
}

func TestResolveLegacyRuntimePairs_WebhookHeadersInternalOverridesLastWriteWins(t *testing.T) {
	t.Parallel()

	pairs, err := resolveLegacyRuntimePairs(
		nil,
		nil,
		`WEBHOOK_HEADERS=first`,
		[]string{`WEBHOOK_HEADERS=second`},
	)
	require.NoError(t, err)
	assert.Equal(t, []paramPair{
		{Name: "WEBHOOK_HEADERS", Value: "second"},
	}, pairs)
}

func TestSplitPassThroughOverridePairs_DeclaredWebhookPayloadStaysUserParam(t *testing.T) {
	t.Parallel()

	userPairs, passThroughPairs := splitPassThroughOverridePairs(
		[]paramPair{{Name: "WEBHOOK_PAYLOAD", Value: "42"}},
		map[string]struct{}{"WEBHOOK_PAYLOAD": {}},
		nil,
	)

	assert.Equal(t, []paramPair{{Name: "WEBHOOK_PAYLOAD", Value: "42"}}, userPairs)
	assert.Empty(t, passThroughPairs)
}

func TestSplitPassThroughOverridePairs_DeclaredWebhookHeadersStaysUserParam(t *testing.T) {
	t.Parallel()

	userPairs, passThroughPairs := splitPassThroughOverridePairs(
		[]paramPair{{Name: "WEBHOOK_HEADERS", Value: "42"}},
		map[string]struct{}{"WEBHOOK_HEADERS": {}},
		nil,
	)

	assert.Equal(t, []paramPair{{Name: "WEBHOOK_HEADERS", Value: "42"}}, userPairs)
	assert.Empty(t, passThroughPairs)
}

func TestResolveRuntimeParams_RejectsUnknownNamedParam_LegacyNamed(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: reject-unknown-legacy
params:
  - region: us-east-1
steps:
  - name: echo
    run: echo "$region"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	_, err = ResolveRuntimeParams(context.Background(), dag, "region=us-west-2 regoin=typo", ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "regoin")
}

func TestResolveRuntimeParams_AcceptsAnythingWhenNoParamsDeclared(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: no-params
steps:
  - name: echo
    run: echo hello
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	resolved, err := ResolveRuntimeParams(context.Background(), dag, "foo=bar baz=qux", ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.NotNil(t, resolved)
}

// A run records the params declared by the base config next to the DAG's own,
// and queued dispatch and retry pass that list back when rebuilding the DAG.
func TestLoadYAML_BaseConfigParamOverride(t *testing.T) {
	t.Parallel()

	baseConfig := []byte(`
params:
  - name: DAY_OF_WEEK
    type: integer
    default: 1
`)

	schemaPath := filepath.Join(t.TempDir(), "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "properties": {
    "BLOCKLIST_FN": {"type": "string", "default": "et_block.netset"}
  },
  "additionalProperties": false
}`), 0o600))

	tests := []struct {
		name   string
		params string
	}{
		{
			name: "InlineDefinition",
			params: `
  - name: BLOCKLIST_FN
    default: et_block.netset`,
		},
		{
			name: "LegacyNamed",
			params: `
  - BLOCKLIST_FN: et_block.netset`,
		},
		{
			name: "InlineSchema",
			params: `
  type: object
  properties:
    BLOCKLIST_FN:
      type: string
      default: et_block.netset
  additionalProperties: false`,
		},
		{
			name: "ExternalSchema",
			params: `
  schema: ` + schemaPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			yaml := []byte("name: base-param-override\nparams:" + tt.params + "\n")

			dag, err := LoadYAML(context.Background(), yaml,
				WithBaseConfigContent(baseConfig),
				WithParams("DAY_OF_WEEK=3 BLOCKLIST_FN=custom.netset"),
			)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"DAY_OF_WEEK=3", "BLOCKLIST_FN=custom.netset"}, dag.Params)

			_, err = LoadYAML(context.Background(), yaml,
				WithBaseConfigContent(baseConfig),
				WithParams("DAY_OF_WEEK=3 regoin=typo"),
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "regoin")
		})
	}
}

// A DAG schema that declares a base config param without a default still
// validates overrides of it.
func TestLoadYAML_DAGSchemaValidatesBaseConfigParam(t *testing.T) {
	t.Parallel()

	baseConfig := []byte(`
params:
  - COUNT: 1
`)

	schema := `{
  "type": "object",
  "properties": {"COUNT": {"type": "integer"}},
  "required": ["COUNT"]
}`
	schemaPath := filepath.Join(t.TempDir(), "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(schema), 0o600))

	tests := []struct {
		name   string
		params string
	}{
		{
			name: "InlineSchema",
			params: `
  type: object
  properties:
    COUNT:
      type: integer
  required: [COUNT]`,
		},
		{
			name: "ExternalSchema",
			params: `
  schema: ` + schemaPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			yaml := []byte("name: schema-base-param\nparams:" + tt.params + "\n")

			dag, err := LoadYAML(context.Background(), yaml,
				WithBaseConfigContent(baseConfig),
				WithParams("COUNT=2"),
			)
			require.NoError(t, err)
			assert.Equal(t, []string{"COUNT=2"}, dag.Params)

			_, err = LoadYAML(context.Background(), yaml,
				WithBaseConfigContent(baseConfig),
				WithParams("COUNT=abc"),
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "COUNT")
		})
	}
}

// An open schema keeps explicit values outside its properties. An override of
// such a value replaces it even when the base config declares the same name.
func TestLoadYAML_SchemaValueOfBaseConfigParam(t *testing.T) {
	t.Parallel()

	baseConfig := []byte(`
params:
  - DAY_OF_WEEK: 1
`)

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "properties": {"BLOCKLIST_FN": {"type": "string", "default": "et_block.netset"}},
  "additionalProperties": true
}`), 0o600))

	yaml := []byte(`
name: schema-value-base-param
params:
  schema: ` + schemaPath + `
  values:
    DAY_OF_WEEK: "5"
`)

	dag, err := LoadYAML(context.Background(), yaml,
		WithBaseConfigContent(baseConfig),
		WithParams("DAY_OF_WEEK=3"),
	)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"BLOCKLIST_FN=et_block.netset", "DAY_OF_WEEK=3"}, dag.Params)
}

func TestResolveRuntimeParams_RejectsUnknownViaJSON_InlineTyped(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: reject-json-unknown
params:
  - name: region
    type: string
    required: true
steps:
  - name: echo
    run: echo "$region"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yaml

	_, err = ResolveRuntimeParams(context.Background(), dag, `{"region":"us-west-2","regoin":"typo"}`, ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "regoin")
}

func TestResolveRuntimeParams_ExternalSchemaRejectsUnknownWhenForbidden(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "properties": {
    "region": {
      "type": "string"
    }
  },
  "required": ["region"],
  "additionalProperties": false
}`), 0o600))

	dagPath := filepath.Join(dir, "external-reject.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: external-reject
params:
  schema: params.schema.json
`), 0o600))

	dag, err := Load(context.Background(), dagPath, WithoutEval())
	require.NoError(t, err)

	_, err = ResolveRuntimeParams(context.Background(), dag, "region=us-west-2 regoin=typo", ResolveRuntimeParamsOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "regoin")
}

func TestResolveRuntimeParams_ExternalSchemaAllowsExtraWhenPermitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "properties": {
    "region": {
      "type": "string"
    }
  },
  "required": ["region"],
  "additionalProperties": true
}`), 0o600))

	dagPath := filepath.Join(dir, "external-allow.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: external-allow
params:
  schema: params.schema.json
`), 0o600))

	dag, err := Load(context.Background(), dagPath, WithoutEval())
	require.NoError(t, err)

	resolved, err := ResolveRuntimeParams(context.Background(), dag, "region=us-west-2 extra=ok", ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.NotNil(t, resolved)
}

func TestResolveRuntimeParams_ExternalSchemaAcceptsWebhookPayloadWhenClosed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "properties": {
    "idea": {
      "type": "string"
    }
  },
  "required": ["idea"],
  "additionalProperties": false
}`), 0o600))

	dagPath := filepath.Join(dir, "external-webhook-payload.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: external-webhook-payload
params:
  schema: params.schema.json
`), 0o600))

	dag, err := Load(context.Background(), dagPath, WithoutEval())
	require.NoError(t, err)

	resolved, err := ResolveRuntimeParams(
		context.Background(),
		dag,
		`idea=ship WEBHOOK_PAYLOAD="{\"event\":\"push\"}"`,
		ResolveRuntimeParamsOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"idea=ship",
		`WEBHOOK_PAYLOAD={"event":"push"}`,
	}, resolved.Params)
	assert.JSONEq(t, `{"idea":"ship","WEBHOOK_PAYLOAD":"{\"event\":\"push\"}"}`, resolved.ParamsJSON)
}

func TestToFloat64_RejectsUnsafeIntegerPrecision(t *testing.T) {
	t.Parallel()

	_, err := toFloat64(int64(maxSafeFloat64Integer + 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "safe range")
}

func TestInt64FromUint64_ConvertsDirectly(t *testing.T) {
	t.Parallel()

	value, err := int64FromUint64(maxInt64AsUint)
	require.NoError(t, err)
	assert.Equal(t, int64(maxInt64AsUint), value)
}
