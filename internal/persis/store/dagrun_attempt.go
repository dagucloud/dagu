// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

type DAGRunAttempt struct {
	store    *DAGRunStore
	id       string
	recordID string
	dag      *core.DAG
	mu       sync.Mutex
	open     bool
}

var _ exec.DAGRunAttempt = (*DAGRunAttempt)(nil)

func (a *DAGRunAttempt) ID() string { return a.id }

func (a *DAGRunAttempt) Open(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.open {
		return nil
	}
	if err := a.store.updatePayload(ctx, a.recordID, func(payload *dagRunPayload) error {
		if a.dag != nil {
			payload.DAG = cloneDAG(a.dag)
		}
		return nil
	}); err != nil {
		return err
	}
	a.open = true
	return nil
}

func (a *DAGRunAttempt) Write(ctx context.Context, status exec.DAGRunStatus) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.open {
		return fmt.Errorf("attempt not open")
	}
	return a.store.updatePayload(ctx, a.recordID, func(payload *dagRunPayload) error {
		expectedName := payload.Name
		if a.dag != nil && a.dag.Name != "" {
			expectedName = a.dag.Name
		}
		if status.Name == "" {
			status.Name = expectedName
		} else if status.Name != expectedName {
			return fmt.Errorf("dag-run store: status name %q does not match record name %q", status.Name, expectedName)
		}
		if status.DAGRunID == "" {
			status.DAGRunID = payload.DAGRunID
		} else if status.DAGRunID != payload.DAGRunID {
			return fmt.Errorf("dag-run store: status dag-run ID %q does not match record dag-run ID %q", status.DAGRunID, payload.DAGRunID)
		}
		if status.AttemptID == "" {
			status.AttemptID = payload.AttemptID
		} else if status.AttemptID != payload.AttemptID {
			return fmt.Errorf("dag-run store: status attempt %q does not match record attempt %q", status.AttemptID, payload.AttemptID)
		}
		if status.Root.Zero() {
			status.Root = payload.Root
		} else if status.Root != payload.Root {
			return fmt.Errorf("dag-run store: status root %q does not match record root %q", status.Root.String(), payload.Root.String())
		}
		if status.Parent.Zero() {
			status.Parent = payload.Parent
		} else if status.Parent != payload.Parent {
			return fmt.Errorf("dag-run store: status parent %q does not match record parent %q", status.Parent.String(), payload.Parent.String())
		}
		payload.Name = expectedName
		payload.Status = cloneDAGRunStatus(&status)
		if a.dag != nil {
			payload.DAG = cloneDAG(a.dag)
		}
		return nil
	})
}

func (a *DAGRunAttempt) Close(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.open = false
	return nil
}

func (a *DAGRunAttempt) ReadStatus(ctx context.Context) (*exec.DAGRunStatus, error) {
	payload, err := a.store.payloadByID(ctx, a.recordID)
	if err != nil {
		return nil, err
	}
	if payload.Status == nil {
		return nil, exec.ErrNoStatusData
	}
	return cloneDAGRunStatus(payload.Status), nil
}

func (a *DAGRunAttempt) ReadDAG(ctx context.Context) (*core.DAG, error) {
	payload, err := a.store.payloadByID(ctx, a.recordID)
	if err != nil {
		return nil, err
	}
	if payload.DAG == nil {
		return nil, fmt.Errorf("DAG definition not found")
	}
	return cloneDAG(payload.DAG), nil
}

func (a *DAGRunAttempt) SetDAG(dag *core.DAG) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dag = cloneDAG(dag)
}

func (a *DAGRunAttempt) Abort(ctx context.Context) error {
	return a.store.updatePayload(ctx, a.recordID, func(payload *dagRunPayload) error {
		payload.AbortRequested = true
		return nil
	})
}

func (a *DAGRunAttempt) IsAborting(ctx context.Context) (bool, error) {
	payload, err := a.store.payloadByID(ctx, a.recordID)
	if err != nil {
		return false, err
	}
	return payload.AbortRequested, nil
}

func (a *DAGRunAttempt) Hide(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.open {
		return fmt.Errorf("cannot hide open attempt")
	}
	return a.store.updatePayload(ctx, a.recordID, func(payload *dagRunPayload) error {
		payload.Hidden = true
		return nil
	})
}

func (a *DAGRunAttempt) Hidden() bool {
	// The DAGRunAttempt interface cannot surface storage errors here. Treat a
	// failed read as visible so callers do not silently skip a run on transient
	// backend failures.
	payload, err := a.store.payloadByID(context.Background(), a.recordID)
	return err == nil && payload.Hidden
}

func (a *DAGRunAttempt) WriteOutputs(ctx context.Context, outputs *exec.DAGRunOutputs) error {
	if outputs == nil {
		return nil
	}
	return a.store.updatePayload(ctx, a.recordID, func(payload *dagRunPayload) error {
		copied := *outputs
		copied.Outputs = copyStringMap(outputs.Outputs)
		payload.Outputs = &copied
		return nil
	})
}

func (a *DAGRunAttempt) ReadOutputs(ctx context.Context) (*exec.DAGRunOutputs, error) {
	payload, err := a.store.payloadByID(ctx, a.recordID)
	if err != nil {
		return nil, err
	}
	if payload.Outputs == nil {
		return nil, nil
	}
	copied := *payload.Outputs
	copied.Outputs = copyStringMap(payload.Outputs.Outputs)
	return &copied, nil
}

func (a *DAGRunAttempt) WriteStepMessages(ctx context.Context, stepName string, messages []exec.LLMMessage) error {
	if len(messages) == 0 {
		return nil
	}
	return a.store.updatePayload(ctx, a.recordID, func(payload *dagRunPayload) error {
		if payload.StepMessages == nil {
			payload.StepMessages = make(map[string][]exec.LLMMessage)
		}
		payload.StepMessages[stepName] = append([]exec.LLMMessage(nil), messages...)
		return nil
	})
}

func (a *DAGRunAttempt) ReadStepMessages(ctx context.Context, stepName string) ([]exec.LLMMessage, error) {
	payload, err := a.store.payloadByID(ctx, a.recordID)
	if err != nil {
		return nil, err
	}
	if len(payload.StepMessages[stepName]) == 0 {
		records, err := a.store.recordsForPayload(ctx, payload)
		if err != nil {
			return nil, err
		}
		return latestStepMessagesFromPayloads(records, stepName), nil
	}
	return append([]exec.LLMMessage(nil), payload.StepMessages[stepName]...), nil
}

func (a *DAGRunAttempt) WorkDir() string { return "" }
