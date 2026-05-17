// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"encoding/json"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// OutputValuesFromNodes extracts typed DAG/action outputs from runtime nodes.
func OutputValuesFromNodes(nodes []NodeData) map[string]any {
	outputs := make(map[string]any)
	for _, node := range nodes {
		for key, value := range node.OutputsValueMap() {
			outputs[key] = value
		}
	}
	if len(outputs) == 0 {
		return nil
	}
	return outputs
}

// OutputValuesFromExecNodes extracts typed DAG/action outputs from persisted nodes.
func OutputValuesFromExecNodes(nodes []*exec.Node) map[string]any {
	outputs := make(map[string]any)
	for _, node := range nodes {
		if node == nil || node.OutputsValue == nil {
			continue
		}
		var values map[string]any
		if err := json.Unmarshal([]byte(*node.OutputsValue), &values); err != nil {
			continue
		}
		for key, value := range values {
			outputs[key] = value
		}
	}
	if len(outputs) == 0 {
		return nil
	}
	return outputs
}
