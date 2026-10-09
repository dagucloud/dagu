// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const askQuestionHeader = "Computer input"

// waitForInput records where to resume, frees the desktop for other steps,
// and puts the step into Waiting until a person answers. The desktop keeps
// its windows as they are.
func (r *run) waitForInput(_ context.Context, index int, spec askSpec) error {
	generation := r.exec.GetAgentSession().Generation
	deadline := time.Now().Add(spec.timeout())
	record := computerhost.Record{
		DAGName:    r.dagName,
		DAGRunID:   r.dagRunID,
		StepName:   r.stepName,
		Generation: generation,
		Deadline:   deadline,
		Cursor:     index + 1,
		Outputs:    r.outputs,
	}
	record.ReplayPending, record.ReplayUsed = r.cache.Held()
	if err := r.store.Save(record); err != nil {
		return r.fail(context.Background(), index, opAsk, err)
	}
	r.shutdown()

	prompt := r.masker.MaskString(spec.Prompt)
	r.timeline.Operation(agentstep.Report{Index: index, Kind: opAsk, Subject: prompt, Status: agentstep.StatusWaiting})
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionWaiting
		s.Usage = r.agentUsage()
		s.Interactions = append(s.Interactions, agentstep.AskInteraction(index, generation, askQuestionHeader, prompt, deadline))
	})
	r.exec.setNodeStatus(ir.NodeWaiting)
	return nil
}

// resume applies the answer to a paused step and returns the operation
// after the ask. An answer that cannot be read back or cleared for now stays
// pending, so a retry can still resume; one that can never be used is
// marked applied, so a retry starts the step over.
func (r *run) resume(session *ir.AgentSession, answer agentstep.AskAnswer) (int, error) {
	// The attempt used these tokens before it paused.
	r.usage = tokenUsage{Input: int(session.Usage.InputTokens), Output: int(session.Usage.OutputTokens)}
	record, err := r.store.Load(r.dagRunID, r.stepName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read the paused step's record: %w", err)
	}
	resumable := err == nil && record.Generation == session.Generation
	if resumable {
		if err := r.store.Delete(r.dagRunID, r.stepName); err != nil {
			return 0, fmt.Errorf("clear the paused step's record: %w", err)
		}
	}
	r.exec.updateSession(func(s *ir.AgentSession) {
		agentstep.MarkApplied(s, answer.InteractionID)
		s.State = ir.AgentSessionRunning
		s.OwnerWorkerID = r.workerID
	})
	if !resumable {
		return 0, errors.New("the paused step can no longer be resumed; retry the step to start over")
	}
	if answer.Rejected {
		return 0, agentstep.ErrAskRejected
	}
	if !record.Waiting(time.Now()) {
		return 0, errors.New("the answer arrived after ask.timeout; retry the step to start over")
	}
	for index, value := range agentstep.AnsweredAsks(session) {
		if index < len(r.cfg.Do) && r.cfg.Do[index].Ask != nil {
			name := r.cfg.Do[index].Ask.As
			r.variables[name] = value
			r.answers[name] = value
		}
	}
	r.refreshMasker()
	maps.Copy(r.outputs, record.Outputs)
	r.cache.Hold(record.ReplayPending, record.ReplayUsed)
	return record.Cursor, nil
}

// refreshMasker rebuilds the masker so answers given through ask
// operations are hidden like secrets.
func (r *run) refreshMasker() {
	masker := agentstep.NewMasker(r.secrets, r.answers)
	r.masker = masker
	r.timeline.Masker = masker
}
