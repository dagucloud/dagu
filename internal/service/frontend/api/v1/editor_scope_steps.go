// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// scopeSteps lists the top-level steps of dag in DAG order, then its handler
// steps in the order init, success, failure, abort, exit, wait.
//
// Handler steps are outside the step graph (specs 007 and 009): they have no
// step-output lookup scope, so they list no upstream steps. Nothing can
// reference the outputs of a handler step either, so they list no outputs, and
// that list is static.
func scopeSteps(dag *ir.DAG) []api.ReferenceScopeStep {
	steps := make([]api.ReferenceScopeStep, 0, len(dag.Steps))
	upstream := newUpstreamIndex(dag)
	for i := range dag.Steps {
		item := scopeStep(&dag.Steps[i], fmt.Sprintf("steps[%d]", i), upstream.names(i))
		steps = append(steps, item)
	}
	handlers := []struct {
		event string
		step  *ir.Step
	}{
		{"init", dag.HandlerOn.Init},
		{"success", dag.HandlerOn.Success},
		{"failure", dag.HandlerOn.Failure},
		{"abort", dag.HandlerOn.Abort},
		{"exit", dag.HandlerOn.Exit},
		{"wait", dag.HandlerOn.Wait},
	}
	for _, h := range handlers {
		if h.step == nil {
			continue
		}
		item := scopeStep(h.step, "handler_on."+h.event, []string{})
		item.Outputs = []api.ReferenceScopeOutput{}
		item.OutputsKnown = api.ReferenceScopeStepOutputsKnownStatic
		steps = append(steps, item)
	}
	return steps
}

// scopeStep describes one step with the given path and upstream names.
func scopeStep(step *ir.Step, stepPath string, upstream []string) api.ReferenceScopeStep {
	item := api.ReferenceScopeStep{
		Name:         step.Name,
		StepPath:     stepPath,
		Upstream:     upstream,
		Outputs:      []api.ReferenceScopeOutput{},
		OutputsKnown: api.ReferenceScopeStepOutputsKnownStatic,
	}
	if publishesRuntimeOutputs(step) {
		item.OutputsKnown = api.ReferenceScopeStepOutputsKnownRuntime
	}
	if step.ID != "" {
		item.Id = ptrOf(step.ID)
	}
	item.Outputs = stepOutputs(step)
	return item
}

// outputNamePattern is the name syntax of a step-output reference (spec 007).
var outputNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// variableNamePattern is the name syntax of an output variable reference.
var variableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// outputSourceRank orders outputs by source: declared, stdoutField,
// outputVariable, action.
var outputSourceRank = map[api.ReferenceScopeOutputSource]int{
	api.ReferenceScopeOutputSourceDeclared:       0,
	api.ReferenceScopeOutputSourceStdoutField:    1,
	api.ReferenceScopeOutputSourceOutputVariable: 2,
	api.ReferenceScopeOutputSourceAction:         3,
}

// stepOutputs lists the outputs that other steps can reference. It orders them
// by source, then by name. A name that an earlier source already lists is not
// repeated.
//
// ${steps.<id>.outputs.<name>} needs an id, so a step without one lists no such
// output. The output variable ${VAR} needs no id.
func stepOutputs(step *ir.Step) []api.ReferenceScopeOutput {
	outputs := []api.ReferenceScopeOutput{}
	seen := map[string]struct{}{}
	add := func(name, typ string, source api.ReferenceScopeOutputSource) {
		if step.ID == "" || !outputNamePattern.MatchString(name) {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		o := api.ReferenceScopeOutput{
			Name:      name,
			Reference: "${steps." + step.ID + ".outputs." + name + "}",
			Source:    source,
		}
		if typ != "" {
			o.Type = ptrOf(typ)
		}
		outputs = append(outputs, o)
	}

	for _, out := range step.Outputs {
		if out.Source != ir.StepDeclaredOutputSourceCapture {
			add(out.Name, out.Type, api.ReferenceScopeOutputSourceDeclared)
		}
	}
	if cfg := step.StdoutOutputs; cfg != nil {
		names := slices.Sorted(maps.Keys(cfg.Fields))
		if len(names) == 0 && cfg.Field != "" {
			names = []string{cfg.Field}
		}
		for _, name := range names {
			add(name, "", api.ReferenceScopeOutputSourceStdoutField)
		}
	}

	// The build derives the names that an action publishes (a custom action
	// output_schema, outputs.write, browser extracts) into Outputs with the
	// capture source. A step that runs a plain command publishes its derived
	// names through its own output or output_schema field: they are declared.
	captured := api.ReferenceScopeOutputSourceDeclared
	if !step.ExecutorConfig.IsCommand() || isCustomAction(step) {
		captured = api.ReferenceScopeOutputSourceAction
	}
	for _, out := range step.Outputs {
		if out.Source == ir.StepDeclaredOutputSourceCapture {
			add(out.Name, out.Type, captured)
		}
	}

	if name := strings.TrimPrefix(step.Output, "$"); variableNamePattern.MatchString(name) {
		outputs = append(outputs, api.ReferenceScopeOutput{
			Name:      name,
			Reference: "${" + name + "}",
			Source:    api.ReferenceScopeOutputSourceOutputVariable,
		})
	}

	slices.SortStableFunc(outputs, func(a, b api.ReferenceScopeOutput) int {
		if c := cmp.Compare(outputSourceRank[a.Source], outputSourceRank[b.Source]); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return outputs
}

// publishesRuntimeOutputs reports whether only a run reveals the output names of
// the step. It follows the rule of the value resolver (publishesRuntimeOnlyOutputs
// in internal/spec/value_reference_notices_report.go): a sub-DAG step takes its
// names from the child document, and a bundle action from a manifest. A custom
// action is also runtime, because its template can publish more than its
// output_schema lists. The remaining cases are steps whose captured names are
// dynamic. Browser, computer and xlsx extract steps are not checked.
func publishesRuntimeOutputs(step *ir.Step) bool {
	switch step.ExecutorConfig.Type {
	case ir.ExecutorTypeDAG, ir.ExecutorTypeSubworkflow, ir.ExecutorTypeAction:
		return true
	}
	if step.SubDAG != nil || isCustomAction(step) {
		return true
	}
	switch {
	case step.HasStdoutOutputs():
		// Decoding the whole stdout into outputs reveals the keys only at run time.
		return len(step.StdoutOutputs.Fields) == 0 && step.StdoutOutputs.Field == ""
	case step.HasStructuredOutput():
		return false
	case step.HasOutputSchema():
		if _, ok := step.OutputSchema["properties"].(map[string]any); !ok {
			return true
		}
		// An open schema accepts names that it does not list.
		return step.ExecutorConfig.Type != ir.ExecutorTypeChat && !schemaForbidsExtraProperties(step.OutputSchema)
	case step.ExecutorConfig.Type == ir.ExecutorTypeOutputs:
		_, ok := step.ExecutorConfig.Config["values"].(map[string]any)
		return !ok
	}
	return false
}

// schemaForbidsExtraProperties reports whether a schema accepts only the names
// it lists. A pattern-matched property counts as an unlisted name.
func schemaForbidsExtraProperties(schema map[string]any) bool {
	if _, ok := schema["patternProperties"]; ok {
		return false
	}
	additional, ok := schema["additionalProperties"].(bool)
	return ok && !additional
}

// isCustomAction reports whether the build expanded the step from a custom
// action or a custom step type. Its executor is the one of the template.
func isCustomAction(step *ir.Step) bool {
	name, _ := step.ExecutorConfig.Metadata["custom_type"].(string)
	return name != ""
}

// upstreamIndex holds the ancestor sets of the top-level steps of one DAG.
type upstreamIndex struct {
	steps []ir.Step
	// ancestors[i] is a bit set over step indexes, filled once by a memoized DFS.
	ancestors [][]uint64
}

// newUpstreamIndex computes the transitive ancestors of every top-level step.
// A dependency on an unknown step name is ignored.
func newUpstreamIndex(dag *ir.DAG) *upstreamIndex {
	n := len(dag.Steps)
	idx := &upstreamIndex{steps: dag.Steps, ancestors: make([][]uint64, n)}
	byName := make(map[string]int, n)
	for i := range dag.Steps {
		byName[dag.Steps[i].Name] = i
	}
	parents := make([][]int, n)
	for i := range dag.Steps {
		for _, dep := range dag.Steps[i].AllDepends() {
			if j, ok := byName[dep]; ok {
				parents[i] = append(parents[i], j)
			}
		}
	}

	const (
		unvisited = iota
		inProgress
		done
	)
	state := make([]int, n)
	words := (n + 63) / 64
	cyclic := false
	var visit func(i int)
	visit = func(i int) {
		state[i] = inProgress
		set := make([]uint64, words)
		for _, p := range parents[i] {
			switch state[p] {
			case inProgress:
				cyclic = true
				continue
			case unvisited:
				visit(p)
			}
			set[p/64] |= 1 << (p % 64)
			for w, bits := range idx.ancestors[p] {
				set[w] |= bits
			}
		}
		idx.ancestors[i] = set
		state[i] = done
	}
	for i := range n {
		if state[i] == unvisited {
			visit(i)
		}
	}
	if cyclic {
		// A memoized set is incomplete for steps on a cycle: recompute them by search.
		for i := range n {
			idx.ancestors[i] = reachableFrom(i, parents, words)
		}
	}
	return idx
}

// reachableFrom returns the steps reachable from start through parents.
// Start itself is included only when it lies on a cycle.
func reachableFrom(start int, parents [][]int, words int) []uint64 {
	set := make([]uint64, words)
	stack := append([]int(nil), parents[start]...)
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if set[p/64]&(1<<(p%64)) != 0 {
			continue
		}
		set[p/64] |= 1 << (p % 64)
		stack = append(stack, parents[p]...)
	}
	return set
}

// names lists the ancestors of step i in DAG order, never step i itself.
// The slice is never nil.
func (u *upstreamIndex) names(i int) []string {
	out := []string{}
	for j := range u.steps {
		if j != i && u.ancestors[i][j/64]&(1<<(j%64)) != 0 {
			out = append(out, u.steps[j].Name)
		}
	}
	return out
}
