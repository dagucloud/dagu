// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	nodesFieldLabels        = "labels"
	nodesFieldMaxConcurrent = "max_concurrent"
	nodesFieldKeepGoing     = "keep_going"

	// nodeRequiredValue is the one value the DAG field node takes.
	nodeRequiredValue = "required"
	// nodeNameReference is the reference an sftp.download on a node step
	// must use in its destination, so nodes do not write one local path.
	nodeNameReference = "${node.name}"
)

// buildStepNodes parses a step's nodes field: a selector string, a list of
// selector terms, or an object with labels, max_concurrent, and keep_going.
func buildStepNodes(_ stepBuildContext, s *step) (*ir.NodeTargets, error) {
	if s.Nodes == nil {
		return nil, nil
	}
	targets := &ir.NodeTargets{MaxConcurrent: ir.DefaultNodeMaxConcurrent}
	switch v := s.Nodes.(type) {
	case map[string]any:
		if err := parseNodesObject(v, targets); err != nil {
			return nil, err
		}
	default:
		selector, err := nodeSelector(v)
		if err != nil {
			return nil, ir.NewValidationError("nodes", nil, err)
		}
		targets.Selector = selector
	}
	if err := validateLiteralSelector(targets.Selector); err != nil {
		return nil, ir.NewValidationError("nodes", targets.Selector, err)
	}
	return targets, nil
}

func parseNodesObject(obj map[string]any, targets *ir.NodeTargets) error {
	for key := range obj {
		if !slices.Contains([]string{nodesFieldLabels, nodesFieldMaxConcurrent, nodesFieldKeepGoing}, key) {
			return ir.NewValidationError("nodes", nil, fmt.Errorf("unknown field %q; use labels, max_concurrent, or keep_going", key))
		}
	}
	raw, ok := obj[nodesFieldLabels]
	if !ok {
		return ir.NewValidationError("nodes.labels", nil, errors.New("is required"))
	}
	selector, err := nodeSelector(raw)
	if err != nil {
		return ir.NewValidationError("nodes.labels", nil, err)
	}
	targets.Selector = selector

	if raw, ok := obj[nodesFieldMaxConcurrent]; ok {
		n, err := toInt(raw)
		if err != nil || n < 1 || n > ir.MaxNodeMaxConcurrent {
			return ir.NewValidationError("nodes.max_concurrent", raw,
				fmt.Errorf("must be an integer from 1 to %d", ir.MaxNodeMaxConcurrent))
		}
		targets.MaxConcurrent = n
	}
	if raw, ok := obj[nodesFieldKeepGoing]; ok {
		keepGoing, isBool := raw.(bool)
		if !isBool {
			return ir.NewValidationError("nodes.keep_going", raw, errors.New("must be true or false"))
		}
		targets.KeepGoing = keepGoing
	}
	return nil
}

// nodeSelector reads a selector given as a string or as a list of terms.
func nodeSelector(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v), nil
	case []any:
		terms := make([]string, 0, len(v))
		for _, item := range v {
			term, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("each selector term must be a string, got %T", item)
			}
			// Checked here, since a term with a reference defers the rest of
			// the selector's checks to step start.
			if term = strings.TrimSpace(term); term == "" {
				return "", errors.New("each selector term must be non-empty")
			}
			terms = append(terms, term)
		}
		return strings.Join(terms, ","), nil
	default:
		return "", fmt.Errorf("must be a selector string, a list of selector terms, or an object with labels, max_concurrent, and keep_going; got %T", raw)
	}
}

// validateLiteralSelector checks a selector that holds no reference. A
// selector with a reference is checked when the step starts, once the
// reference resolves.
func validateLiteralSelector(selector string) error {
	if strings.Contains(selector, "${") {
		return nil
	}
	filters, err := ir.ParseLabelFilters(strings.Split(selector, ","))
	if err != nil {
		return err
	}
	if len(filters) == 0 {
		return errors.New("selector is empty")
	}
	return nil
}

// validateNodeTargets checks that a step with nodes is a step RFC 013 can
// run on each node.
func validateNodeTargets(result *ir.Step) error {
	if result.Nodes == nil {
		return nil
	}
	for _, conflict := range []struct {
		field string
		set   bool
	}{
		{"parallel", result.Parallel != nil},
		{"foreach", result.Foreach != nil},
		{"worker_selector", len(result.WorkerSelector) > 0},
		{"container", result.Container != nil},
		{"repeat_policy", result.RepeatPolicy.RepeatMode != ""},
	} {
		if conflict.set {
			return ir.NewValidationError("nodes", nil, fmt.Errorf("cannot be combined with %s", conflict.field))
		}
	}

	executor := result.ExecutorConfig
	switch {
	case executor.IsCommand(), executor.Type == "ssh", executor.Type == "sftp",
		executor.Type == ir.ExecutorTypeDAG, executor.Type == ir.ExecutorTypeSubworkflow:
	default:
		return ir.NewValidationError("nodes", nil, fmt.Errorf(
			"is allowed only on dag.run and on command steps (run, command, script, ssh.run, sftp.*), not on %q", executor.Type))
	}

	if executor.Type == "ssh" || executor.Type == "sftp" {
		for _, key := range []string{"host", "ip"} {
			if _, set := executor.Config[key]; set {
				return ir.NewValidationError("with."+key, nil, errors.New("cannot be set on a step with nodes, since each node gives the host"))
			}
		}
	}
	if executor.Type == "sftp" && executor.Config["direction"] == "download" {
		destination, _ := executor.Config["destination"].(string)
		if !strings.Contains(destination, nodeNameReference) {
			return ir.NewValidationError("with.destination", destination,
				fmt.Errorf("must use %s on a step with nodes, so nodes do not write one local path", nodeNameReference))
		}
	}
	if len(result.ShellPackages) > 0 {
		return ir.NewValidationError("with.shell_packages", nil, errors.New("cannot be used on a step with nodes"))
	}
	return nil
}

// buildNodeRequired parses the DAG field node, whose one value is "required".
func buildNodeRequired(_ buildContext, d *dag) (bool, error) {
	switch d.Node {
	case "":
		return false, nil
	case nodeRequiredValue:
		return true, nil
	default:
		return false, ir.NewValidationError("node", d.Node, fmt.Errorf("must be %q", nodeRequiredValue))
	}
}

// validateNodeRequired checks the rules of a DAG that runs only bound to a
// node: the node gives its connection, so the DAG sets none of its own, and
// it targets no further nodes.
func validateNodeRequired(dag *ir.DAG) ir.ErrorList {
	if !dag.NodeRequired {
		return nil
	}
	var errs ir.ErrorList
	for _, conflict := range []struct {
		field string
		set   bool
	}{
		{"ssh", dag.SSH != nil},
		{"container", dag.Container != nil},
		{"worker_selector", len(dag.WorkerSelector) > 0 || dag.ForceLocal},
	} {
		if conflict.set {
			errs = append(errs, ir.NewValidationError("node", nodeRequiredValue,
				fmt.Errorf("cannot be combined with %s, since the bound node gives the connection", conflict.field)))
		}
	}
	walkSteps(dag, func(step ir.Step, path string) {
		if step.Nodes != nil {
			errs = append(errs, ir.NewValidationError(path+".nodes", nil,
				errors.New("a node: required DAG runs on one node and cannot target more nodes")))
		}
	})
	return errs
}
