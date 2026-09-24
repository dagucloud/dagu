// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
)

// SelectedStepNodes returns seed nodes for a run of dag that executes only
// steps, each given by step name or ID. Every other step is recorded as
// skipped; when source is set, it carries the outputs source recorded for it.
func SelectedStepNodes(dag *ir.DAG, steps []string, source *ir.DAGRunStatus) ([]runtime.NodeData, error) {
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
	return transform.SeedNodes(dag, source, skipped), nil
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
