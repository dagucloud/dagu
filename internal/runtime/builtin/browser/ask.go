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
	generation := r.exec.GetAgentSession().Generation
	if err := r.eng.Detach(ctx); err != nil {
		return r.fail(ctx, index, opAsk, fmt.Errorf("keep browser open: %w", err))
	}
	r.eng = nil
	r.record.State = browserhost.StateDetached
	r.record.Deadline = time.Now().Add(spec.timeout())
	r.record.Cursor = index + 1
	r.record.Outputs = r.outputs
	if r.cache != nil {
		r.record.ReplayPending, r.record.ReplayUsed = r.cache.Held()
	}
	r.record.OwnerPID = 0
	r.record.OwnerStartedAt = 0
	if err := r.store.Save(r.record); err != nil {
		return r.fail(ctx, index, opAsk, err)
	}
	// The record marks the profile as held while the browser waits.
	r.profile.release()
	r.profile = nil

	prompt := r.masker.MaskString(spec.Prompt)
	r.timeline.Operation(agentstep.Report{Index: index, Kind: opAsk, Subject: prompt, Status: agentstep.StatusWaiting})
	usage := r.bridge.totals()
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionWaiting
		// The step resumes in a new process, which counts on from here.
		s.Usage = ir.AgentUsage{InputTokens: int64(usage.Input), OutputTokens: int64(usage.Output), TotalTokens: int64(usage.total())}
		s.Interactions = append(s.Interactions, agentstep.AskInteraction(index, generation, askQuestionHeader, prompt, r.record.Deadline))
	})
	r.exec.setNodeStatus(ir.NodeWaiting)
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
	if time.Now().After(record.Deadline) {
		return 0, errors.New("the answer arrived after ask.timeout and the browser was closed; retry the step to start over")
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
	if r.cache != nil {
		r.cache.Hold(record.ReplayPending, record.ReplayUsed)
	}
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
