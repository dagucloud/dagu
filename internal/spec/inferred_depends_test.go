// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

const inferredDependsProducer = `
steps:
  - id: read
    run: |
      printf 'rows=[{"a":1},{"a":2}]\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: rows
`

func TestLoadYAMLInfersStepOutputDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		// wantInferred maps a consumer step name to its inferred producers.
		wantInferred map[string][]ir.InferredDependency
	}{
		{
			name: "ForeachItems",
			yaml: inferredDependsProducer + `
  - id: each
    foreach:
      items: ${steps.read.outputs.rows}
      as: row
      steps:
        - run: echo "item=${foreach.row.a}"
`,
			wantInferred: map[string][]ir.InferredDependency{
				"each": {{Step: "read", Field: "steps[1].foreach.items"}},
			},
		},
		{
			name: "ParallelString",
			yaml: inferredDependsProducer + `
  - id: fan
    action: dag.run
    with:
      dag: child
    parallel: ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{
				"fan": {{Step: "read", Field: "steps[1].parallel.variable"}},
			},
		},
		{
			name: "CommandWithExplicitEmptyDepends",
			yaml: inferredDependsProducer + `
  - id: show
    depends: []
    run: echo ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{
				"show": {{Step: "read", Field: "steps[1].run"}},
			},
		},
		{
			name: "StepEnv",
			yaml: inferredDependsProducer + `
  - id: show
    env:
      - ROWS=${steps.read.outputs.rows}
    run: echo "$ROWS"
`,
			wantInferred: map[string][]ir.InferredDependency{
				"show": {{Step: "read", Field: "steps[1].env[0]"}},
			},
		},
		{
			name: "SameProducerReferencedTwiceKeepsFirstField",
			yaml: inferredDependsProducer + `
  - id: show
    env:
      - ROWS=${steps.read.outputs.rows}
    run: echo ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{
				"show": {{Step: "read", Field: "steps[1].run"}},
			},
		},
		{
			name: "ExplicitDependsAlreadyPresent",
			yaml: inferredDependsProducer + `
  - id: show
    depends: read
    run: echo ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{"show": nil},
		},
		{
			name: "TransitiveExplicitDepends",
			yaml: inferredDependsProducer + `
  - id: middle
    depends: read
    run: echo middle
  - id: show
    depends: middle
    run: echo ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{"middle": nil, "show": nil},
		},
		{
			name: "ChainEarlierProducer",
			yaml: `
type: chain
` + inferredDependsProducer + `
  - id: show
    run: echo ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{"show": nil},
		},
		{
			name: "ForeachBodyStepAttachesToOwner",
			yaml: inferredDependsProducer + `
  - id: each
    foreach:
      items: [a, b]
      as: item
      steps:
        - id: body
          run: echo ${steps.read.outputs.rows} ${foreach.item}
`,
			wantInferred: map[string][]ir.InferredDependency{
				"each": {{Step: "read", Field: "steps[1].foreach.steps[0].run"}},
			},
		},
		{
			name: "NestedForeachBodyStepAttachesToTopLevelOwner",
			yaml: inferredDependsProducer + `
  - id: outer
    foreach:
      items: [a]
      as: item
      steps:
        - id: inner
          foreach:
            items: [b]
            as: sub
            steps:
              - id: leaf
                run: echo ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{
				"outer": {{Step: "read", Field: "steps[1].foreach.steps[0].foreach.steps[0].run"}},
			},
		},
		{
			name: "HandlerCreatesNoEdge",
			yaml: inferredDependsProducer + `
handler_on:
  success:
    run: echo ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{},
		},
		{
			name: "TemplateBodyCreatesNoEdge",
			yaml: inferredDependsProducer + `
  - id: render
    action: template.render
    with:
      template: "rows: ${steps.read.outputs.rows}"
`,
			wantInferred: map[string][]ir.InferredDependency{"render": nil},
		},
		{
			name: "TemplateDataCreatesEdge",
			yaml: inferredDependsProducer + `
  - id: render
    action: template.render
    with:
      template: "rows: {{ .rows }}"
      data:
        rows: ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{
				"render": {{Step: "read", Field: "steps[1].with.data.rows"}},
			},
		},
		{
			name: "JSScriptCreatesNoEdge",
			yaml: inferredDependsProducer + `
  - id: shape
    action: js.run
    with:
      script: return "${steps.read.outputs.rows}"
`,
			wantInferred: map[string][]ir.InferredDependency{"shape": nil},
		},
		{
			name: "JSInputCreatesEdge",
			yaml: inferredDependsProducer + `
  - id: shape
    action: js.run
    with:
      script: return input.rows
      input:
        rows: ${steps.read.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{
				"shape": {{Step: "read", Field: "steps[1].with.input.rows"}},
			},
		},
		{
			name: "EscapedTextCreatesNoEdge",
			yaml: inferredDependsProducer + `
  - id: show
    run: echo '\${steps.read.outputs.rows}'
`,
			wantInferred: map[string][]ir.InferredDependency{"show": nil},
		},
		{
			name: "UnsupportedBracedTextCreatesNoEdge",
			yaml: inferredDependsProducer + `
  - id: show
    run: echo ${steps.read.stdout}
`,
			wantInferred: map[string][]ir.InferredDependency{"show": nil},
		},
		{
			name: "UnknownStepCreatesNoEdge",
			yaml: inferredDependsProducer + `
  - id: show
    run: echo ${steps.missing.outputs.rows}
`,
			wantInferred: map[string][]ir.InferredDependency{"show": nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(tt.yaml)), spec.WithoutEval())
			require.NoError(t, err)

			got := make(map[string][]ir.InferredDependency, len(dag.Steps))
			for _, step := range dag.Steps {
				if step.Name == "read" {
					continue
				}
				got[step.Name] = step.InferredDepends
			}
			assert.Equal(t, tt.wantInferred, got)
			for _, step := range dag.Steps {
				for _, dep := range step.InferredDepends {
					assert.NotContains(t, step.Depends, dep.Step, "inferred edges must not be merged into Depends")
				}
			}
		})
	}
}

func TestLoadYAMLRejectsInferredDependencyCycles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yaml     string
		wantErrs []string
	}{
		{
			name: "MutualReference",
			yaml: `
steps:
  - id: a
    run: echo ${steps.b.outputs.y}
    outputs:
      - name: x
  - id: b
    run: echo ${steps.a.outputs.x}
    outputs:
      - name: y
`,
			wantErrs: []string{"inferred dependency a -> b", "creates a cycle"},
		},
		{
			name: "ChainForwardReference",
			yaml: `
type: chain
steps:
  - id: first
    run: echo ${steps.last.outputs.x}
  - id: last
    run: echo done
    outputs:
      - name: x
`,
			wantErrs: []string{"inferred dependency last -> first", "creates a cycle"},
		},
		{
			name: "ExplicitDependsClosesCycle",
			yaml: `
steps:
  - id: a
    depends: b
    run: echo a
    outputs:
      - name: x
  - id: b
    run: echo ${steps.a.outputs.x}
`,
			wantErrs: []string{"inferred dependency a -> b", "creates a cycle"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(tt.yaml)), spec.WithoutEval())
			require.Error(t, err)
			for _, want := range tt.wantErrs {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// An approval gate may rewind to a producer it depends on only through an
// inferred edge.
func TestLoadYAMLAcceptsRewindToInferredProducer(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(inferredDependsProducer+`
  - id: gate
    run: echo ${steps.read.outputs.rows}
    approval:
      rewind_to: read
`)), spec.WithoutEval())
	require.NoError(t, err)
	require.NotNil(t, dag.Steps[1].Approval)
	assert.Equal(t, "read", dag.Steps[1].Approval.RewindTo)
}

func TestLoadYAMLWithResultReportsNoNoticeForInferredDependency(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(strings.TrimSpace(inferredDependsProducer+`
  - id: show
    run: echo ${steps.read.outputs.rows}
`)), spec.WithoutEval())
	require.NoError(t, err)
	assert.Empty(t, result.ValueReferenceNotices)
}
