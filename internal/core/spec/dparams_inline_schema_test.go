// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- isInlineJSONSchema predicate ---

// U1: nil input is not an inline schema
func TestIsInlineJSONSchema_NilReturnsFalse(t *testing.T) {
	t.Parallel()
	assert.False(t, isInlineJSONSchema(nil))
}

// U2: string input is not an inline schema
func TestIsInlineJSONSchema_StringReturnsFalse(t *testing.T) {
	t.Parallel()
	assert.False(t, isInlineJSONSchema("type: object"))
}

// U3: slice input is not an inline schema
func TestIsInlineJSONSchema_SliceReturnsFalse(t *testing.T) {
	t.Parallel()
	assert.False(t, isInlineJSONSchema([]any{"foo=bar"}))
}

// U4: map without properties key is not an inline schema
func TestIsInlineJSONSchema_MapWithoutPropertiesReturnsFalse(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type": "object",
	}
	assert.False(t, isInlineJSONSchema(input))
}

// U5: map with properties as a string is not an inline schema
func TestIsInlineJSONSchema_MapWithPropertiesAsStringReturnsFalse(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":       "object",
		"properties": "not-a-map",
	}
	assert.False(t, isInlineJSONSchema(input))
}

func TestBuildInlineSchemaParamPlan_RejectsPropertiesArrayShape(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-properties-array
params:
  type: object
  properties:
    - name: region
      type: string
  required:
    - region
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "params")
	assert.Contains(t, err.Error(), "properties must be an object")
}

func TestBuildInlineSchemaParamPlan_SkipSchemaValidationStillRejectsPropertiesArrayShape(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-properties-array-skip
params:
  type: object
  properties:
    - name: region
      type: string
`)

	_, err := LoadYAML(context.Background(), yaml, SkipSchemaValidation(), WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "params")
	assert.Contains(t, err.Error(), "properties must be an object")
}

func TestBuildInlineSchemaParamPlan_RejectsPropertiesNullShape(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-properties-null
params:
  type: object
  properties: null
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "params")
	assert.Contains(t, err.Error(), "properties must be an object")
}

func TestBuildInlineSchemaParamPlan_SkipSchemaValidationStillRejectsPropertiesNullShape(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-properties-null-skip
params:
  type: object
  properties: null
`)

	_, err := LoadYAML(context.Background(), yaml, SkipSchemaValidation(), WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "params")
	assert.Contains(t, err.Error(), "properties must be an object")
}

// U6: map with properties as a map IS an inline schema
func TestIsInlineJSONSchema_MapWithPropertiesAsMapReturnsTrue(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"batch_size": map[string]any{"type": "integer", "default": 10},
		},
	}
	assert.True(t, isInlineJSONSchema(input))
}

// U7: external schema format (has schema key with file path) is NOT an inline schema
func TestIsInlineJSONSchema_ExternalSchemaRefReturnsFalse(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"schema": "/path/to/schema.json",
		"values": map[string]any{},
	}
	assert.False(t, isInlineJSONSchema(input))
}

// U8: map with properties and other standard JSON Schema fields is an inline schema
func TestIsInlineJSONSchema_MapWithPropertiesAndRequiredReturnsTrue(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"start_date": map[string]any{"type": "string"},
		},
		"required": []any{"start_date"},
	}
	assert.True(t, isInlineJSONSchema(input))
}

// U9: map with properties but without type=object remains a legacy params map
func TestIsInlineJSONSchema_MapWithPropertiesWithoutObjectTypeReturnsFalse(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"properties": map[string]any{
			"foo": "bar",
		},
	}
	assert.False(t, isInlineJSONSchema(input))
}

// --- buildInlineSchemaParamPlan ---

// U10: defaults are extracted into entries and paramDefs
func TestBuildInlineSchemaParamPlan_ExtractsDefaults(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-defaults
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
    debug:
      type: boolean
      default: false
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 2)

	assert.Equal(t, "batch_size", dag.ParamDefs[0].Name)
	assert.Equal(t, core.ParamDefTypeInteger, dag.ParamDefs[0].Type)
	assert.Equal(t, float64(10), dag.ParamDefs[0].Default)

	assert.Equal(t, "debug", dag.ParamDefs[1].Name)
	assert.Equal(t, core.ParamDefTypeBoolean, dag.ParamDefs[1].Type)
	assert.Equal(t, false, dag.ParamDefs[1].Default)

	assert.Contains(t, dag.DefaultParams, `batch_size="10"`)
	assert.Contains(t, dag.DefaultParams, `debug="false"`)
}

// U11: required fields appear in paramDefs
func TestBuildInlineSchemaParamPlan_RequiredFieldInParamDefs(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-required
params:
  type: object
  properties:
    start_date:
      type: string
      format: date
    batch_size:
      type: integer
      default: 10
  required:
    - start_date
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 2)

	// Find start_date paramDef
	var startDateDef *core.ParamDef
	for i := range dag.ParamDefs {
		if dag.ParamDefs[i].Name == "start_date" {
			startDateDef = &dag.ParamDefs[i]
		}
	}
	require.NotNil(t, startDateDef)
	assert.True(t, startDateDef.Required)
	assert.Nil(t, startDateDef.Default)
}

// U12: required field missing causes validation failure at runtime
func TestBuildInlineSchemaParamPlan_RequiredMissingFails(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-required-fail
params:
  type: object
  properties:
    start_date:
      type: string
  required:
    - start_date
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval(), WithParams(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start_date")
}

// U13: runtime override with valid typed value is accepted
func TestBuildInlineSchemaParamPlan_RuntimeOverrideAccepted(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-override
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
    debug:
      type: boolean
      default: false
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval(), WithParams("batch_size=50 debug=true"))
	require.NoError(t, err)
	assert.Contains(t, dag.Params, "batch_size=50")
	assert.Contains(t, dag.Params, "debug=true")
}

// U14: runtime override with wrong type is rejected
func TestBuildInlineSchemaParamPlan_RuntimeOverrideInvalidTypeRejected(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-type-reject
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval(), WithParams("batch_size=not-an-integer"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "batch_size")
}

func TestBuildInlineSchemaParamPlan_ExposesRenderableParamSchema(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
name: inline-schema-ui
params:
  type: object
  properties:
    region:
      title: Which compute location?
      description: Pick the AWS region to run in.
      oneOf:
        - const: us-east-1
          title: US East 1
        - const: us-west-2
          title: US West 2
    count:
      type: integer
      default: 3
      minimum: 1
  required:
    - region
`)

	dag, err := LoadYAML(context.Background(), yamlData, WithoutEval())
	require.NoError(t, err)
	require.NotEmpty(t, dag.ParamSchema)
	assert.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "region": {
	      "type": "string",
	      "title": "Which compute location?",
	      "description": "Pick the AWS region to run in.",
	      "oneOf": [
	        {"const": "us-east-1", "title": "US East 1", "type": "string"},
	        {"const": "us-west-2", "title": "US West 2", "type": "string"}
	      ]
	    },
	    "count": {
	      "type": "integer",
	      "default": 3,
	      "minimum": 1
	    }
	  },
	  "required": ["region"]
	}`, string(dag.ParamSchema))
}

func TestBuildInlineSchemaParamPlan_OmitsUnsupportedNestedParamSchema(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
name: inline-schema-nested-ui
params:
  type: object
  properties:
    settings:
      type: object
      properties:
        retries:
          type: integer
`)

	dag, err := LoadYAML(context.Background(), yamlData, WithoutEval())
	require.NoError(t, err)
	assert.Empty(t, dag.ParamSchema)
}

// U15: positional parameters are rejected for inline schema
func TestBuildInlineSchemaParamPlan_PositionalParamRejected(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-positional-reject
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval(), WithParams("50"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positional")
}

// U16: readonly field is stripped at top level (does not cause a parse error)
func TestBuildInlineSchemaParamPlan_ReadonlyFieldStripped(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-readonly
params:
  type: object
  readOnly: true
  properties:
    batch_size:
      type: integer
      default: 10
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 1)
	assert.Equal(t, "batch_size", dag.ParamDefs[0].Name)
}

// U17: full inline schema from issue #1182 example is accepted
func TestBuildInlineSchemaParamPlan_Issue1182Example(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: issue1182-example
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
    start_date:
      type: string
      format: date
    debug:
      type: boolean
      default: false
  required:
    - start_date
`)

	// Metadata load (no runtime params) — should succeed since required has no default
	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	assert.Equal(t, `batch_size="10" debug="false"`, dag.DefaultParams)

	// Runtime load with required param provided
	dag, err = LoadYAML(context.Background(), yaml, WithoutEval(), WithParams("start_date=2026-01-15"))
	require.NoError(t, err)
	assert.Contains(t, dag.Params, "start_date=2026-01-15")
	assert.Contains(t, dag.Params, "batch_size=10")
	assert.Contains(t, dag.Params, "debug=false")
}

// U18: string type property with no default produces no entry in DefaultParams
func TestBuildInlineSchemaParamPlan_StringNoDefaultProducesNoEntry(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-no-default
params:
  type: object
  properties:
    name:
      type: string
      description: The name
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 1)
	assert.Equal(t, "name", dag.ParamDefs[0].Name)
	assert.Nil(t, dag.ParamDefs[0].Default)
	assert.Empty(t, dag.DefaultParams)
}

// U19: unknown named override is rejected when schema uses additionalProperties: false
func TestBuildInlineSchemaParamPlan_UnknownNamedOverrideRejectedWithAdditionalPropertiesFalse(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-unknown-param
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
  additionalProperties: false
`)

	_, err := LoadYAML(context.Background(), yaml, WithoutEval(), WithParams("unknown_param=foo"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown_param")
}

// U20: ParamsJSON is produced correctly
func TestBuildInlineSchemaParamPlan_ParamsJSONCorrect(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
name: inline-schema-json
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
    debug:
      type: boolean
      default: false
`)

	dag, err := LoadYAML(context.Background(), yaml, WithoutEval(), WithParams("batch_size=20 debug=true"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"batch_size":"20","debug":"true"}`, dag.ParamsJSON)
}

// C1: SkipSchemaValidation + inline schema + runtime params must not panic.
// rebuildDAGFromYAML in cmd/helper.go always combines SkipSchemaValidation with
// WithParams, so a nil plan.schema would cause a nil-pointer panic on restart/retry.
func TestBuildInlineSchemaParamPlan_SkipSchemaValidationWithParamsNoPanic(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
name: inline-schema-skip-validation
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
`)

	// This combination mirrors rebuildDAGFromYAML: SkipSchemaValidation() + WithParams().
	// Before the fix this panicked with a nil-pointer dereference in validateSchemaMap.
	dag, err := LoadYAML(context.Background(), yamlData, SkipSchemaValidation(), WithParams("batch_size=50"))
	require.NoError(t, err)
	assert.Contains(t, dag.Params, "batch_size=50")
}

// C2: SkipSchemaValidation still preserves inline-schema metadata.
func TestBuildInlineSchemaParamPlan_SkipSchemaValidationPreservesMetadata(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
name: inline-schema-skip-metadata
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
    debug:
      type: boolean
      default: false
`)

	dag, err := LoadYAML(context.Background(), yamlData, SkipSchemaValidation())
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 2)
	assert.Equal(t, `batch_size="10" debug="false"`, dag.DefaultParams)
}

// C3: SkipSchemaValidation still rejects positional params for inline schema.
func TestBuildInlineSchemaParamPlan_SkipSchemaValidationRejectsPositionalParams(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
name: inline-schema-skip-positional
params:
  type: object
  properties:
    batch_size:
      type: integer
`)

	_, err := LoadYAML(context.Background(), yamlData, SkipSchemaValidation(), WithParams("50"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positional")
}

// C4: SkipSchemaValidation keeps additionalProperties false behavior for overrides.
func TestBuildInlineSchemaParamPlan_SkipSchemaValidationRejectsUnknownNamedOverrideWhenClosed(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
name: inline-schema-skip-closed
params:
  type: object
  properties:
    batch_size:
      type: integer
  additionalProperties: false
`)

	_, err := LoadYAML(context.Background(), yamlData, SkipSchemaValidation(), WithParams("unknown=1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

// C5: legacy params maps with a properties key remain legacy unless type=object is present.
func TestBuildInlineSchemaParamPlan_LegacyPropertiesMapRemainsLegacy(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
name: legacy-properties-map
params:
  properties:
    foo: bar
  region: us-west-2
`)

	dag, err := LoadYAML(context.Background(), yamlData, WithoutEval())
	require.NoError(t, err)
	assert.Equal(t, []string{"properties=map[foo:bar]", "region=us-west-2"}, dag.Params)
	assert.Equal(t, `properties="map[foo:bar]" region="us-west-2"`, dag.DefaultParams)
}

func TestSchemaDisallowsAdditionalProperties(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		root *jsonschema.Schema
		want bool
	}{
		{
			name: "NilRoot",
			root: nil,
			want: false,
		},
		{
			name: "NoAdditionalProperties",
			root: &jsonschema.Schema{},
			want: false,
		},
		{
			name: "ClosedSchema",
			root: &jsonschema.Schema{
				AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
			},
			want: true,
		},
		{
			name: "AdditionalPropertiesTrueLikeEmptySchema",
			root: &jsonschema.Schema{
				AdditionalProperties: &jsonschema.Schema{},
			},
			want: false,
		},
		{
			name: "AdditionalPropertiesWithConstraint",
			root: &jsonschema.Schema{
				AdditionalProperties: &jsonschema.Schema{Type: "string"},
			},
			want: false,
		},
		{
			name: "NotWithNonEmptySchema",
			root: &jsonschema.Schema{
				AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{Type: "string"}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, schemaDisallowsAdditionalProperties(tt.root))
		})
	}
}
