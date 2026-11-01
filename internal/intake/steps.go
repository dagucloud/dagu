// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
)

// outputNamePattern is the name a step output reference can address.
var outputNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// OutputSource names the run that supplied outputs of a skipped step were
// captured from. The run may no longer exist.
type OutputSource struct {
	RunID string
	// CapturedAt is an RFC 3339 timestamp, or empty when unknown.
	CapturedAt string
}

// CheckOutputSources makes sure that every entry of sources is for a step in
// outputs. A step in outputs may have no entry. Steps are given by name or ID.
// The caller checks the run IDs.
func CheckOutputSources(dag *ir.DAG, outputs map[string]map[string]string, sources map[string]OutputSource) error {
	supplied, err := stepRefsByName(dag, slices.Sorted(maps.Keys(outputs)), false)
	if err != nil {
		return err
	}
	named, err := stepRefsByName(dag, slices.Sorted(maps.Keys(sources)), true)
	if err != nil {
		return fmt.Errorf("outputsSources: %w", err)
	}
	for _, name := range slices.Sorted(maps.Keys(named)) {
		if _, ok := supplied[name]; !ok {
			return fmt.Errorf("outputsSources names step %s, which is not in outputs", named[name])
		}
	}
	return nil
}

// RunStepSources names, by step, the finished run whose recorded outputs the
// step carries, in place of the main source run. Steps are given by name or ID.
type RunStepSources map[string]*ir.DAGRunStatus

// RunStepSourceError reports a run named for a step that cannot be read. Its
// message names the step and the run.
type RunStepSourceError struct {
	Message string
}

func (e *RunStepSourceError) Error() string { return e.Message }

// LoadRunStepSources reads the run that each step of refs carries its outputs
// from. refs maps a step name or ID to a run ID. A run that does not exist for
// the DAG (it was removed, or it belongs to another DAG) gives a
// *RunStepSourceError that names the step and the run. A run ID that refs
// repeats is read once. check, when set, accepts or rejects each run read; its
// error is returned as it is.
func LoadRunStepSources(ctx context.Context, repo *persis.DAGRunRepository, dagName string, refs map[string]string, check func(*ir.DAGRunStatus) error) (RunStepSources, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	runs := make(map[string]*ir.DAGRunStatus)
	result := make(RunStepSources, len(refs))
	for _, step := range slices.Sorted(maps.Keys(refs)) {
		runID := refs[step]
		run, ok := runs[runID]
		if !ok {
			attempt, err := repo.FindAttempt(ctx, ir.NewDAGRunRef(dagName, runID))
			if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
				return nil, &RunStepSourceError{Message: fmt.Sprintf("step %s cannot be reused from run %s: the run does not exist for DAG %s", step, runID, dagName)}
			}
			if err != nil {
				return nil, fmt.Errorf("failed to find dag-run %s: %w", runID, err)
			}
			run, err = attempt.ReadStatus(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to read status for dag-run %s: %w", runID, err)
			}
			if check != nil {
				if err := check(run); err != nil {
					return nil, err
				}
			}
			runs[runID] = run
		}
		result[step] = run
	}
	return result, nil
}

// CheckRunStepSources makes sure that every step of fromRuns exists, is not
// selected to run, and can be reused from its run: the run is finished and its
// node for the step succeeded, or carries outputs of an earlier run. The error
// names the step and the run.
func CheckRunStepSources(dag *ir.DAG, steps []string, fromRuns RunStepSources) error {
	selected := make(map[string]struct{}, len(steps))
	for _, ref := range steps {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return err
		}
		selected[name] = struct{}{}
	}
	named, err := stepRefsByName(dag, slices.Sorted(maps.Keys(fromRuns)), true)
	if err != nil {
		return fmt.Errorf("outputsFromRuns: %w", err)
	}
	for _, name := range slices.Sorted(maps.Keys(named)) {
		ref := named[name]
		run := fromRuns[ref]
		if _, ok := selected[name]; ok {
			return fmt.Errorf("outputsFromRuns names step %q, which is selected to run", ref)
		}
		if run == nil {
			return fmt.Errorf("step %s cannot be reused from run: no run is given", ref)
		}
		if run.Status.IsActive() || run.Status == ir.NotStarted {
			return fmt.Errorf("step %s cannot be reused from run %s: the run is %s, not finished", ref, run.DAGRunID, run.Status)
		}
		index := slices.IndexFunc(run.Nodes, func(node *ir.Node) bool { return node != nil && node.Step.Name == name })
		switch {
		case index < 0:
			return fmt.Errorf("step %s cannot be reused from run %s: the run has no such step", ref, run.DAGRunID)
		case !carriesOutputs(run.Nodes[index]):
			return fmt.Errorf("step %s cannot be reused from run %s: the step did not succeed there", ref, run.DAGRunID)
		}
	}
	return nil
}

// carriesOutputs reports whether a node can give its outputs to another run.
// A step skipped by retry that carried nothing has no finish time.
func carriesOutputs(node *ir.Node) bool {
	return transform.Reusable(node) && (node.Status != ir.NodeSkipped || node.FinishedAt != "")
}

// stepRefsByName maps the name of each step that refs address to the ref that
// addresses it first. With unique set, it rejects a step that two refs address.
func stepRefsByName(dag *ir.DAG, refs []string, unique bool) (map[string]string, error) {
	byName := make(map[string]string, len(refs))
	for _, ref := range refs {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return nil, err
		}
		if first, ok := byName[name]; ok {
			if unique {
				return nil, fmt.Errorf("step %q is given by both %q and %q", name, first, ref)
			}
			continue
		}
		byName[name] = ref
	}
	return byName, nil
}

// SelectedStepNodes returns seed nodes for a run of dag that executes only
// steps, each given by step name or ID, and the provenance of the outputs
// that the skipped steps carry. Every other step is recorded as skipped; when
// source is set, it carries the outputs source recorded for it. fromRuns
// gives a step another run to carry from; it takes precedence over source for
// that step, and the work directory still comes from source only. outputs sets
// outputs of skipped steps, keyed by step name or ID and then by output name,
// and takes precedence over outputs carried from a run. sources names the
// run each step of outputs was captured from; a step of outputs with no entry
// has no recorded provenance.
func SelectedStepNodes(dag *ir.DAG, steps []string, source *ir.DAGRunStatus, fromRuns RunStepSources, outputs map[string]map[string]string, sources map[string]OutputSource) ([]runtime.NodeData, []ir.ReusedInput, error) {
	if dag.IsAgent() {
		return nil, nil, fmt.Errorf("selecting steps is not supported for %s DAGs", ir.TypeAgent)
	}
	if len(steps) == 0 {
		return nil, nil, fmt.Errorf("at least one step is required")
	}
	if source != nil && (source.Status.IsActive() || source.Status == ir.NotStarted) {
		return nil, nil, fmt.Errorf("dag-run %s is %s; outputs are reused only from a finished run", source.DAGRunID, source.Status)
	}

	selected := make(map[string]struct{}, len(steps))
	for _, ref := range steps {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return nil, nil, err
		}
		selected[name] = struct{}{}
	}

	skipped := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		if _, ok := selected[step.Name]; !ok {
			skipped = append(skipped, step.Name)
		}
	}
	if err := CheckRunStepSources(dag, steps, fromRuns); err != nil {
		return nil, nil, err
	}
	carrying, err := carryingSteps(dag, selected, skipped, fromRuns, outputs)
	if err != nil {
		return nil, nil, err
	}
	nodes := transform.SeedNodesCarrying(dag, source, skipped, carrying)
	runs, err := seedFromRuns(dag, nodes, fromRuns)
	if err != nil {
		return nil, nil, err
	}
	if err := setStepOutputs(dag, nodes, selected, outputs); err != nil {
		return nil, nil, err
	}
	carried := make([]string, 0, len(skipped))
	for _, name := range skipped {
		if _, ok := carrying[name]; ok {
			carried = append(carried, name)
		}
	}
	reused, err := reusedInputs(dag, carried, source, runs, outputs, sources)
	if err != nil {
		return nil, nil, err
	}
	return nodes, reused, nil
}

// SelectionSkipsHandlers reports whether a run of dag that executes only steps,
// each given by step name or ID, must not call the end-of-run handlers of the
// DAG. It does when no selected step is a graph end step: a step that no other
// step depends on, by depends, by an inferred dependency, or as the target of a
// router (which the DAG definition records as a dependency).
func SelectionSkipsHandlers(dag *ir.DAG, steps []string) (bool, error) {
	selected := make(map[string]struct{}, len(steps))
	for _, ref := range steps {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return false, err
		}
		selected[name] = struct{}{}
	}
	hasDependents := make(map[string]struct{}, len(dag.Steps))
	for i := range dag.Steps {
		for _, dep := range dag.Steps[i].AllDepends() {
			hasDependents[dep] = struct{}{}
		}
	}
	for name := range selected {
		if _, interior := hasDependents[name]; !interior {
			return false, nil
		}
	}
	return true, nil
}

// carryingSteps returns the skipped steps that carry outputs: the steps that
// a selected step depends on, directly or not, and the steps that the caller
// names in fromRuns or outputs. The other skipped steps, such as the steps
// after the selected ones, are only skipped: nothing reads their outputs.
func carryingSteps(dag *ir.DAG, selected map[string]struct{}, skipped []string, fromRuns RunStepSources, outputs map[string]map[string]string) (map[string]struct{}, error) {
	byName := make(map[string]*ir.Step, len(dag.Steps))
	for i := range dag.Steps {
		byName[dag.Steps[i].Name] = &dag.Steps[i]
	}
	carry := make(map[string]struct{}, len(skipped))
	var visit func(name string)
	visit = func(name string) {
		step, ok := byName[name]
		if !ok {
			return
		}
		for _, dep := range step.AllDepends() {
			if _, done := carry[dep]; done {
				continue
			}
			if _, isSelected := selected[dep]; !isSelected {
				carry[dep] = struct{}{}
			}
			visit(dep)
		}
	}
	for name := range selected {
		visit(name)
	}
	for _, refs := range []map[string]struct{}{keySet(fromRuns), keySet(outputs)} {
		for ref := range refs {
			name, err := resolveStepName(dag, ref)
			if err != nil {
				return nil, err
			}
			if _, isSelected := selected[name]; !isSelected {
				carry[name] = struct{}{}
			}
		}
	}
	return carry, nil
}

func keySet[V any](m map[string]V) map[string]struct{} {
	set := make(map[string]struct{}, len(m))
	for key := range m {
		set[key] = struct{}{}
	}
	return set
}

// seedFromRuns replaces the seeded node of each step of fromRuns by the node
// that the seeding of that run gives: the same seeding as for the main source.
// It returns the run of each step by step name.
func seedFromRuns(dag *ir.DAG, nodes []runtime.NodeData, fromRuns RunStepSources) (map[string]*ir.DAGRunStatus, error) {
	byName := make(map[string]*ir.DAGRunStatus, len(fromRuns))
	for ref, run := range fromRuns {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return nil, err
		}
		own := transform.SeedNodes(dag, run, []string{name})
		to := slices.IndexFunc(nodes, func(node runtime.NodeData) bool { return node.Step.Name == name })
		from := slices.IndexFunc(own, func(node runtime.NodeData) bool { return node.Step.Name == name })
		nodes[to] = own[from]
		byName[name] = run
	}
	return byName, nil
}

// reusedInputs lists, in DAG step order, where each skipped step took its
// outputs from. A supplied entry wins over a run, as the values do, and a
// run named for the step wins over the source run. A step of outputs with no
// entry in sources has no entry, also when a run carries it.
func reusedInputs(dag *ir.DAG, skipped []string, source *ir.DAGRunStatus, runs map[string]*ir.DAGRunStatus, outputs map[string]map[string]string, sources map[string]OutputSource) ([]ir.ReusedInput, error) {
	outputsByName, err := stepRefsByName(dag, slices.Sorted(maps.Keys(outputs)), false)
	if err != nil {
		return nil, err
	}
	cached := make(map[string]OutputSource, len(sources))
	for ref, from := range sources {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return nil, err
		}
		if _, supplied := outputsByName[name]; supplied {
			cached[name] = from
		}
	}
	// reusableNode finds the node that carries a step of run.
	reusableNode := func(run *ir.DAGRunStatus, name string) *ir.Node {
		if run == nil {
			return nil
		}
		for _, node := range run.Nodes {
			if node != nil && node.Step.Name == name && carriesOutputs(node) {
				return node
			}
		}
		return nil
	}

	var reused []ir.ReusedInput
	for _, name := range skipped {
		if from, ok := cached[name]; ok {
			reused = append(reused, ir.ReusedInput{Step: name, RunID: from.RunID, CapturedAt: from.CapturedAt, Via: ir.ReusedInputViaCache})
			continue
		}
		if _, supplied := outputsByName[name]; supplied {
			continue
		}
		run := source
		if own, ok := runs[name]; ok {
			run = own
		}
		if node := reusableNode(run, name); node != nil {
			reused = append(reused, ir.ReusedInput{
				Step:       name,
				RunID:      run.DAGRunID,
				CapturedAt: node.FinishedAt,
				StartedAt:  node.StartedAt,
				FinishedAt: node.FinishedAt,
				Via:        ir.ReusedInputViaRun,
			})
		}
	}
	return reused, nil
}

// setStepOutputs writes caller-supplied outputs onto the skipped nodes.
func setStepOutputs(dag *ir.DAG, nodes []runtime.NodeData, selected map[string]struct{}, outputs map[string]map[string]string) error {
	seen := make(map[string]string)
	for _, ref := range slices.Sorted(maps.Keys(outputs)) {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return err
		}
		if _, ok := selected[name]; ok {
			return fmt.Errorf("cannot set outputs of step %q: it is selected to run", ref)
		}
		for output := range outputs[ref] {
			key := name + "." + output
			if first, ok := seen[key]; ok {
				return fmt.Errorf("output %q of step %q is set by both %q and %q", output, name, first, ref)
			}
			seen[key] = ref
		}
		index := slices.IndexFunc(nodes, func(node runtime.NodeData) bool { return node.Step.Name == name })
		if err := setNodeOutputs(&nodes[index], outputs[ref]); err != nil {
			return fmt.Errorf("step %q: %w", ref, err)
		}
	}
	return nil
}

// setNodeOutputs records values as outputs node published. A name declared
// by the step's outputs contract, or any name when the step has no contract,
// is published for ${steps.<id>.outputs.<name>}; the name of the step's
// string-form output variable is published as that variable.
func setNodeOutputs(node *runtime.NodeData, values map[string]string) error {
	step := node.Step
	declared := make(map[string]ir.StepOutputDeclaration, len(step.Outputs))
	for _, declaration := range step.Outputs {
		declared[declaration.Name] = declaration
	}
	published := map[string]json.RawMessage{}
	if raw := node.State.StepOutputsValue; raw != nil && *raw != "" {
		if err := json.Unmarshal([]byte(*raw), &published); err != nil {
			return fmt.Errorf("read carried outputs: %w", err)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(values)) {
		value := values[name]
		variable := step.Output != "" && name == step.Output
		declaration, isDeclared := declared[name]
		named := isDeclared || (len(declared) == 0 && !variable)
		switch {
		case !named && !variable:
			return fmt.Errorf("output %q is not declared by the step", name)
		case named && !outputNamePattern.MatchString(name):
			return fmt.Errorf("invalid output name %q", name)
		case named && !variable && step.ID == "":
			return fmt.Errorf("output %q cannot be referenced because the step has no id", name)
		case isDeclared && declaration.Type == ir.StepDeclaredOutputTypeJSON && !json.Valid([]byte(value)):
			return fmt.Errorf("output %q must be valid JSON", name)
		}

		if named {
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			published[name] = encoded
		}
		if variable {
			if node.State.OutputVariables == nil {
				node.State.OutputVariables = &collections.SyncMap{}
			}
			node.State.OutputVariables.Store(name, name+"="+value)
		}
	}

	if len(published) == 0 {
		return nil
	}
	encoded, err := json.Marshal(published)
	if err != nil {
		return err
	}
	value := string(encoded)
	node.State.StepOutputsValue = &value
	return nil
}

// resolveStepName maps a step name or ID to the step's name. Names win over
// IDs; validation keeps the two from colliding across steps.
func resolveStepName(dag *ir.DAG, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("step name must not be empty")
	}
	for _, step := range dag.Steps {
		if step.Name == ref {
			return step.Name, nil
		}
	}
	for _, step := range dag.Steps {
		if step.ID != "" && step.ID == ref {
			return step.Name, nil
		}
	}
	names := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		names = append(names, step.Name)
	}
	return "", fmt.Errorf("unknown step %q (available: %s)", ref, strings.Join(names, ", "))
}
