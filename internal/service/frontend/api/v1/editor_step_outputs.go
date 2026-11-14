// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
)

// maxStepOutputBytes is the size at which a step output value is cut.
const maxStepOutputBytes = 65536

// GetDAGRunStepOutputs returns the persisted outputs of one step of a DAG-run.
// The runtime masks secret values before it persists them; this handler never
// changes them.
func (a *API) GetDAGRunStepOutputs(ctx context.Context, request api.GetDAGRunStepOutputsRequestObject) (api.GetDAGRunStepOutputsResponseObject, error) {
	attempt, notFound, err := a.findVisibleAttempt(ctx, request.Name, request.DagRunId)
	if err != nil {
		return nil, err
	}
	if notFound != "" {
		return api.GetDAGRunStepOutputs404JSONResponse{Code: api.ErrorCodeNotFound, Message: notFound}, nil
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("error reading dag-run status: %w", err)
	}
	var node *ir.Node
	if status != nil {
		node = findStepOutputsNode(status, request.StepName)
	}
	if node == nil {
		return api.GetDAGRunStepOutputs404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in dag-run of DAG %s", request.StepName, request.Name),
		}, nil
	}

	return api.GetDAGRunStepOutputs200JSONResponse(toStepOutputs(node)), nil
}

// findStepOutputsNode resolves ref in this order: top-level step name,
// top-level step id, handler step name, handler step id, handler key.
func findStepOutputsNode(status *ir.DAGRunStatus, ref string) *ir.Node {
	for _, node := range status.Nodes {
		if node != nil && node.Step.Name == ref {
			return node
		}
	}
	for _, node := range status.Nodes {
		if node != nil && node.Step.ID != "" && node.Step.ID == ref {
			return node
		}
	}
	handlers := []struct {
		key  string
		node *ir.Node
	}{
		{"handler_on.init", status.OnInit},
		{"handler_on.success", status.OnSuccess},
		{"handler_on.failure", status.OnFailure},
		{"handler_on.abort", status.OnAbort},
		{"handler_on.exit", status.OnExit},
		{"handler_on.wait", status.OnWait},
	}
	for _, h := range handlers {
		if h.node != nil && h.node.Step.Name == ref {
			return h.node
		}
	}
	for _, h := range handlers {
		if h.node != nil && h.node.Step.ID != "" && h.node.Step.ID == ref {
			return h.node
		}
	}
	for _, h := range handlers {
		if h.node != nil && h.key == ref {
			return h.node
		}
	}
	return nil
}

func toStepOutputs(node *ir.Node) api.StepOutputs {
	data := runtimepkg.NodeData{
		Step: node.Step,
		State: runtimepkg.NodeState{
			OutputValue:      node.OutputValue,
			OutputVariables:  node.OutputVariables,
			OutputsValue:     node.OutputsValue,
			StepOutputsValue: node.StepOutputsValue,
		},
	}

	// As in a ${steps.<id>.outputs.<name>} reference, the published payload
	// stands in for a step that has no declared outputs.
	values := data.StepOutputsValueMap()
	if node.StepOutputsValue == nil {
		values = data.OutputsValueStringMap()
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	outputs := make([]api.StepOutputValue, 0, len(names))
	for _, name := range names {
		outputs = append(outputs, toStepOutputValue(name, values[name]))
	}

	result := api.StepOutputs{
		StepName:    node.Step.Name,
		Status:      api.NodeStatus(node.Status),
		StatusLabel: api.NodeStatusLabel(node.Status.String()),
		Outputs:     outputs,
	}
	if node.Step.ID != "" {
		result.StepId = ptrOf(node.Step.ID)
	}
	if value, ok := outputVariableValue(data); ok {
		v := toStepOutputValue(node.Step.Output, value)
		result.OutputVariable = &v
	}
	return result
}

// outputVariableValue returns the value that ${NAME} gives for a step with
// output: NAME. The captured variable comes first: with output_schema, the
// output value holds a normalized copy of it.
func outputVariableValue(data runtimepkg.NodeData) (string, bool) {
	if vars := data.State.OutputVariables; data.Step.Output != "" && vars != nil {
		if raw, ok := vars.Load(data.Step.Output); ok {
			if entry, ok := raw.(string); ok {
				if _, value, found := strings.Cut(entry, "="); found {
					return value, true
				}
			}
		}
	}
	return data.StringFormOutputValue()
}

// toStepOutputValue cuts value at maxStepOutputBytes without splitting a rune.
func toStepOutputValue(name, value string) api.StepOutputValue {
	size := len(value)
	if size <= maxStepOutputBytes {
		return api.StepOutputValue{Name: name, Value: value, Size: size}
	}
	cut := maxStepOutputBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return api.StepOutputValue{Name: name, Value: value[:cut], Size: size, Truncated: true}
}
