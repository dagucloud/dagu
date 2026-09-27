// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	askInteractionPrefix = "ask-"
	askQuestionHeader    = "Computer input"
)

var errAskRejected = errors.New("the input request was rejected")

// askAnswer is a response to an ask operation that the step has not applied.
type askAnswer struct {
	interactionID string
	rejected      bool
}

// askInteractionID names the interaction for the ask operation at index.
func askInteractionID(index, generation int) string {
	return fmt.Sprintf("%s%d-%d", askInteractionPrefix, index, generation)
}

// parseAskInteractionID returns the operation index and generation encoded
// in an ask interaction ID.
func parseAskInteractionID(id string) (index, generation int, ok bool) {
	rest, found := strings.CutPrefix(id, askInteractionPrefix)
	if !found {
		return 0, 0, false
	}
	indexText, generationText, found := strings.Cut(rest, "-")
	if !found {
		return 0, 0, false
	}
	index, err := strconv.Atoi(indexText)
	if err != nil {
		return 0, 0, false
	}
	generation, err = strconv.Atoi(generationText)
	if err != nil {
		return 0, 0, false
	}
	return index, generation, true
}

// pendingAnswer returns the current generation's answered or rejected ask
// that has not been applied yet.
func pendingAnswer(session *ir.AgentSession) (askAnswer, bool) {
	if session == nil || session.Provider != providerName {
		return askAnswer{}, false
	}
	for _, interaction := range session.Interactions {
		_, generation, ok := parseAskInteractionID(interaction.ID)
		if !ok || generation != session.Generation || interaction.Applied {
			continue
		}
		switch interaction.Status {
		case ir.AgentInteractionRejected:
			return askAnswer{interactionID: interaction.ID, rejected: true}, true
		case ir.AgentInteractionAnswered:
			return askAnswer{interactionID: interaction.ID}, true
		case ir.AgentInteractionPending:
		}
	}
	return askAnswer{}, false
}

func firstAnswer(interaction ir.AgentInteraction) string {
	if len(interaction.Answers) == 0 || len(interaction.Answers[0]) == 0 {
		return ""
	}
	return interaction.Answers[0][0]
}

// waitForInput records where to resume, frees the desktop for other steps,
// and puts the step into Waiting until a person answers. The desktop keeps
// its windows as they are.
func (r *run) waitForInput(_ context.Context, index int, spec askSpec) error {
	generation := r.exec.GetAgentSession().Generation
	deadline := time.Now().Add(spec.timeout())
	record := computerhost.Record{
		ID:         computerhost.RecordID(r.dagRunID, r.stepName),
		DAGName:    r.dagName,
		DAGRunID:   r.dagRunID,
		StepName:   r.stepName,
		Generation: generation,
		Deadline:   deadline,
		Cursor:     index + 1,
		Outputs:    r.outputs,
	}
	if err := r.store.Save(record); err != nil {
		return r.fail(context.Background(), index, opAsk, err)
	}
	r.shutdown()

	prompt := r.masker.MaskString(spec.Prompt)
	r.timeline.operation(operationReport{index: index, kind: opAsk, subject: prompt, status: statusWaiting})
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionWaiting
		s.Usage = r.agentUsage()
		s.Interactions = append(s.Interactions, ir.AgentInteraction{
			ID:     askInteractionID(index, generation),
			Kind:   ir.AgentInteractionQuestion,
			Status: ir.AgentInteractionPending,
			Questions: []ir.AgentQuestion{{
				Header:   askQuestionHeader,
				Question: prompt,
				Custom:   true,
			}},
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			ExpiresAt: deadline.UTC().Format(time.RFC3339Nano),
		})
	})
	r.exec.setNodeStatus(ir.NodeWaiting)
	return nil
}

// resume applies the answer to a paused step and returns the operation
// after the ask.
func (r *run) resume(recordID string, session *ir.AgentSession, answer askAnswer) (int, error) {
	r.exec.updateSession(func(s *ir.AgentSession) {
		for i := range s.Interactions {
			if s.Interactions[i].ID == answer.interactionID {
				s.Interactions[i].Applied = true
			}
		}
		s.State = ir.AgentSessionRunning
		s.OwnerWorkerID = r.workerID
	})
	record, err := r.store.Load(recordID)
	if err != nil || record.Generation != session.Generation {
		return 0, errors.New("the paused step can no longer be resumed; retry the step to start over")
	}
	if err := r.store.Delete(recordID); err != nil {
		return 0, err
	}
	if answer.rejected {
		return 0, errAskRejected
	}
	if !record.Waiting(time.Now()) {
		return 0, errors.New("the answer arrived after ask.timeout; retry the step to start over")
	}
	for _, interaction := range session.Interactions {
		index, generation, ok := parseAskInteractionID(interaction.ID)
		if !ok || generation != session.Generation || interaction.Status != ir.AgentInteractionAnswered {
			continue
		}
		if index < len(r.cfg.Do) && r.cfg.Do[index].Ask != nil {
			name, value := r.cfg.Do[index].Ask.As, firstAnswer(interaction)
			r.variables[name] = value
			r.answers[name] = value
		}
	}
	r.refreshMasker()
	maps.Copy(r.outputs, record.Outputs)
	return record.Cursor, nil
}

// refreshMasker rebuilds the masker so answers given through ask
// operations are hidden like secrets.
func (r *run) refreshMasker() {
	masker := newMasker(r.secrets, r.answers)
	r.masker = masker
	r.timeline.masker = masker
}
