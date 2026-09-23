// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"encoding/json"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// maxPushBackPayloadSize bounds DAG_PUSHBACK below the 32,767-character limit
// of one Windows environment variable. Recorded inputs are at most
// dagrun.MaxPushBackInputsSize, so the latest inputs always fit.
const maxPushBackPayloadSize = 30 << 10

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

// pushBackAllowlist returns the approval input allowlist that limits the
// approval push-back inputs step receives.
func pushBackAllowlist(step ir.Step) []string {
	if step.Approval == nil {
		return nil
	}
	return step.Approval.Input
}

// visiblePushBackInputs returns the latest push-back inputs step receives.
func visiblePushBackInputs(step ir.Step, state NodeState) map[string]string {
	return dagrun.VisiblePushBackInputs(pushBackAllowlist(step), state.PushBackInputs, state.PushBackHistory)
}

func marshalPushBackPayload(allowedInputs []string, state NodeState) (string, error) {
	if state.ApprovalIteration == 0 {
		return "", nil
	}

	history := dagrun.NormalizePushBackHistory(allowedInputs, state.ApprovalIteration, state.PushBackInputs, state.PushBackHistory)
	payload := pushBackPayload{
		Iteration: state.ApprovalIteration,
		Inputs:    dagrun.VisiblePushBackInputs(allowedInputs, state.PushBackInputs, state.PushBackHistory),
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

	// The payload is one environment variable, so drop the oldest history
	// entries until it fits. The run status keeps the full history.
	data, err := json.Marshal(payload)
	for err == nil && len(data) > maxPushBackPayloadSize && len(payload.History) > 0 {
		payload.History = payload.History[1:]
		data, err = json.Marshal(payload)
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
