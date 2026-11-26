// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

var (
	dagOutputNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	// dagOutputRefPattern matches exactly one reference in the short
	// ${<step>.outputs.<name>} or canonical ${steps.<step>.outputs.<name>} form.
	dagOutputRefPattern = regexp.MustCompile(
		`^\$\{\s*(?:steps\.)?([A-Za-z][A-Za-z0-9_]*)\.outputs\.([A-Za-z][A-Za-z0-9_]*)\s*\}$`)
)

// buildDAGOutputs reads the root outputs field (Spec 106). It checks names and
// reference form; validateDAGOutputs checks them against the steps.
func buildDAGOutputs(_ buildContext, d *dag) (map[string]ir.DAGOutput, error) {
	if d.Outputs == nil {
		return nil, nil
	}
	declared, ok := d.Outputs.(map[string]any)
	if !ok {
		return nil, ir.NewValidationError("outputs", nil, fmt.Errorf("outputs must be a mapping of result name to step output reference"))
	}
	outputs := make(map[string]ir.DAGOutput, len(declared))
	var errs ir.ErrorList
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		value := declared[name]
		path := "outputs." + name
		if !dagOutputNamePattern.MatchString(name) {
			errs = append(errs, ir.NewValidationError(path, nil, fmt.Errorf("result name %q must match %s", name, dagOutputNamePattern)))
			continue
		}
		text, ok := value.(string)
		if !ok {
			errs = append(errs, ir.NewValidationError(path, nil, fmt.Errorf("value must be a string holding one step output reference")))
			continue
		}
		match := dagOutputRefPattern.FindStringSubmatch(strings.TrimSpace(text))
		if match == nil {
			errs = append(errs, ir.NewValidationError(path, nil, fmt.Errorf(
				"value %q must be exactly one ${<step id>.outputs.<name>} reference", text)))
			continue
		}
		outputs[name] = ir.DAGOutput{Step: match[1], Output: match[2]}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	if len(outputs) == 0 {
		return nil, nil
	}
	return outputs, nil
}

// validateDAGOutputs checks that each result names a top-level step and an
// output the step can publish.
func validateDAGOutputs(dag *ir.DAG) []error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(dag.Outputs)) {
		out := dag.Outputs[name]
		path := "outputs." + name
		stepIdx := slices.IndexFunc(dag.Steps, func(s ir.Step) bool { return s.ID == out.Step })
		if stepIdx < 0 {
			errs = append(errs, ir.NewValidationError(path, nil, fmt.Errorf(
				"step %q not found among top-level step ids", out.Step)))
			continue
		}
		declared, runtimeOnly := StepOutputs(dag.Steps[stepIdx])
		if runtimeOnly || slices.ContainsFunc(declared, func(d ir.StepOutputDeclaration) bool { return d.Name == out.Output }) {
			continue
		}
		errs = append(errs, ir.NewValidationError(path, nil, fmt.Errorf("step %q does not declare output %q", out.Step, out.Output)))
	}
	return errs
}
