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

// A value the schema does not type is decoded only to validate it, so the run,
// a rebuild from its saved params, and the start form all keep the text the
// caller supplied.
func TestLoad_ExternalSchemaKeepsUntypedValueText(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "params.schema.json"), []byte(`{
  "type": "object",
  "properties": {"BLOCKLIST_FN": {"type": "string", "default": "et_block.netset"}},
  "additionalProperties": true
}`), 0o600))
	dagPath := filepath.Join(dir, "untyped-text.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: untyped-text
params:
  schema: params.schema.json
  values:
    VERSION: "1.10"
    RATIOS: "[1.10, 2.50]"
`), 0o600))

	dag, err := Load(context.Background(), dagPath, WithParams("CUSTOMER_ID=12345678901234567"))
	require.NoError(t, err)
	want := []string{
		"BLOCKLIST_FN=et_block.netset",
		"CUSTOMER_ID=12345678901234567",
		"RATIOS=[1.10, 2.50]",
		"VERSION=1.10",
	}
	assert.Equal(t, want, dag.Params)

	rebuilt, err := ResolveRuntimeParams(context.Background(), dag, dag.Params, ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.Equal(t, want, rebuilt.Params)

	defaults := map[string]any{}
	for _, def := range dag.ParamDefs {
		defaults[def.Name] = def.Default
	}
	assert.Equal(t, "1.10", defaults["VERSION"])
	assert.Equal(t, "[1.10, 2.50]", defaults["RATIOS"])
}

// Defaults the schema fills into an untyped object value reach the run without
// changing the numbers the caller wrote.
func TestLoad_ExternalSchemaFillsDefaultsIntoUntypedValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "params.schema.json"), []byte(`{
  "type": "object",
  "properties": {
    "CONFIG": {
      "type": "object",
      "properties": {"retries": {"type": "integer", "default": 2}}
    }
  }
}`), 0o600))
	dagPath := filepath.Join(dir, "untyped-defaults.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: untyped-defaults
params:
  schema: params.schema.json
  values:
    CONFIG: '{"ratio": 1.10, "id": 12345678901234567}'
`), 0o600))

	dag, err := Load(context.Background(), dagPath)
	require.NoError(t, err)
	assert.Equal(t, []string{`CONFIG={"id":12345678901234567,"ratio":1.10,"retries":2}`}, dag.Params)

	resolved, err := ResolveRuntimeParams(context.Background(), dag, []string{`CONFIG={"ratio": 0.50}`}, ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{`CONFIG={"ratio":0.50,"retries":2}`}, resolved.Params)
}

// Scheduled, queued, and retried runs rebuild a DAG from the params its run
// saved, which include the ones the base config declares. The rebuild must
// accept them whichever way the DAG declares its own params, keep the saved
// values, and list each name once in the order of the first build.
func TestResolveRuntimeParams_BaseConfigParams(t *testing.T) {
	t.Parallel()

	baseConfig := []byte(`
params:
  - name: DAY_OF_WEEK
    type: integer
    default: 3
  - name: BLOCKLIST_FN
    default: base.netset
`)
	schema := []byte(`{
  "type": "object",
  "properties": {
    "BLOCKLIST_FN": {"type": "string", "default": "et_block.netset"},
    "IPSET": {"type": "string", "default": "et_block"}
  },
  "additionalProperties": false
}`)

	tests := []struct {
		name   string
		params string
	}{
		{
			name: "InlineDefinition",
			params: `
  - name: BLOCKLIST_FN
    default: et_block.netset
  - name: IPSET
    default: et_block`,
		},
		{
			name: "LegacyNamed",
			params: `
  - BLOCKLIST_FN: et_block.netset
  - IPSET: et_block`,
		},
		{
			name: "InlineSchema",
			params: `
  type: object
  additionalProperties: false
  properties:
    BLOCKLIST_FN:
      type: string
      default: et_block.netset
    IPSET:
      type: string
      default: et_block`,
		},
		{
			name: "ExternalSchema",
			params: `
  schema: params.schema.json`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "params.schema.json"), schema, 0o600))
			dagPath := filepath.Join(dir, "refresh.yaml")
			require.NoError(t, os.WriteFile(dagPath, []byte("params:"+tt.params+"\nsteps:\n  - run: echo ok\n"), 0o600))

			dag, err := Load(ctx, dagPath, WithBaseConfigContent(baseConfig))
			require.NoError(t, err)
			assert.Equal(t, []string{"DAY_OF_WEEK=3", "BLOCKLIST_FN=et_block.netset", "IPSET=et_block"}, dag.Params)

			saved := []string{"DAY_OF_WEEK=5", "BLOCKLIST_FN=saved.netset", "IPSET=et_block"}
			resolved, err := ResolveRuntimeParams(ctx, dag, saved, ResolveRuntimeParamsOptions{})
			require.NoError(t, err)
			assert.Equal(t, saved, resolved.Params)

			rebuilt, err := RebuildFromYAML(ctx, resolved)
			require.NoError(t, err)
			assert.Equal(t, saved, rebuilt.Params)

			_, err = ResolveRuntimeParams(ctx, dag, []string{"TYPO=1"}, ResolveRuntimeParamsOptions{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "TYPO")
		})
	}
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
