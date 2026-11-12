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

// variableReferences lists the reference tokens of variables, nil when absent.
func variableReferences(vars *[]api.ReferenceScopeVariable) []string {
	if vars == nil {
		return nil
	}
	out := make([]string, 0, len(*vars))
	for _, v := range *vars {
		out = append(out, v.Reference)
	}
	return out
}

func TestScopeStepEnv(t *testing.T) {
	const secret = "s3cret-value"
	dag := loadScopeDAG(t, `
env:
  - ROOT_ONLY: root
defaults:
  env:
    - FROM_DEFAULTS: d
steps:
  - name: list
    run: echo a
    env:
      - TARGET: `+secret+`
      - BUCKET=b
  - name: plain
    run: echo b
    depends: list
`)
	got := scopeStepsByName(t, dag)
	assert.Equal(t,
		[]string{"${env.BUCKET}", "${env.FROM_DEFAULTS}", "${env.TARGET}"},
		variableReferences(got["list"].Env))
	// A step inherits defaults too, and the root env is not its own.
	assert.Equal(t, []string{"${env.FROM_DEFAULTS}"}, variableReferences(got["plain"].Env))
	require.NotNil(t, got["list"].Env)
	for _, v := range *got["list"].Env {
		// Env values can hold secrets: the scope never carries them.
		assert.Nil(t, v.DefaultValue)
		assert.NotContains(t, fmt.Sprintf("%+v", v), secret)
	}
}

func TestScopeStepRepeats(t *testing.T) {
	dag := loadScopeDAG(t, `
steps:
  - name: poll
    run: echo a
    repeat_policy:
      repeat: while
      limit: 2
      condition: "false"
  - name: once
    run: echo b
    depends: poll
`)
	got := scopeStepsByName(t, dag)
	assert.True(t, valueOf(got["poll"].Repeats))
	assert.Nil(t, got["once"].Repeats)
}

func TestScopeStepRepeatsFromDefaults(t *testing.T) {
	dag := loadScopeDAG(t, `
defaults:
  repeat_policy:
    repeat: while
    limit: 2
    condition: "false"
steps:
  - name: poll
    run: echo a
`)
	assert.True(t, valueOf(scopeStepsByName(t, dag)["poll"].Repeats))
}

func TestScopeStepBuildPaths(t *testing.T) {
	dag := loadScopeDAG(t, `
type: build
working_dir: /workspace
steps:
  - id: compile
    name: compile
    inputs:
      - name: source
        path: source.txt
      - name: header
        path: header.h
    outputs:
      - name: artifact
        path: artifact.bin
    run: compiler
  - id: plain
    name: plain
    run: echo hi
    depends: compile
`)
	got := scopeStepsByName(t, dag)
	assert.Equal(t, []string{"${inputs.source}", "${inputs.header}"}, variableReferences(got["compile"].Inputs))
	assert.Equal(t, []string{"${outputs.artifact}"}, variableReferences(got["compile"].PathOutputs))
	assert.Nil(t, got["plain"].Inputs)
	assert.Nil(t, got["plain"].PathOutputs)
}

// A document with build errors can hold a name that no reference can use.
func TestScopeStepBuildPathsSkipInvalidNames(t *testing.T) {
	step := &ir.Step{
		Inputs: []ir.StepInputDeclaration{{Name: ""}, {Name: "two words"}, {Name: "source"}},
		Outputs: []ir.StepOutputDeclaration{
			{Name: "", Path: "a"},
			{Name: "1bad", Path: "b"},
			{Name: "artifact", Path: "c"},
		},
	}
	assert.Equal(t, []api.ReferenceScopeVariable{{Name: "source", Reference: "${inputs.source}"}}, stepInputs(step))
	assert.Equal(t, []api.ReferenceScopeVariable{{Name: "artifact", Reference: "${outputs.artifact}"}}, stepPathOutputs(step))
}

func TestScopeForeachItem(t *testing.T) {
	const (
		value = "${foreach.item}"
		index = "${foreach.index}"
		key   = "${foreach.key}"
	)
	tests := []struct {
		name string
		// lines holds the fields of the foreach mapping, before its steps.
		lines string
		want  []string
	}{
		{
			name:  "scalar items use the default alias",
			lines: `items: [a, b]`,
			want:  []string{value, index, key},
		},
		{
			name:  "an alias renames the item",
			lines: "items: [a]\n      as: episode",
			want:  []string{"${foreach.episode}", index, key},
		},
		{
			name:  "objects give the fields that every item has",
			lines: `items: [{url: a, slug: b}, {url: c, slug: d, extra: e}]`,
			want:  []string{value, "${foreach.item.slug}", "${foreach.item.url}", index, key},
		},
		{
			name:  "any script may be written unquoted",
			lines: `items: [{名前: a, _row: 1}]`,
			want:  []string{value, "${foreach.item._row}", "${foreach.item.名前}", index, key},
		},
		{
			name:  "a field that needs quotes is left out",
			lines: `items: [{"First Name": a, ok: b}]`,
			want:  []string{value, "${foreach.item.ok}", index, key},
		},
		{
			name:  "items from an expression have unknown fields",
			lines: `items: "${params.LIST}"`,
			want:  []string{value, index, key},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dag := loadScopeDAG(t, `
params:
  - LIST: '[]'
steps:
  - name: each
    foreach:
      `+tt.lines+`
      steps:
        - name: body
          run: echo hi
`)
			got := scopeStepsByName(t, dag)["each"]
			require.NotNil(t, got.Foreach)
			assert.Equal(t, tt.want, variableReferences(&got.Foreach.Item))
		})
	}
}

func TestScopeForeachBodySteps(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
steps:
  - id: before
    name: before
    run: echo a
  - id: each
    name: each
    depends: before
    foreach:
      items: [a]
      steps:
        - id: fetch
          name: fetch
          run: echo a
          outputs:
            - name: body
          env:
            - LOCAL: x
        - id: parse
          name: parse
          depends: fetch
          run: echo b
          repeat_policy:
            repeat: while
            limit: 2
            condition: "false"
        - id: last
          name: last
          depends: parse
          run: echo c
`)
	got := scopeStepsByName(t, dag)
	// Body steps stay under their foreach step, not in the top-level list.
	assert.NotContains(t, got, "fetch")
	require.NotNil(t, got["each"].Foreach)
	body := got["each"].Foreach.Steps
	require.Len(t, body, 3)

	fetch, parse, last := body[0], body[1], body[2]
	assert.Equal(t, "steps[1].foreach.steps[0]", fetch.StepPath)
	assert.Equal(t, []string{}, fetch.Upstream)
	require.Len(t, fetch.Outputs, 1)
	assert.Equal(t, "${steps.fetch.outputs.body}", fetch.Outputs[0].Reference)
	assert.Equal(t, []string{"${env.LOCAL}"}, variableReferences(fetch.Env))
	assert.Nil(t, fetch.Repeats)

	assert.Equal(t, []string{"fetch"}, parse.Upstream)
	assert.True(t, valueOf(parse.Repeats))
	// Upstream is transitive and names body steps only, never `before`.
	assert.Equal(t, []string{"fetch", "parse"}, last.Upstream)
}

// The steps of a nested body use the path of their own body.
func TestScopeForeachNested(t *testing.T) {
	dag := loadScopeDAG(t, `
type: graph
steps:
  - id: outer
    name: outer
    foreach:
      items: [a]
      as: group
      steps:
        - id: inner
          name: inner
          foreach:
            items: [{n: 1}]
            as: row
            steps:
              - id: leaf
                name: leaf
                run: echo hi
`)
	got := scopeStepsByName(t, dag)["outer"]
	require.NotNil(t, got.Foreach)
	require.Len(t, got.Foreach.Steps, 1)
	inner := got.Foreach.Steps[0]
	assert.Equal(t, "steps[0].foreach.steps[0]", inner.StepPath)
	require.NotNil(t, inner.Foreach)
	assert.Equal(t, "${foreach.row}", inner.Foreach.Item[0].Reference)
	require.Len(t, inner.Foreach.Steps, 1)
	assert.Equal(t, "steps[0].foreach.steps[0].foreach.steps[0]", inner.Foreach.Steps[0].StepPath)
}

// A step without foreach, env or build paths gives none of the optional fields.
func TestScopeStepOptionalFieldsAbsent(t *testing.T) {
	dag := loadScopeDAG(t, `
steps:
  - name: plain
    run: echo hi
`)
	got := scopeStepsByName(t, dag)["plain"]
	assert.Nil(t, got.Env)
	assert.Nil(t, got.Inputs)
	assert.Nil(t, got.PathOutputs)
	assert.Nil(t, got.Repeats)
	assert.Nil(t, got.Foreach)
}

// TestScopeModelOutputMatchesValidator keeps the picker and `dagu validate` in
// step: an output is flagged exactly when writing it into a shell command warns.
func TestScopeModelOutputMatchesValidator(t *testing.T) {
	const (
		chat = "action: chat.completion\n    with:\n      messages: [{role: user, content: hi}]\n"
		// The fields of an output_schema are what a reference can name.
		schema = "    output_schema:\n      type: object\n      properties:\n        reply: {type: string}\n        label: {type: string, enum: [a, b]}\n        count: {type: integer}\n        rate: {type: number}\n        ok: {type: boolean}\n"
	)
	tests := []struct {
		name string
		// producer holds the fields of the step, after its id.
		producer string
		output   string
		want     bool
	}{
		{"free text from a model", chat + schema, "reply", true},
		{"an enum", chat + schema, "label", false},
		{"an integer", chat + schema, "count", false},
		{"a number", chat + schema, "rate", false},
		{"a boolean", chat + schema, "ok", false},
		{"a command", "run: echo hi\n    outputs:\n      - name: reply\n", "reply", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dag := loadScopeDAG(t, `
type: graph
steps:
  - id: producer
    `+tt.producer+`
  - id: consumer
    depends: producer
    run: echo "${steps.producer.outputs.`+tt.output+`}"
`)
			warned := false
			for _, warning := range dag.BuildWarnings {
				warned = warned || strings.Contains(warning, "writes model output into a shell command")
			}
			require.Equal(t, tt.want, warned, "the validator: %v", dag.BuildWarnings)

			flagged := false
			for _, output := range scopeStepsByName(t, dag)["producer"].Outputs {
				if output.Name == tt.output {
					flagged = valueOf(output.ModelOutput)
				}
			}
			assert.Equal(t, tt.want, flagged)
		})
	}
}

// ${VAR} is not what the validator reads, so an output variable stays unflagged.
func TestScopeModelOutputVariable(t *testing.T) {
	step := &ir.Step{
		ID:             "producer",
		Output:         "REPLY",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeChat},
	}
	outputs := stepOutputs(step)
	require.Len(t, outputs, 1)
	assert.Nil(t, outputs[0].ModelOutput)
}
