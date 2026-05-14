// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInlineParamDefs_MetadataAndExecution(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-params
params:
  - name: region
    description: AWS region
    type: string
    enum: [us-east-1, us-west-2]
    required: true
  - name: instance_count
    default: 3
    type: integer
    minimum: 1
    maximum: 10
  - name: debug
    default: false
    type: boolean
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 3)
	assert.Equal(t, "region", dag.ParamDefs[0].Name)
	assert.Equal(t, core.ParamDefTypeString, dag.ParamDefs[0].Type)
	assert.True(t, dag.ParamDefs[0].Required)
	assert.Nil(t, dag.ParamDefs[0].Default)
	assert.Equal(t, int64(3), dag.ParamDefs[1].Default)
	assert.Equal(t, false, dag.ParamDefs[2].Default)
	assert.Equal(t, `instance_count="3" debug="false"`, dag.DefaultParams)
	assert.JSONEq(t, `{"instance_count":"3","debug":"false"}`, dag.ParamsJSON)

	_, err = LoadYAML(context.Background(), yaml, WithoutEval(), WithParams(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region")

	dag, err = LoadYAML(
		context.Background(),
		yaml,
		WithoutEval(),
		WithParams(`[{"region":"us-west-2"},{"instance_count":"5"},{"debug":"true"}]`),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"region=us-west-2",
		"instance_count=5",
		"debug=true",
	}, dag.Params)
	assert.JSONEq(t, `[{"region":"us-west-2"},{"instance_count":"5"},{"debug":"true"}]`, dag.ParamsJSON)
}

func TestInlineParamDefs_MixedLegacyAndInline(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: mixed-params
params:
  - name: environment
    type: string
    enum: [dev, staging, prod]
    default: staging
  - TAG: latest
  - DRY_RUN: "true"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 3)

	assert.Equal(t, "environment", dag.ParamDefs[0].Name)
	assert.Equal(t, core.ParamDefTypeString, dag.ParamDefs[0].Type)
	assert.Equal(t, "staging", dag.ParamDefs[0].Default)

	assert.Equal(t, "TAG", dag.ParamDefs[1].Name)
	assert.Equal(t, core.ParamDefTypeString, dag.ParamDefs[1].Type)
	assert.Equal(t, "latest", dag.ParamDefs[1].Default)

	assert.Equal(t, "DRY_RUN", dag.ParamDefs[2].Name)
	assert.Equal(t, "true", dag.ParamDefs[2].Default)
	assert.Equal(t, `environment="staging" TAG="latest" DRY_RUN="true"`, dag.DefaultParams)
}

func TestInlineParamDefs_DescriptionOnlyDefaultsToString(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: described-inline-param
params:
  - name: notes
    description: Free-form operator notes
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 1)
	assert.Equal(t, "notes", dag.ParamDefs[0].Name)
	assert.Equal(t, core.ParamDefTypeString, dag.ParamDefs[0].Type)
	assert.Equal(t, "Free-form operator notes", dag.ParamDefs[0].Description)
	assert.Nil(t, dag.ParamDefs[0].Default)
	assert.Empty(t, dag.Params)
	assert.Empty(t, dag.DefaultParams)
}

func TestInlineParamDefs_RejectDuplicateNames(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: duplicate-inline-params
params:
  - region=us-east-1
  - name: region
    type: string
    default: us-west-2
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate parameter name "region"`)
}

func TestInlineParamDefs_RejectDefaultPatternMismatch(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: invalid-inline-default
params:
  - name: project_name
    type: string
    default: INVALID_NAME
    pattern: "^[a-z][a-z0-9-]*$"
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `default does not match pattern`)
}

func TestInlineParamDefs_RejectNonStringDescription(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: invalid-inline-description
params:
  - name: notes
    description: 123
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `description must be a string`)
}

func TestInlineParamDefs_EvalResolvesDuringNormalBuild(t *testing.T) {
	t.Setenv("WORK_DIR", "/tmp/work")
	ctx := context.Background()
	if runtime.GOOS == "windows" {
		ctx = config.WithConfig(ctx, &config.Config{
			Core: config.Core{DefaultShell: "cmd"},
		})
	}

	yaml := []byte(`
name: eval-params
params:
  - name: base_dir
    eval: "$WORK_DIR/pipeline"
  - name: output_dir
    eval: "$base_dir/output"
  - name: parallelism
    type: integer
    eval: "` + "`echo 7`" + `"
`)

	dag, err := LoadYAML(ctx, yaml)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"base_dir=/tmp/work/pipeline",
		"output_dir=/tmp/work/pipeline/output",
		"parallelism=7",
	}, dag.Params)
	assert.Equal(t, `base_dir="/tmp/work/pipeline" output_dir="/tmp/work/pipeline/output" parallelism="7"`, dag.DefaultParams)
	assert.JSONEq(t, `{"base_dir":"/tmp/work/pipeline","output_dir":"/tmp/work/pipeline/output","parallelism":"7"}`, dag.ParamsJSON)
	require.Len(t, dag.ParamDefs, 3)
	assert.Nil(t, dag.ParamDefs[0].Default)
	assert.Nil(t, dag.ParamDefs[1].Default)
	assert.Nil(t, dag.ParamDefs[2].Default)
}

func TestInlineParamDefs_EvalOverrideWinsAndFeedsLaterParams(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: eval-override
params:
  - name: base_dir
    eval: "/default/base"
  - name: output_dir
    eval: "$base_dir/output"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithParams(`base_dir=/custom/base`))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"base_dir=/custom/base",
		"output_dir=/custom/base/output",
	}, dag.Params)
	assert.Equal(t, `base_dir="/custom/base" output_dir="/custom/base/output"`, dag.DefaultParams)
	assert.JSONEq(t, `{"base_dir":"/custom/base","output_dir":"/custom/base/output"}`, dag.ParamsJSON)
}

func TestInlineParamDefs_EvalExplicitEmptyOverrideWins(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: eval-empty-override
params:
  - name: base_dir
    eval: "/default/base"
  - name: output_dir
    eval: "$base_dir/output"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithParams(`base_dir=""`))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"base_dir=",
		"output_dir=/output",
	}, dag.Params)
	assert.Equal(t, `base_dir="" output_dir="/output"`, dag.DefaultParams)
	assert.JSONEq(t, `{"base_dir":"","output_dir":"/output"}`, dag.ParamsJSON)
}

func TestInlineParamDefs_EvalOverrideDoesNotReexecuteUnchangedParams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell semantics")
	}

	countFile := filepath.Join(t.TempDir(), "eval-count")
	t.Setenv("EVAL_COUNT_FILE", countFile)

	yaml := []byte(`
name: eval-single-execution
params:
  - name: token
    eval: "` + "`sh -c 'n=0; [ -f $EVAL_COUNT_FILE ] && n=$(cat $EVAL_COUNT_FILE); n=$((n+1)); printf %s $n > $EVAL_COUNT_FILE; printf token-%s $n'`" + `"
  - name: mode
    default: default
`)

	dag, err := LoadYAML(context.Background(), yaml, WithParams(`mode=override`))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"token=token-1",
		"mode=override",
	}, dag.Params)
	assert.Equal(t, `token="token-1" mode="override"`, dag.DefaultParams)

	data, err := os.ReadFile(countFile)
	require.NoError(t, err)
	assert.Equal(t, "1", string(data))
}

func TestInlineParamDefs_EvalFailureFallsBackToDefault(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: eval-fallback
params:
  - name: run_date
    eval: "` + "`command_that_does_not_exist_12345`" + `"
    default: fallback-date
`)

	dag, err := LoadYAML(context.Background(), yaml)
	require.NoError(t, err)
	assert.Equal(t, []string{"run_date=fallback-date"}, dag.Params)
	assert.Equal(t, `run_date="fallback-date"`, dag.DefaultParams)
}

func TestInlineParamDefs_EvalFailureWithoutDefaultFails(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: eval-failure
params:
  - name: run_date
    eval: "` + "`command_that_does_not_exist_12345`" + `"
`)

	_, err := LoadYAML(context.Background(), yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `parameter "run_date" eval failed`)
}

func TestInlineParamDefs_EvalRespectsWithoutEval(t *testing.T) {
	t.Setenv("WORK_DIR", "/tmp/work")

	yaml := []byte(`
name: eval-noeval
params:
  - name: work_dir
    eval: "$WORK_DIR/pipeline"
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	assert.Empty(t, dag.Params)
	assert.Empty(t, dag.DefaultParams)
	assert.Empty(t, dag.ParamsJSON)
}

func TestInlineParamDefs_RejectNonStringEval(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: invalid-inline-eval
params:
  - name: run_date
    eval: 123
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `eval must be a string`)
}

func TestInlineParamDefs_RejectBlankEval(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: invalid-inline-eval
params:
  - name: run_date
    eval: "   "
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `eval must not be empty`)
}

func TestInlineParamDefs_RejectLegacyNestedMapSyntax(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: invalid-inline-shape
params:
  - region:
      type: string
      default: us-west-2
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must use object form with name")
	assert.Contains(t, err.Error(), "rewrite")
}

func TestInlineParamDefs_RejectNameOnlyEntry(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: invalid-inline-name-only
params:
  - name: foo
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `must define at least one field in addition to name`)
}

func TestLegacyParamsMap_AllowsSchemaKey(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: legacy-schema-key
params:
  schema: prod
  region: us
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	assert.Equal(t, []string{"region=us", "schema=prod"}, dag.Params)
	assert.Equal(t, `region="us" schema="prod"`, dag.DefaultParams)
}

func TestLegacyParamsMap_AllowsBooleanSchemaKeyWithoutValues(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: legacy-boolean-schema-key
params:
  schema: true
  debug: false
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	assert.Equal(t, []string{"debug=false", "schema=true"}, dag.Params)
	assert.Equal(t, `debug="false" schema="true"`, dag.DefaultParams)
}

func TestInlineParamDefs_RejectNegativeStringLengthConstraint(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: invalid-negative-length
params:
  - name: project
    type: string
    min_length: -1
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `min_length must be non-negative`)
}

func TestInlineParamDefs_LocalDAGYamlReload(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-subdag-parent
steps:
  - name: invoke-child
    action: dag.run
    with:
      dag: inline-subdag-child

---
name: inline-subdag-child
params:
  - name: region
    type: string
    enum: [us-east-1, us-west-2]
    required: true
  - name: count
    type: integer
    minimum: 1
    maximum: 10
    required: true
  - name: debug
    type: boolean
    required: true
steps:
  - name: shell-values
    run: echo "region=$region count=$count debug=$debug"
`)

	dir := t.TempDir()
	path := filepath.Join(dir, "inline-subdag.yaml")
	require.NoError(t, os.WriteFile(path, yaml, 0o600))

	dag, err := Load(context.Background(), path, WithoutEval())
	require.NoError(t, err)

	child, ok := dag.LocalDAGs["inline-subdag-child"]
	require.True(t, ok)
	require.NotEmpty(t, child.YamlData)

	tempFile, err := fileutil.CreateTempDAGFile("spec-tests", child.Name, child.YamlData)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(tempFile) })

	reloaded, err := Load(
		context.Background(),
		tempFile,
		WithoutEval(),
		WithName(child.Name),
		WithParams("region=us-west-2 count=5 debug=true"),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"region=us-west-2", "count=5", "debug=true"}, reloaded.Params)
}

func TestExternalSchemaParamDefs_MetadataAndExecution(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "properties": {
    "region": {
      "type": "string",
      "description": "Target region",
      "enum": ["us-east-1", "us-west-2"]
    },
    "count": {
      "type": "integer",
      "minimum": 1,
      "maximum": 10,
      "default": 3
    }
  },
  "required": ["region"]
}`), 0o600))

	yaml := []byte("name: schema-params\nparams:\n  schema: " + schemaPath + "\n")

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 2)
	assert.Equal(t, "count", dag.ParamDefs[0].Name)
	assert.Equal(t, float64(3), dag.ParamDefs[0].Default)
	assert.Equal(t, "region", dag.ParamDefs[1].Name)
	assert.True(t, dag.ParamDefs[1].Required)
	assert.Nil(t, dag.ParamDefs[1].Default)
	assert.Equal(t, `count="3"`, dag.DefaultParams)

	_, err = LoadYAML(context.Background(), yaml, WithoutEval(), WithParams(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region")

	dag, err = LoadYAML(context.Background(), yaml, WithoutEval(), WithParams("region=us-east-1 count=4"))
	require.NoError(t, err)
	assert.Equal(t, []string{"count=4", "region=us-east-1"}, dag.Params)
	assert.JSONEq(t, `{"region":"us-east-1","count":"4"}`, dag.ParamsJSON)
}
