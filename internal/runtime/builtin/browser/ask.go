// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const askQuestionHeader = "Browser input"

// waitForInput leaves the browser running, records where to resume, and
// puts the step into Waiting until a person answers.
func (r *run) waitForInput(ctx context.Context, index int, spec askSpec) error {
	prompt := r.masker.MaskString(spec.Prompt)
	deadline := time.Now().Add(spec.timeout())
	generation := r.exec.GetAgentSession().Generation
	interaction := agentstep.AskInteraction(index, generation, askQuestionHeader, prompt, deadline)
	if err := r.detachForAnswer(ctx, index, index+1, opAsk, deadline); err != nil {
		return err
	}
	r.timeline.Operation(agentstep.Report{Index: index, Kind: opAsk, Subject: prompt, Status: agentstep.StatusWaiting})
	r.awaitAnswer(interaction)
	return nil
}

// awaitAnswer puts the step into Waiting until a person answers
// interaction.
func (r *run) awaitAnswer(interaction ir.AgentInteraction) {
	usage := r.bridge.totals()
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionWaiting
		// The step resumes in a new process, which counts on from here.
		s.Usage = ir.AgentUsage{InputTokens: int64(usage.Input), OutputTokens: int64(usage.Output), TotalTokens: int64(usage.total())}
		s.Interactions = append(s.Interactions, interaction)
	})
	r.exec.setNodeStatus(ir.NodeWaiting)
}

// detachForAnswer leaves the browser running until deadline and records
// that the step resumes at cursor once the question the operation at index
// of kind asks is answered.
func (r *run) detachForAnswer(ctx context.Context, index, cursor int, kind string, deadline time.Time) error {
	if err := r.eng.Detach(ctx); err != nil {
		return r.fail(ctx, index, kind, fmt.Errorf("keep browser open: %w", err))
	}
	r.eng = nil
	r.record.State = browserhost.StateDetached
	r.record.Deadline = deadline
	r.record.Cursor = cursor
	r.record.Outputs = r.outputs
	r.record.ReplayPending, r.record.ReplayUsed = r.cache.Held()
	r.record.WriteGateSeq = r.writeSeq
	r.record.OwnerPID = 0
	r.record.OwnerStartedAt = 0
	if err := r.store.Save(r.record); err != nil {
		return r.fail(ctx, index, kind, err)
	}
	// The record marks the profile as held while the browser waits.
	r.profile.release()
	r.profile = nil
	return nil
}

// resumeSession reattaches to the browser an ask operation left running and
// returns the operation after that ask. An answer whose record cannot be
// read for now stays pending, so a retry can still reattach.
func (r *run) resumeSession(ctx context.Context, recordID string, session *ir.AgentSession, answer agentstep.AskAnswer) (int, error) {
	// The attempt used these tokens before it waited.
	r.bridge.resume(tokenUsage{Input: int(session.Usage.InputTokens), Output: int(session.Usage.OutputTokens)})
	record, err := r.store.Load(recordID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read the waiting browser's record: %w", err)
	}
	r.exec.updateSession(func(s *ir.AgentSession) {
		agentstep.MarkApplied(s, answer.InteractionID)
		s.State = ir.AgentSessionRunning
		s.OwnerWorkerID = r.workerID
	})
	if err != nil || record.State != browserhost.StateDetached || record.Generation != session.Generation {
		return 0, errors.New("the browser waiting for input is no longer running; retry the step to start over")
	}
	r.record = record
	if answer.Rejected {
		return 0, agentstep.ErrAskRejected
	}
	// An answer to a permission before a commit operation says whether
	// that operation sends its write.
	commit := answer.Index < len(r.cfg.Do) && r.cfg.Do[answer.Index].Ask == nil
	if commit {
		r.commitDecisions[answer.Index] = answer.Decision
	}
	if time.Now().After(record.Deadline) {
		wait := "ask.timeout"
		if commit {
			wait = "the wait for a write decision"
		}
		return 0, fmt.Errorf("the answer arrived after %s and the browser was closed; retry the step to start over", wait)
	}
	for index, value := range agentstep.AnsweredAsks(session) {
		if index < len(r.cfg.Do) && r.cfg.Do[index].Ask != nil {
			name := r.cfg.Do[index].Ask.As
			r.variables[name] = value
			r.answers[name] = value
		}
	}
	r.refreshMasker()

	if name := record.Profile; name != "" {
		lease, err := acquireProfile(ctx, r.browser, name, recordID, true)
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
	eng, err := r.exec.launcher.Reattach(ctx, recordedHandle(record), opts)
	if err != nil {
		return 0, err
	}
	r.eng = eng
	r.certificateProxy = record.CertProxyURL != ""
	r.writeSeq = record.WriteGateSeq
	if record.WriteGateURL != "" {
		if r.writeGate, err = writeGateMode(ctx, r.cfg.Browser.WriteGate); err != nil {
			return 0, err
		}
		r.restoreWrites(ctx)
		r.loadCommitPoints()
	}
	if err := r.saveRunningRecord(); err != nil {
		return 0, err
	}
	maps.Copy(r.outputs, record.Outputs)
	r.cache.Hold(record.ReplayPending, record.ReplayUsed)
	r.timeline.Lifecycle(agentstep.StatusRunning, "Resumed browser after input")
	return record.Cursor, nil
}

// refreshMasker rebuilds the masker so answers given through ask
// operations are hidden like secrets.
func (r *run) refreshMasker() {
	masker := agentstep.NewMasker(r.secrets, r.answers)
	r.masker = masker
	r.bridge.masker = masker
	r.timeline.Masker = masker
}
