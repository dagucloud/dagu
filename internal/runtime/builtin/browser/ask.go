// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	askInteractionPrefix = "ask-"
	askQuestionHeader    = "Browser input"
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

// waitForInput leaves the browser running, records where to resume, and
// puts the step into Waiting until a person answers.
func (r *run) waitForInput(ctx context.Context, index int, spec askSpec) error {
	generation := r.exec.GetAgentSession().Generation
	if err := r.eng.Detach(ctx); err != nil {
		return r.fail(ctx, index, opAsk, fmt.Errorf("keep browser open: %w", err))
	}
	r.eng = nil
	r.record.State = browserhost.StateDetached
	r.record.Deadline = time.Now().Add(spec.timeout())
	r.record.Cursor = index + 1
	r.record.Outputs = r.outputs
	r.record.OwnerPID = 0
	r.record.OwnerStartedAt = 0
	if err := r.store.Save(r.record); err != nil {
		return r.fail(ctx, index, opAsk, err)
	}
	// The record marks the profile as held while the browser waits.
	r.profile.release()
	r.profile = nil

	prompt := r.masker.MaskString(spec.Prompt)
	r.timeline.operation(operationReport{index: index, kind: opAsk, subject: prompt, status: statusWaiting})
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionWaiting
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
		})
	})
	r.exec.setNodeStatus(ir.NodeWaiting)
	return nil
}

// resumeSession reattaches to the browser an ask operation left running and
// returns the operation after that ask.
func (r *run) resumeSession(ctx context.Context, recordID string, session *ir.AgentSession, answer askAnswer) (int, error) {
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
	if err != nil || record.State != browserhost.StateDetached || record.Generation != session.Generation {
		return 0, errors.New("the browser waiting for input is no longer running; retry the step to start over")
	}
	r.record = record
	if answer.rejected {
		return 0, errAskRejected
	}
	if time.Now().After(record.Deadline) {
		return 0, errors.New("the answer arrived after ask.timeout and the browser was closed; retry the step to start over")
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

	if name := record.Profile; name != "" {
		lease, err := acquireProfile(ctx, r.browser, name, r.store, recordID)
		if err != nil {
			return 0, err
		}
		r.profile = lease
	}
	opts := launchOptions{
		DownloadsDir:   record.DownloadsDir,
		AllowedDomains: r.cfg.Browser.AllowedDomains,
		Generate:       r.bridge.generate,
	}
	eng, err := r.exec.launcher.Reattach(ctx, browserHandle{
		CDPURL:       record.CDPURL,
		ExtensionID:  record.ExtensionID,
		ExtensionDir: record.ExtensionDir,
	}, opts)
	if err != nil {
		return 0, err
	}
	r.eng = eng
	if err := r.saveRunningRecord(); err != nil {
		return 0, err
	}
	maps.Copy(r.outputs, record.Outputs)
	r.timeline.lifecycle(statusRunning, "Resumed browser after input")
	return record.Cursor, nil
}

// refreshMasker rebuilds the masker so answers given through ask
// operations are hidden like secrets.
func (r *run) refreshMasker() {
	masker := newMasker(r.secrets, r.answers)
	r.masker = masker
	r.bridge.masker = masker
	r.timeline.masker = masker
}
