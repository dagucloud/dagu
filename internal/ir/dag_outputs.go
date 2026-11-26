// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"bytes"
	"encoding/json"
	"slices"
)

// DAGOutput is a root outputs entry (Spec 106): the step output a named DAG
// result reads, normalized from either reference form.
type DAGOutput struct {
	Step   string `json:"step"`
	Output string `json:"output"`
}

// ProjectDAGOutputs builds the results of a finished run from the DAG
// definition stored with the run and the run's status (Spec 106). It returns
// nil unless the run succeeded or partially succeeded. A result is left out
// when its step published no value or secret masking changed the stored value.
// Each value is a JSON document: the parsed value of a json output, or a JSON
// string for a string output.
func ProjectDAGOutputs(dag *DAG, status *DAGRunStatus) map[string]json.RawMessage {
	if dag == nil || status == nil || len(dag.Outputs) == 0 || !status.Status.IsSuccess() {
		return nil
	}
	results := make(map[string]json.RawMessage, len(dag.Outputs))
	for name, out := range dag.Outputs {
		node := nodeByStepID(status.Nodes, out.Step)
		if node == nil || node.StepOutputsValue == nil || node.MaskedOutputs.hidesPublished(out.Output) {
			continue
		}
		if node.Status == NodeFailed || node.Status == NodeAborted || node.Status == NodeRejected {
			continue
		}
		var values map[string]json.RawMessage
		if json.Unmarshal([]byte(*node.StepOutputsValue), &values) != nil {
			continue
		}
		raw, ok := values[out.Output]
		if !ok {
			continue
		}
		if value, ok := projectValue(declaredOutput(dag, out), raw); ok {
			results[name] = value
		}
	}
	if len(results) == 0 {
		return nil
	}
	return results
}

// nodeByStepID finds a top-level step's node; top-level step IDs are unique.
func nodeByStepID(nodes []*Node, id string) *Node {
	for _, node := range nodes {
		if node != nil && node.Step.ID == id {
			return node
		}
	}
	return nil
}

// declaredOutput returns the step's outputs contract entry (Spec 012) for the
// referenced output, or nil for any other published output, such as a captured
// one or one whose name is known only at run time.
func declaredOutput(dag *DAG, out DAGOutput) *StepOutputDeclaration {
	for _, step := range dag.Steps {
		if step.ID != out.Step {
			continue
		}
		declared := step.ValueOutputs()
		if i := slices.IndexFunc(declared, func(d StepOutputDeclaration) bool { return d.Name == out.Output }); i >= 0 {
			return &declared[i]
		}
	}
	return nil
}

// projectValue turns the stored value of a published output into a result. A
// run stores each contract output as a JSON string holding its text, so a json
// output is parsed from that text when it is valid JSON, and a string output
// stays a JSON string. Any other output keeps the JSON it was published as.
func projectValue(decl *StepOutputDeclaration, raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false
	}
	if decl == nil {
		return raw, true
	}
	var text string
	isString := json.Unmarshal(raw, &text) == nil
	if decl.Type == StepDeclaredOutputTypeJSON {
		if isString && json.Valid([]byte(text)) {
			return json.RawMessage(text), true
		}
		return raw, true
	}
	if isString {
		return raw, true
	}
	quoted, err := json.Marshal(string(raw))
	return quoted, err == nil
}

func (m *MaskedOutputs) hidesPublished(name string) bool {
	return m != nil && (m.AllPublished || slices.Contains(m.Published, name))
}
