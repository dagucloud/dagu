// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"strings"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishedOutputContractValidatePath(t *testing.T) {
	t.Parallel()

	t.Run("DescendsTypedLiteralMaps", func(t *testing.T) {
		t.Parallel()

		contract := publishedOutputContract{
			StepName: "build",
			Source:   "output",
			Keys: map[string]StepOutputEntry{
				"artifact": {
					HasValue: true,
					Value: map[string]map[string]string{
						"meta": {"name": "report"},
					},
				},
			},
		}

		assert.Equal(t, outputReferenceValid, contract.validatePath([]string{"artifact", "meta", "name"}))
		assert.Equal(t, outputReferenceInvalid, contract.validatePath([]string{"artifact", "meta", "missing"}))
	})

	t.Run("TreatsUnresolvedRefSchemaAsUnknown", func(t *testing.T) {
		t.Parallel()

		contract := publishedOutputContract{
			StepName: "build",
			Source:   "output_schema",
			Schema: map[string]any{
				"$ref": "#/$defs/BuildOutput",
			},
		}

		tassert := assert.New(t)
		tassert.Equal(outputReferenceUnknown, contract.validatePath([]string{"artifact"}))
	})

	t.Run("TreatsEmptyCompositionAsUnknown", func(t *testing.T) {
		t.Parallel()

		for name, schema := range map[string]map[string]any{
			"empty anyOf":     {"anyOf": []any{}},
			"empty oneOf":     {"oneOf": []any{}},
			"empty allOf":     {"allOf": []any{}},
			"non-array anyOf": {"anyOf": "not-an-array"},
			"non-array oneOf": {"oneOf": "not-an-array"},
			"non-array allOf": {"allOf": "not-an-array"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				contract := publishedOutputContract{
					StepName: "build",
					Source:   "output_schema",
					Schema:   schema,
				}

				assert.Equal(t, outputReferenceUnknown, contract.validatePath([]string{"artifact"}))
			})
		}
	})

	t.Run("AllOfInvalidBranchIsInvalid", func(t *testing.T) {
		t.Parallel()

		contract := publishedOutputContract{
			StepName: "build",
			Source:   "output_schema",
			Schema: map[string]any{
				"allOf": []any{
					map[string]any{
						"type":                 "object",
						"properties":           map[string]any{"artifact": map[string]any{"type": "string"}},
						"additionalProperties": false,
					},
					map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": false,
					},
				},
			},
		}

		assert.Equal(t, outputReferenceInvalid, contract.validatePath([]string{"artifact"}))
	})

	t.Run("ClosedSchemaWithPatternPropertiesIsUnknown", func(t *testing.T) {
		t.Parallel()

		contract := publishedOutputContract{
			StepName: "build",
			Source:   "output_schema",
			Schema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"patternProperties": map[string]any{
					"^x_": map[string]any{"type": "string"},
				},
			},
		}

		assert.Equal(t, outputReferenceUnknown, contract.validatePath([]string{"x_dynamic"}))
		assert.Equal(t, outputReferenceInvalid, contract.validatePath([]string{"dynamic"}))
	})

	t.Run("NestedLookupUnderScalarLiteralIsInvalid", func(t *testing.T) {
		t.Parallel()

		contract := publishedOutputContract{
			StepName: "build",
			Source:   "output",
			Keys: map[string]StepOutputEntry{
				"version": {HasValue: true, Value: "v1"},
			},
		}

		assert.Equal(t, outputReferenceInvalid, contract.validatePath([]string{"version", "major"}))
	})

	t.Run("NonObjectOutputSchemaRejectsFieldAccess", func(t *testing.T) {
		t.Parallel()

		contract := publishedOutputContract{
			StepName: "build",
			Source:   "output_schema",
			Schema:   map[string]any{"type": "string"},
		}

		assert.Equal(t, outputReferenceInvalid, contract.validatePath([]string{"field"}))
	})
}

func TestOutputReferencesDescendsTypedContainersViaFieldWalker(t *testing.T) {
	t.Parallel()

	type collectedReference struct {
		field string
		ref   cmnvalue.StepOutputReference
	}
	var refs []collectedReference
	dag := &DAG{Steps: []Step{{
		Name: "publish",
		StructuredOutput: map[string]StepOutputEntry{
			"payload": {
				HasValue: true,
				Value: []map[string]string{
					{"z": "${build.output.zed}"},
					{"a": "${build.output.alpha}"},
				},
			},
		},
	}}}
	for _, field := range ReferenceFields(dag) {
		for _, ref := range outputReferences(field.Value) {
			refs = append(refs, collectedReference{field: field.Path, ref: ref})
		}
	}

	require.Len(t, refs, 2)
	assert.Equal(t, "steps[0].output.payload.value[0].z", refs[0].field)
	assert.Equal(t, "zed", refs[0].ref.Path[0])
	assert.Equal(t, "steps[0].output.payload.value[1].a", refs[1].field)
	assert.Equal(t, "alpha", refs[1].ref.Path[0])
}

func TestReferenceFieldsIncludesExecutorConfig(t *testing.T) {
	t.Parallel()

	var refs []ReferenceField
	dag := &DAG{Steps: []Step{{
		Name: "consumer",
		ExecutorConfig: ExecutorConfig{
			Config: map[string]any{
				"endpoint": "https://example.com/${build.output.host}",
				"headers": map[string]any{
					"authorization": "Bearer ${build.output.token}",
				},
			},
		},
	}}}
	for _, field := range ReferenceFields(dag) {
		if len(outputReferences(field.Value)) > 0 {
			refs = append(refs, field)
		}
	}

	require.Len(t, refs, 2)
	assert.Equal(t, "steps[0].with.endpoint", refs[0].Path)
	assert.Equal(t, "https://example.com/${build.output.host}", refs[0].Value)
	assert.Equal(t, "steps[0].with.headers.authorization", refs[1].Path)
	assert.Equal(t, "Bearer ${build.output.token}", refs[1].Value)
}

func TestValidateOutputReferencesWarnsForStrictPluralStepOutputReferences(t *testing.T) {
	t.Parallel()

	dag := &DAG{
		Name: "test",
		Steps: []Step{
			{
				ID:   "build",
				Name: "build",
				StructuredOutput: map[string]StepOutputEntry{
					"image": {},
				},
			},
			{
				Name:    "deploy",
				Depends: []string{"build"},
				Script:  "echo ${steps.build.outputs.digest}",
			},
		},
	}

	err := dag.Validate()
	require.NoError(t, err)
	require.Len(t, dag.BuildWarnings, 1)
	assert.Contains(t, dag.BuildWarnings[0], "${steps.build.outputs.digest}")
	assert.Contains(t, dag.BuildWarnings[0], `publishes no output field "digest"`)
}

func TestValidateOutputReferencesWarnsForGraphMisses(t *testing.T) {
	t.Parallel()

	dag := &DAG{
		Name: "test",
		Steps: []Step{
			{
				Name: "build",
				StructuredOutput: map[string]StepOutputEntry{
					"image": {},
				},
			},
			{
				Name: "deploy",
				Script: strings.Join([]string{
					"echo ${steps.missing.outputs.image}",
					"echo ${steps.deploy.outputs.image}",
					"echo ${steps.build.outputs.image}",
				}, "\n"),
			},
		},
	}

	err := dag.Validate()
	require.NoError(t, err)
	require.Len(t, dag.BuildWarnings, 3)
	assert.Contains(t, dag.BuildWarnings[0], `step "missing" does not exist`)
	assert.Contains(t, dag.BuildWarnings[1], `references its own output`)
	assert.Contains(t, dag.BuildWarnings[2], `step "build" is not an upstream dependency`)
}
