// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadScopeDAG loads YAML the way the editor does: build errors are kept and
// nothing is evaluated.
func loadScopeDAG(t *testing.T, yaml string) *ir.DAG {
	t.Helper()
	dag, err := spec.LoadYAML(context.Background(), []byte(yaml), spec.WithAllowBuildErrors(), spec.WithoutEval())
	require.NoError(t, err)
	require.NotNil(t, dag)
	return dag
}

// scopeStepsByName indexes the scope steps of dag by name.
func scopeStepsByName(t *testing.T, dag *ir.DAG) map[string]api.ReferenceScopeStep {
	t.Helper()
	out := map[string]api.ReferenceScopeStep{}
	for _, s := range scopeSteps(dag) {
		out[s.Name] = s
	}
	return out
}

func TestScopeUpstreamTransitive(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
steps:
  - name: a
    run: echo a
  - name: b
    run: echo b
    depends: a
  - name: c
    run: echo c
    depends: b
  - name: d
    run: echo d
  - name: e
    run: echo e
    depends: [c, d]
`)
	got := scopeStepsByName(t, dag)
	assert.Equal(t, []string{}, got["a"].Upstream)
	assert.Equal(t, []string{"a"}, got["b"].Upstream)
	assert.Equal(t, []string{"a", "b"}, got["c"].Upstream)
	assert.Equal(t, []string{}, got["d"].Upstream)
	// Ancestors come in DAG order, not in dependency order.
	assert.Equal(t, []string{"a", "b", "c", "d"}, got["e"].Upstream)
}

// A cycle must give a finite answer: every step of the cycle lists the others.
func TestScopeUpstreamCycle(t *testing.T) {
	dag := &ir.DAG{Steps: []ir.Step{
		{Name: "a", Depends: []string{"c"}},
		{Name: "b", Depends: []string{"a"}},
		{Name: "c", Depends: []string{"b"}},
		{Name: "d", Depends: []string{"d", "missing"}},
	}}
	got := scopeStepsByName(t, dag)
	assert.Equal(t, []string{"b", "c"}, got["a"].Upstream)
	assert.Equal(t, []string{"a", "c"}, got["b"].Upstream)
	assert.Equal(t, []string{"a", "b"}, got["c"].Upstream)
	assert.Equal(t, []string{}, got["d"].Upstream)
}

// An output reference orders its step after the producer without depends.
func TestScopeUpstreamInferred(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
steps:
  - id: build
    run: echo build
    outputs:
      - name: tag
  - id: mid
    run: echo ${steps.build.outputs.tag}
  - id: deploy
    run: echo ${steps.mid.outputs.x}
    depends: []
`)
	got := scopeStepsByName(t, dag)
	assert.Equal(t, []string{}, got["build"].Upstream)
	assert.Equal(t, []string{"build"}, got["mid"].Upstream)
	assert.Equal(t, []string{"build", "mid"}, got["deploy"].Upstream)
}

// The builder writes the router into the depends of every target, so the
// closure needs no router-specific rule.
func TestScopeUpstreamRouter(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
steps:
  - name: fetch
    run: echo fetch
  - name: route
    type: router
    depends: fetch
    value: ${MODE}
    routes:
      a: [step_a]
      b: [step_b, step_c]
  - name: step_a
    run: echo a
  - name: step_b
    run: echo b
  - name: step_c
    run: echo c
    depends: step_b
  - name: join
    run: echo join
    depends: [step_a, step_c]
`)
	got := scopeStepsByName(t, dag)
	assert.Equal(t, []string{"fetch"}, got["route"].Upstream)
	assert.Equal(t, []string{"fetch", "route"}, got["step_a"].Upstream)
	assert.Equal(t, []string{"fetch", "route"}, got["step_b"].Upstream)
	assert.Equal(t, []string{"fetch", "route", "step_b"}, got["step_c"].Upstream)
	assert.Equal(t, []string{"fetch", "route", "step_a", "step_b", "step_c"}, got["join"].Upstream)
}

// A chain DAG has no depends in the YAML: the builder links each step to the
// previous one, and an id does not change the resulting names.
func TestScopeUpstreamChain(t *testing.T) {
	dag := loadScopeDAG(t, `
type: chain
steps:
  - name: one
    id: first
    run: echo 1
  - name: two
    run: echo 2
  - name: three
    run: echo 3
`)
	got := scopeStepsByName(t, dag)
	assert.Equal(t, []string{}, got["one"].Upstream)
	assert.Equal(t, []string{"one"}, got["two"].Upstream)
	assert.Equal(t, []string{"one", "two"}, got["three"].Upstream)
}

// A nested array of a chain DAG is a group of steps that share the previous
// entry; the next entry depends on the whole group.
func TestScopeUpstreamChainGroup(t *testing.T) {
	dag := loadScopeDAG(t, `
type: chain
steps:
  - name: first
    run: echo 1
  -
    - name: left
      run: echo l
    - name: right
      run: echo r
  - name: last
    run: echo z
`)
	got := scopeStepsByName(t, dag)
	assert.Equal(t, []string{"first"}, got["left"].Upstream)
	assert.Equal(t, []string{"first"}, got["right"].Upstream)
	assert.Equal(t, []string{"first", "left", "right"}, got["last"].Upstream)
}

// Handler steps follow the top-level steps in a fixed event order and have no
// upstream steps or referenceable outputs.
func TestScopeHandlers(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
handler_on:
  wait:
    run: echo wait
  exit:
    run: echo exit
  failure:
    run: echo failure
  abort:
    run: echo abort
  success:
    run: echo success
  init:
    run: echo init
steps:
  - id: main
    run: echo main
    outputs:
      - name: x
`)
	steps := scopeSteps(dag)
	require.Len(t, steps, 7)
	assert.Equal(t, "steps[0]", steps[0].StepPath)
	wantPaths := []string{
		"handler_on.init", "handler_on.success", "handler_on.failure",
		"handler_on.abort", "handler_on.exit", "handler_on.wait",
	}
	for i, path := range wantPaths {
		h := steps[i+1]
		assert.Equal(t, path, h.StepPath)
		assert.NotEmpty(t, h.Name)
		assert.Equal(t, []string{}, h.Upstream)
		assert.Equal(t, []api.ReferenceScopeOutput{}, h.Outputs)
		assert.Equal(t, api.ReferenceScopeStepOutputsKnownStatic, h.OutputsKnown)
	}
}

// A handler step with an id still lists nothing: no field can resolve it, so
// the empty list is final even when only a run would give its names.
func TestScopeHandlerWithID(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
handler_on:
  exit:
    id: cleanup
    run: echo done
    outputs:
      - name: result
  failure:
    id: report
    run: echo done
    stdout:
      outputs:
        decode: json
steps:
  - run: echo main
    name: main
`)
	steps := scopeSteps(dag)
	require.Len(t, steps, 3)
	assert.Equal(t, "handler_on.failure", steps[1].StepPath)
	assert.Equal(t, "handler_on.exit", steps[2].StepPath)
	require.NotNil(t, steps[2].Id)
	assert.Equal(t, "cleanup", *steps[2].Id)
	for _, h := range steps[1:] {
		assert.Empty(t, h.Outputs, h.StepPath)
		assert.Equal(t, api.ReferenceScopeStepOutputsKnownStatic, h.OutputsKnown, h.StepPath)
	}
}

func outputRefs(outputs []api.ReferenceScopeOutput) []string {
	refs := make([]string, 0, len(outputs))
	for _, o := range outputs {
		refs = append(refs, string(o.Source)+" "+o.Reference)
	}
	return refs
}

func TestScopeOutputsStdoutFields(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
steps:
  - id: build
    run: echo ok
    outputs:
      - name: tag
        type: string
    stdout:
      outputs:
        fields:
          zeta: {decode: json, select: .z}
          image: {decode: json, select: .image}
          tag: {decode: json, select: .tag}
  - id: single
    run: echo ok
    stdout:
      outputs:
        field: result
        decode: json
  - id: whole
    run: echo ok
    stdout:
      outputs:
        decode: json
  - run: echo ok
    name: noid
    stdout:
      outputs:
        fields:
          x: {decode: json}
`)
	got := scopeStepsByName(t, dag)
	// A declared name wins over a stdout field of the same name.
	assert.Equal(t, []string{
		"declared ${steps.build.outputs.tag}",
		"stdoutField ${steps.build.outputs.image}",
		"stdoutField ${steps.build.outputs.zeta}",
	}, outputRefs(got["build"].Outputs))
	require.NotNil(t, got["build"].Outputs[0].Type)
	assert.Equal(t, "string", *got["build"].Outputs[0].Type)
	assert.Nil(t, got["build"].Outputs[1].Type)
	assert.Equal(t, []string{"stdoutField ${steps.single.outputs.result}"}, outputRefs(got["single"].Outputs))
	assert.Empty(t, got["whole"].Outputs)
	assert.Empty(t, got["noid"].Outputs)
}

// The output variable ${VAR} needs no id, so it is listed for every step.
func TestScopeOutputsVariable(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
steps:
  - id: fetch
    run: echo ok
    output: BODY
    outputs:
      - name: size
  - run: echo ok
    name: noid
    output: $RESULT
  - run: echo ok
    name: plain
  - run: echo ok
    name: object
    output:
      image:
        from: stdout
`)
	got := scopeStepsByName(t, dag)
	assert.Equal(t, []string{
		"declared ${steps.fetch.outputs.size}",
		"outputVariable ${BODY}",
	}, outputRefs(got["fetch"].Outputs))
	assert.Equal(t, []string{"outputVariable ${RESULT}"}, outputRefs(got["noid"].Outputs))
	assert.Equal(t, "RESULT", got["noid"].Outputs[0].Name)
	assert.Empty(t, got["plain"].Outputs)
	// Object-form output publishes step outputs, not a variable.
	assert.Empty(t, got["object"].Outputs)
}

func TestScopeOutputsCustomAction(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
actions:
  build.image:
    input_schema: {type: object, additionalProperties: false, properties: {}}
    output_schema:
      type: object
      additionalProperties: false
      properties:
        tag: {type: string}
        meta: {type: object}
    template:
      run: echo ok
steps:
  - id: build
    action: build.image
    with: {}
  - id: write
    action: outputs.write
    with:
      values:
        answer: "42"
  - id: plain
    run: echo ok
    output_schema:
      type: object
      properties:
        found: {type: string}
`)
	got := scopeStepsByName(t, dag)
	build := got["build"].Outputs
	assert.Equal(t, []string{
		"action ${steps.build.outputs.meta}",
		"action ${steps.build.outputs.tag}",
	}, outputRefs(build))
	require.NotNil(t, build[0].Type)
	assert.Equal(t, "json", *build[0].Type)
	require.NotNil(t, build[1].Type)
	assert.Equal(t, "string", *build[1].Type)
	assert.Equal(t, []string{"action ${steps.write.outputs.answer}"}, outputRefs(got["write"].Outputs))
	// A command step publishes its own output_schema names.
	assert.Equal(t, []string{"declared ${steps.plain.outputs.found}"}, outputRefs(got["plain"].Outputs))
}

// Steps whose output names only a run reveals are marked runtime. A static
// step has a closed list of names.
func TestScopeOutputsKnown(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
actions:
  build.image:
    input_schema: {type: object, additionalProperties: false, properties: {}}
    output_schema:
      type: object
      properties:
        tag: {type: string}
    template:
      run: echo ok
steps:
  - id: plain
    run: echo ok
    outputs:
      - name: x
  - id: closed
    run: echo ok
    output_schema:
      type: object
      additionalProperties: false
      properties:
        found: {type: string}
  - id: open
    run: echo ok
    output_schema:
      type: object
      properties:
        found: {type: string}
  - id: whole
    run: echo ok
    stdout:
      outputs:
        decode: json
  - id: fields
    run: echo ok
    stdout:
      outputs:
        fields:
          a: {decode: json}
  - id: child
    call: child
  - id: custom
    action: build.image
    with: {}
  - id: bundle
    action: acme/dagu-actions-slack@v1
`)
	want := map[string]api.ReferenceScopeStepOutputsKnown{
		"plain":  api.ReferenceScopeStepOutputsKnownStatic,
		"closed": api.ReferenceScopeStepOutputsKnownStatic,
		"open":   api.ReferenceScopeStepOutputsKnownRuntime,
		"whole":  api.ReferenceScopeStepOutputsKnownRuntime,
		"fields": api.ReferenceScopeStepOutputsKnownStatic,
		"child":  api.ReferenceScopeStepOutputsKnownRuntime,
		"custom": api.ReferenceScopeStepOutputsKnownRuntime,
		"bundle": api.ReferenceScopeStepOutputsKnownRuntime,
	}
	got := map[string]api.ReferenceScopeStepOutputsKnown{}
	for _, s := range scopeSteps(dag) {
		got[s.Name] = s.OutputsKnown
	}
	assert.Equal(t, want, got)
}

// Every ${steps...} reference that the scope returns must pass the value
// resolver without a notice. A reference to a missing output still gets one,
// which shows that the check can fail.
func TestScopeReferencesResolve(t *testing.T) {
	const producers = `
type: graph
actions:
  build.image:
    input_schema: {type: object, additionalProperties: false, properties: {}}
    output_schema:
      type: object
      properties:
        tag: {type: string}
        meta: {type: object}
    template:
      run: echo ok
steps:
  - id: declared
    run: echo ok
    output: BODY
    outputs:
      - name: size
      - name: blob
        type: json
  - id: fields
    run: echo ok
    stdout:
      outputs:
        fields:
          image: {decode: json, select: .image}
          zeta: {decode: json}
  - id: single
    run: echo ok
    stdout:
      outputs:
        field: result
        decode: json
  - id: schema
    run: echo ok
    output_schema:
      type: object
      additionalProperties: false
      properties:
        found: {type: string}
  - id: write
    action: outputs.write
    with:
      values:
        answer: "42"
  - id: custom
    action: build.image
    with: {}
  - run: echo ok
    name: noid
    output: RESULT
`
	scope := buildReferenceScope(loadScopeDAG(t, producers))
	require.NotNil(t, scope)
	var refs []string
	for _, step := range scope.Steps {
		for _, o := range step.Outputs {
			refs = append(refs, o.Reference)
		}
	}
	require.Len(t, refs, 11)
	assert.Contains(t, refs, "${RESULT}")

	var consumer strings.Builder
	consumer.WriteString("  - id: consumer\n    run: echo done\n    env:\n")
	for i, ref := range refs {
		fmt.Fprintf(&consumer, "      - V%d: %q\n", i, ref)
	}
	consumer.WriteString("      - MISSING: \"${steps.declared.outputs.nosuch}\"\n")

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(producers+consumer.String()),
		spec.WithAllowBuildErrors(), spec.WithoutEval())
	require.NoError(t, err)

	// An output variable is not known before a run and no dependency is
	// inferred from it, so the resolver reports it as a runtime-only value.
	got := map[string]cmnvalue.ValueReferenceNotice{}
	for _, n := range result.ValueReferenceNotices {
		got[n.Token] = n
	}
	require.Len(t, got, 3)
	for _, token := range []string{"${BODY}", "${RESULT}"} {
		assert.Equal(t, cmnvalue.ValueReferenceReasonUnknownEnvBinding, got[token].Reason, token)
		assert.Equal(t, cmnvalue.NoticeClassRuntimeOnly, got[token].Class, token)
	}
	assert.Equal(t, cmnvalue.ValueReferenceReasonUnknownOutputName, got["${steps.declared.outputs.nosuch}"].Reason)
}

// The closure is computed once per DAG, so 300 steps with a long chain and a
// wide fan-in stay far below the generous bound of the test.
func TestScopeUpstreamLarge(t *testing.T) {
	const n = 300
	tests := []struct {
		name    string
		depends func(i int) string
	}{
		{"chain", func(i int) string { return fmt.Sprintf("[s%d]", i-1) }},
		{"fan-in", func(i int) string { return fmt.Sprintf("[s%d, s%d]", i-1, i/2) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var yaml strings.Builder
			yaml.WriteString("type: graph\nsteps:\n")
			for i := range n {
				fmt.Fprintf(&yaml, "  - name: s%d\n    run: echo %d\n", i, i)
				if i > 0 {
					fmt.Fprintf(&yaml, "    depends: %s\n", tt.depends(i))
				}
			}
			dag := loadScopeDAG(t, yaml.String())
			require.Len(t, dag.Steps, n)

			start := time.Now()
			steps := scopeSteps(dag)
			elapsed := time.Since(start)

			require.Len(t, steps, n)
			for i, step := range steps {
				require.Len(t, step.Upstream, i, step.Name)
				if i > 0 {
					assert.Equal(t, "s0", step.Upstream[0])
					assert.Equal(t, fmt.Sprintf("s%d", i-1), step.Upstream[i-1])
				}
			}
			assert.Less(t, elapsed, 2*time.Second)
		})
	}
}
