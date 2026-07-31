// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"encoding/json"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
)

type pushBackPayload struct {
	Iteration int                    `json:"iteration"`
	By        string                 `json:"by,omitempty"`
	At        string                 `json:"at,omitempty"`
	Inputs    map[string]string      `json:"inputs,omitempty"`
	History   []pushBackHistoryEntry `json:"history,omitempty"`
}

// pushBackHistoryEntry defines the stable workflow-facing DAG_PUSHBACK history contract.
type pushBackHistoryEntry struct {
	Iteration int               `json:"iteration"`
	By        string            `json:"by,omitempty"`
	At        string            `json:"at,omitempty"`
	Inputs    map[string]string `json:"inputs,omitempty"`
}

func marshalPushBackPayload(allowedInputs []string, state NodeState) (string, error) {
	if state.ApprovalIteration == 0 {
		return "", nil
	}

	history := exec.NormalizePushBackHistory(allowedInputs, state.ApprovalIteration, state.PushBackInputs, state.PushBackHistory)
	payload := pushBackPayload{
		Iteration: state.ApprovalIteration,
		Inputs:    exec.FilterPushBackInputs(allowedInputs, state.PushBackInputs),
		History:   make([]pushBackHistoryEntry, len(history)),
	}
	for i, entry := range history {
		payload.History[i] = pushBackHistoryEntry{
			Iteration: entry.Iteration,
			By:        entry.By,
			At:        entry.At,
			Inputs:    entry.Inputs,
		}
	}
	if len(history) > 0 {
		payload.By = history[len(history)-1].By
		payload.At = history[len(history)-1].At
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
