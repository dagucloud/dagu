// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// continueNote reminds a model that answered without acting to finish the
// task properly.
const continueNote = "Continue the task. " + computeruse.DoneInstruction

// actOutcome is what a model-driven act did.
type actOutcome struct {
	summary   string
	actions   int
	recording recording
}

func (r *run) act(ctx context.Context, index int, spec actSpec, timeout time.Duration) error {
	// Validation guarantees every reference names a variable or an earlier
	// ask, so a missing value means that ask was skipped.
	for _, name := range agentstep.VariableReferences(spec.Instruction) {
		if _, ok := r.variables[name]; !ok {
			return fmt.Errorf("the instruction uses %%%s%%, but the ask that sets it did not run", name)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	began, before := time.Now(), r.usage

	useCache := r.cache != nil && (spec.Cache == nil || *spec.Cache)
	status := agentstep.StatusCompleted
	key := ""
	if useCache {
		current, err := r.settle(ctx)
		if err != nil {
			return err
		}
		key = replayKey(index, spec.Instruction, current.Bounds().Size())
		if entry, ok := r.cache.lookup(key); ok {
			replayed, err := r.replay(ctx, index, entry)
			if err != nil {
				return err
			}
			if replayed {
				r.report(ctx, agentstep.Report{
					Index: index, Kind: opAct, Subject: spec.Instruction, Status: agentstep.StatusCacheHit,
					Detail: fmt.Sprintf("replayed %d turns", len(entry.Turns)), Duration: time.Since(began),
				})
				return nil
			}
			status = agentstep.StatusHealed
		}
	}

	outcome, err := r.drive(ctx, index, spec)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
			return fmt.Errorf("act did not finish within %s", timeout)
		}
		return err
	}
	if useCache && len(outcome.recording.Turns) > 0 {
		if err := r.cache.store(key, outcome.recording); err != nil {
			return err
		}
	}
	r.report(ctx, agentstep.Report{
		Index: index, Kind: opAct, Subject: spec.Instruction, Status: status,
		Detail:   fmt.Sprintf("%s (%d actions)", outcome.summary, outcome.actions),
		Tokens:   r.usage.sub(before).total(),
		Duration: time.Since(began),
	})
	return nil
}

// drive runs the task with the models in order. A later model takes over
// only when an earlier one failed before touching the desktop.
func (r *run) drive(ctx context.Context, index int, spec actSpec) (actOutcome, error) {
	var errs []error
	for _, m := range r.models {
		outcome, touched, err := r.driveModel(ctx, index, spec, m)
		if err == nil {
			return outcome, nil
		}
		if touched || ctx.Err() != nil {
			return actOutcome{}, err
		}
		errs = append(errs, fmt.Errorf("%s: %w", m.label(), err))
	}
	return actOutcome{}, fmt.Errorf("model request failed: %w", errors.Join(errs...))
}

// driveModel loops between the model and the desktop until the model
// reports the task done. touched reports whether any action ran.
func (r *run) driveModel(ctx context.Context, index int, spec actSpec, m model) (outcome actOutcome, touched bool, err error) {
	session, err := r.exec.newSession(m.providerType, m.provider, r.cfg.mode(), computeruse.Options{
		Model:       m.cfg.Model,
		Task:        spec.Instruction,
		System:      r.environmentNote(),
		MaxTokens:   m.cfg.MaxTokens,
		Temperature: m.cfg.Temperature,
	})
	if err != nil {
		return actOutcome{}, false, err
	}
	limit := session.ImageLimit()
	seen, err := r.observe(ctx, limit)
	if err != nil {
		return actOutcome{}, false, err
	}
	budget := r.cfg.maxActions(spec)
	outcome.recording.Width, outcome.recording.Height = seen.full.Bounds().Dx(), seen.full.Bounds().Dy()
	obs := computeruse.Observation{Screen: seen.forModel()}
	reminded := false
	for {
		turn, err := session.Next(ctx, obs)
		if err != nil {
			return outcome, touched, err
		}
		r.usage.add(turn.Usage)
		if turn.Text != "" {
			logAction(r.timeline, index, "model: "+agentstep.QuoteShort(turn.Text))
		}
		if turn.Confirmation != "" && len(turn.Actions) > 0 && r.cfg.OnConfirmation != confirmationAllow {
			return outcome, touched, fmt.Errorf("the model provider asks a person to confirm the next actions (%s); add an ask operation before this act and set on_confirmation: allow", turn.Confirmation)
		}
		if outcome.actions+len(turn.Actions) > budget {
			return outcome, touched, fmt.Errorf("the task needed more than max_actions (%d) actions", budget)
		}

		results, recorded := r.perform(ctx, index, turn.Actions, seen, limit)
		touched = touched || len(turn.Actions) > 0
		outcome.actions += len(turn.Actions)
		if len(recorded) > 0 {
			outcome.recording.Turns = append(outcome.recording.Turns, recordedTurn{Screen: desktop.FingerprintOf(seen.full), Actions: recorded})
		}
		if ctx.Err() != nil {
			return outcome, touched, ctx.Err()
		}

		// A model that finishes in the same turn as a failed action has not
		// seen the failure yet, so it is shown the result first.
		if turn.Done != nil && !anyFailed(results) {
			if !turn.Done.Success {
				return outcome, touched, fmt.Errorf("the model could not complete the task: %s", turn.Done.Summary)
			}
			final := seen.full
			if len(turn.Actions) > 0 {
				if final, err = r.settle(ctx); err != nil {
					return outcome, touched, err
				}
			}
			outcome.recording.Final = desktop.FingerprintOf(final)
			outcome.summary = turn.Done.Summary
			return outcome, touched, nil
		}

		note := ""
		if len(turn.Actions) == 0 {
			if reminded {
				return outcome, touched, fmt.Errorf("the model stopped without reporting the task done: %s", agentstep.QuoteShort(turn.Text))
			}
			reminded = true
			note = continueNote
		} else {
			reminded = false
		}
		if seen, err = r.observe(ctx, limit); err != nil {
			return outcome, touched, err
		}
		obs = computeruse.Observation{
			Screen:       seen.forModel(),
			Results:      results,
			Acknowledged: turn.Confirmation != "",
			Note:         note,
		}
	}
}

func anyFailed(results []computeruse.Result) bool {
	for _, result := range results {
		if result.Failed() {
			return true
		}
	}
	return false
}

// environmentNote tells the model about the desktop and the placeholders it
// may type.
func (r *run) environmentNote() string {
	note := "The computer runs " + osName() + ". Do not ask questions; work with what is on the screen."
	if len(r.variables) > 0 {
		note += "\nValues written as %name% are placeholders for values you cannot see. To enter one, type the placeholder exactly as written, including the percent signs; it is replaced when typed."
	}
	return note
}

func osName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS; use cmd for keyboard shortcuts"
	case "windows":
		return "Windows"
	default:
		return runtime.GOOS
	}
}
