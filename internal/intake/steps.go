// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
)

// outputNamePattern is the name a step output reference can address.
var outputNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// SelectedStepNodes returns seed nodes for a run of dag that executes only
// steps, each given by step name or ID. Every other step is recorded as
// skipped; when source is set, it carries the outputs source recorded for it.
// outputs sets outputs of skipped steps, keyed by step name or ID and then by
// output name, and takes precedence over outputs carried from source.
func SelectedStepNodes(dag *ir.DAG, steps []string, source *ir.DAGRunStatus, outputs map[string]map[string]string) ([]runtime.NodeData, error) {
	if dag.IsAgent() {
		return nil, fmt.Errorf("selecting steps is not supported for %s DAGs", ir.TypeAgent)
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("at least one step is required")
	}
	if source != nil && (source.Status.IsActive() || source.Status == ir.NotStarted) {
		return nil, fmt.Errorf("dag-run %s is %s; outputs are reused only from a finished run", source.DAGRunID, source.Status)
	}

	selected := make(map[string]struct{}, len(steps))
	for _, ref := range steps {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return nil, err
		}
		selected[name] = struct{}{}
	}

	skipped := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		if _, ok := selected[step.Name]; !ok {
			skipped = append(skipped, step.Name)
		}
	}
	nodes := transform.SeedNodes(dag, source, skipped)
	if err := setStepOutputs(dag, nodes, selected, outputs); err != nil {
		return nil, err
	}
	return nodes, nil
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
