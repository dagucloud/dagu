// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// continueNote reminds a model that answered without acting to finish the
// task properly.
const continueNote = "Continue the task. " + computeruse.DoneInstruction

// personNote tells a model why its actions were not run.
const personNote = "A person used the computer after your last screenshot, so your last actions were not run. Continue the task from the current screen."

// repeatNote tells a model that its last round repeated the one before it
// and changed nothing on the screen.
const repeatNote = "You repeated your previous round of actions, and the screen looks the same as it did before them. Repeating them again will not help. If the task is already complete, report done; otherwise take a different approach."

// capNote tells a model that the task has no actions left, and asks for its
// report instead of its next actions.
const capNote = "This task has used all the actions it may take, so your last actions were not run. Do not act again. If the task is complete, report done with success; otherwise report done with success set to false and say what is left."

// verifyNote asks a model that reported the task done in the same turn as
// actions to look at what they did before the report is accepted.
const verifyNote = "Your actions ran and you reported the task done, but you have not seen the result yet. Look at this screen. If the task is complete, report done again without any action; otherwise continue the task."

// actOutcome is what a model-driven act did.
type actOutcome struct {
	summary   string
	actions   int
	recording recording
}

// replayMiss is the failure of an act that may not call the model: its
// recording is missing or no longer fits the screen. The recording is kept
// for a run that lets the model repair it.
type replayMiss struct {
	err error
	// via is how the replay ran before it missed.
	via string
}

func (m replayMiss) Error() string { return "the act cannot run without AI: " + m.err.Error() }
func (m replayMiss) Unwrap() error { return m.err }

func (r *run) act(ctx context.Context, index int, spec actSpec, timeout time.Duration) error {
	// Validation guarantees every reference names a variable or an earlier
	// ask, so a missing value means that ask was skipped.
	for _, name := range agentstep.VariableReferences(spec.Instruction) {
		if _, ok := r.variables[name]; !ok {
			return fmt.Errorf("the instruction uses %%%s%%, but the ask that sets it did not run", name)
		}
	}
	ctx, cancel := r.operationContext(ctx, timeout)
	defer cancel()
	began, before := time.Now(), r.usage

	choice := r.cfg.actChoice(spec)
	status := agentstep.StatusCompleted
	key := ""
	// replayed holds the recorded turns a partial replay completed, which
	// the healed recording keeps, and spent the actions it performed.
	var replayed []recordedTurn
	spent := 0
	if choice != aiEveryRun {
		key = replayKey(index, spec.Instruction)
		entry, ok := r.cache.Lookup(key)
		switch {
		case !ok || len(entry.Turns) == 0:
			if choice == aiNever {
				return replayMiss{err: errors.New("there is no recording of it on this host"), via: agentstep.ViaScreen}
			}
		case entry.Version != recordingVersion:
			// An older recording is ignored once; what this run records
			// replaces it.
			r.cache.Drop(key)
			if choice == aiNever {
				return replayMiss{err: errors.New("its recording on this host is from an older version and was ignored"), via: agentstep.ViaScreen}
			}
		default:
			findWithin, err := r.cfg.findWithin(spec, r.exec.findWithin)
			if err != nil {
				return err
			}
			replay, err := r.replay(ctx, index, entry, r.cfg.maxActions(spec), findWithin)
			if err != nil {
				return err
			}
			if replay.complete {
				// A recording made before recordings knew their position is
				// written back with it, so it can be forgotten on its own.
				if entry.Op != index {
					entry.Op = index
					r.cache.Stage(key, entry)
				}
				r.report(ctx, agentstep.Report{
					Index: index, Kind: opAct, Subject: spec.Instruction, Status: agentstep.StatusCacheHit, Via: replay.via(),
					Detail: fmt.Sprintf("replayed %d turns", len(entry.Turns)), Duration: time.Since(began),
				})
				return nil
			}
			if choice == aiNever {
				return replayMiss{err: errors.New(replay.reason), via: replay.via()}
			}
			logAction(r.timeline, index, "replay stopped: "+replay.reason)
			status = agentstep.StatusHealed
			// The recording is dropped unless the act that heals it records
			// what it did.
			r.cache.Drop(key)
			replayed, spent = entry.Turns[:replay.turns], replay.actions
		}
	}

	outcome, err := r.drive(ctx, index, spec, spent)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
			return fmt.Errorf("act did not finish within %s", timeout)
		}
		return err
	}
	outcome.recording.Turns = append(slices.Clone(replayed), outcome.recording.Turns...)
	outcome.recording.Op = index
	// A successful act records under every choice, so a later switch to
	// replaying starts with a recording.
	if len(outcome.recording.Turns) > 0 {
		if key == "" {
			key = replayKey(index, spec.Instruction)
		}
		r.cache.Stage(key, outcome.recording)
	}
	r.report(ctx, agentstep.Report{
		Index: index, Kind: opAct, Subject: spec.Instruction, Status: status, Via: agentstep.ViaModel,
		Detail:   fmt.Sprintf("%s (%d actions)", outcome.summary, outcome.actions),
		Tokens:   r.usage.sub(before).total(),
		Duration: time.Since(began),
	})
	return nil
}

// drive runs the task with the models in order, after spent actions a
// replay performed. A later model takes over only when an earlier one failed
// before touching the desktop, not when a person kept using it.
func (r *run) drive(ctx context.Context, index int, spec actSpec, spent int) (actOutcome, error) {
	if len(r.models) == 0 {
		return actOutcome{}, errors.New("no model is configured")
	}
	var errs []error
	for _, m := range r.models {
		outcome, touched, err := r.driveModel(ctx, index, spec, m, spent)
		if err == nil {
			return outcome, nil
		}
		if touched || ctx.Err() != nil || errors.Is(err, errDesktopInUse) {
			return actOutcome{}, err
		}
		errs = append(errs, fmt.Errorf("%s: %w", m.label(), err))
	}
	return actOutcome{}, fmt.Errorf("model request failed: %w", errors.Join(errs...))
}

// driveModel runs the task with one model. touched reports whether any
// action ran.
func (r *run) driveModel(ctx context.Context, index int, spec actSpec, m model, spent int) (actOutcome, bool, error) {
	session, err := r.exec.newSession(m.providerType, m.provider, r.cfg.mode(), computeruse.Options{
		Model:       m.cfg.Model,
		Task:        spec.Instruction,
		System:      r.environmentNote(),
		MaxTokens:   m.cfg.MaxTokens,
		Temperature: m.cfg.Temperature,
	})
	if err != nil {
		return actOutcome{}, false, modelFailure{err}
	}
	loop := &actLoop{r: r, index: index, session: session, limit: session.ImageLimit(), budget: r.cfg.maxActions(spec), spent: spent}
	err = loop.run(ctx)
	return loop.outcome, loop.touched, err
}

// actLoop is one model's attempt at an act.
type actLoop struct {
	r       *run
	index   int
	session computeruse.Session
	limit   computeruse.ImageLimit
	budget  int
	// spent counts the actions a replay performed before the model took
	// over, which count toward budget.
	spent int
	// seen is the screen the model last saw.
	seen    screen
	outcome actOutcome
	touched bool
	// elements are the elements the act's actions landed on, in order,
	// from which the recording's landmarks are chosen.
	elements []recordedElement
	// choices maps the ids offered to the model this turn to the elements
	// they name, so click_element resolves to a position.
	choices  map[string]desktop.Element
	reminded bool
	// capped records that the model was told the task has no actions left
	// and asked for its report.
	capped bool
	// lastRound is the previous round of actions, for noticing a repeat.
	lastRound string
}

// maxModelElements bounds how many actionable elements an observation
// offers the model, so a large tree does not bloat the prompt.
const maxModelElements = 40

// run loops between the model and the desktop until the model reports the
// task done.
func (l *actLoop) run(ctx context.Context) error {
	if err := l.r.awaitPerson(ctx); err != nil {
		return err
	}
	var err error
	if l.seen, err = l.r.observe(ctx, l.limit); err != nil {
		return err
	}
	l.outcome.recording.Width, l.outcome.recording.Height = l.seen.full.Bounds().Dx(), l.seen.full.Bounds().Dy()
	obs := computeruse.Observation{Screen: l.seen.forModel(), Elements: l.offerElements()}
	for {
		turn, err := l.session.Next(ctx, obs)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			return modelFailure{err}
		}
		l.r.usage.add(turn.Usage)
		if turn.Text != "" {
			logAction(l.r.timeline, l.index, "model: "+agentstep.QuoteShort(turn.Text))
		}
		over, err := l.admit(turn)
		if err != nil {
			return err
		}
		if over {
			// The task has no actions left. The model is told once, with the
			// screen, and may still report the task done; one that acts
			// again instead ends the act.
			if l.capped {
				return fmt.Errorf("the task needed more than max_actions (%d) actions", l.budget)
			}
			l.capped = true
			logAction(l.r.timeline, l.index, "the task has used the actions it may take; asking the model to report")
			if l.seen, err = l.r.observe(ctx, l.limit); err != nil {
				return err
			}
			obs = computeruse.Observation{Screen: l.seen.forModel(), Results: skippedResults(turn.Actions), Note: capNote}
			continue
		}
		stale, err := l.stale(ctx, turn)
		if err != nil {
			return err
		}
		if stale {
			// The person has stopped. A screen that still looks like the one
			// the model saw runs the actions it chose, so a person's brief
			// use of the desktop costs no model turn; a changed screen goes
			// back to the model.
			same, err := l.unchanged(ctx, turn)
			if err != nil {
				return err
			}
			if same {
				logAction(l.r.timeline, l.index, "a person used the desktop, but the screen still looks the same")
				stale = false
			}
		}
		var results []computeruse.Result
		before := desktop.FingerprintOf(l.seen.full)
		if stale {
			logAction(l.r.timeline, l.index, "a person used the desktop; the model's actions were not run")
			results = skippedResults(turn.Actions)
		} else {
			results = l.apply(ctx, turn)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A model that finishes in the same turn as actions has not seen
		// what they did, so it is shown the screen and asked again; one that
		// finishes beside a failed action likewise sees the failure first. A
		// report without actions is believed.
		verify := turn.Done != nil && !stale && !anyFailed(results)
		if verify && len(turn.Actions) == 0 {
			return l.finish(ctx, turn)
		}
		note, err := l.reminder(turn)
		if err != nil {
			return err
		}
		if stale {
			note = personNote
		}
		if verify {
			note = verifyNote
		}
		if l.seen, err = l.r.observe(ctx, l.limit); err != nil {
			return err
		}
		// A round that repeats the previous one and leaves the screen as it
		// was is going nowhere; the model is told before it tries a third
		// time.
		if round := describeRound(turn.Actions); !stale && len(turn.Actions) > 0 {
			if round == l.lastRound && desktop.FingerprintOf(l.seen.full).Distance(before) <= replayScreenDistance {
				logAction(l.r.timeline, l.index, "the model repeated its previous actions and the screen did not change")
				if note == "" {
					note = repeatNote
				}
			}
			l.lastRound = round
		}
		obs = computeruse.Observation{
			Screen:       l.seen.forModel(),
			Results:      results,
			Acknowledged: turn.Confirmation != "",
			Note:         note,
			Elements:     l.offerElements(),
		}
	}
}

// offerElements lists the actionable elements of the front window for the
// model to target by id, and remembers which element each id names. It is
// empty when the desktop exposes no elements, so a host without them keeps
// working by pixels.
func (l *actLoop) offerElements() []computeruse.Element {
	l.choices = nil
	els, err := l.r.elementsIfAvailable()
	if err != nil {
		return nil
	}
	front, err := els.FrontWindow()
	if err != nil {
		return nil
	}
	outline, err := els.Outline(front, 0)
	if err != nil {
		return nil
	}
	var list []computeruse.Element
	choices := make(map[string]desktop.Element)
	for _, e := range outline {
		if !actionableRole(e.Role) || strings.TrimSpace(e.Name) == "" || e.Bounds.Empty() {
			continue
		}
		id := fmt.Sprintf("e%d", len(list)+1)
		list = append(list, computeruse.Element{ID: id, Role: e.Role, Name: e.Name})
		choices[id] = e
		if len(list) >= maxModelElements {
			break
		}
	}
	l.choices = choices
	return list
}

// actionableRole reports whether the model may usefully click an element of
// this role by id.
func actionableRole(role string) bool {
	switch role {
	case desktop.RoleButton, desktop.RoleTextField, desktop.RoleCheckbox, desktop.RoleRadio,
		desktop.RoleComboBox, desktop.RoleListItem, desktop.RoleMenuItem, desktop.RoleTab,
		desktop.RoleLink, desktop.RoleCell:
		return true
	}
	return false
}

// resolveElements turns each click aimed at an element id into a click at
// the element's centre. An id the latest observation did not offer fails
// that action and skips the rest, so the model is told and can look again.
func (l *actLoop) resolveElements(turn *computeruse.Turn) []computeruse.Result {
	unknown := -1
	for i := range turn.Actions {
		a := &turn.Actions[i]
		if a.ElementID == "" {
			continue
		}
		e, ok := l.choices[a.ElementID]
		if !ok {
			unknown = i
			break
		}
		centre := image.Pt((e.Bounds.Min.X+e.Bounds.Max.X)/2, (e.Bounds.Min.Y+e.Bounds.Max.Y)/2)
		point := l.seen.toModel(centre)
		a.Point = &point
		logAction(l.r.timeline, l.index, fmt.Sprintf("element %s is %s %q", a.ElementID, e.Role, e.Name))
		a.ElementID = ""
	}
	if unknown < 0 {
		return nil
	}
	results := make([]computeruse.Result, len(turn.Actions))
	for i := range turn.Actions {
		if i == unknown {
			results[i] = computeruse.Result{CallID: turn.Actions[i].CallID, Error: fmt.Sprintf("no element %q is on the screen now; look at the latest list and choose one of its ids", turn.Actions[i].ElementID)}
		} else {
			results[i] = computeruse.Result{CallID: turn.Actions[i].CallID, Skipped: true}
		}
	}
	return results
}

// stale waits until nobody has used the desktop for the idle period and
// reports whether a person used it after the screenshot the model saw, in
// which case the turn's actions may no longer fit the screen.
func (l *actLoop) stale(ctx context.Context, turn *computeruse.Turn) (bool, error) {
	if len(turn.Actions) == 0 || l.r.cfg.idle() <= 0 {
		return false, nil
	}
	if err := l.r.awaitPerson(ctx); err != nil {
		return false, err
	}
	return l.r.driver.PersonInputSince(l.seen.capturedAt), nil
}

// unchanged reports whether the screen still looks like the one the model
// chose the turn's actions on, overall and where each action lands, after a
// person used the desktop.
func (l *actLoop) unchanged(ctx context.Context, turn *computeruse.Turn) (bool, error) {
	// A person who clicked another window changed nothing a screenshot
	// shows, but keys and typing now go there. The focus must be the
	// window the model saw; where the system does not report it, a turn
	// that types or presses keys goes back to the model.
	if l.focusLeftModel(turn) {
		return false, nil
	}
	current, err := l.r.settle(ctx)
	if err != nil {
		return false, err
	}
	// The focus may have moved again while the screen settled, so a turn
	// never runs its keys against a window the model did not see.
	if l.focusLeftModel(turn) {
		return false, nil
	}
	chosen := recordedTurn{Screen: desktop.FingerprintOf(l.seen.full)}
	for _, action := range turn.Actions {
		if display, ok := l.r.toDisplay(action, l.seen); ok {
			chosen.Actions = append(chosen.Actions, recordAction(display, l.seen.full, nil))
		}
	}
	entry := recording{Width: l.seen.full.Bounds().Dx(), Height: l.seen.full.Bounds().Dy()}
	return matches(current, entry, chosen), nil
}

// focusLeftModel reports that the focus is no longer the window the model
// saw, or is unknown while the turn uses the keyboard, in which case the
// turn's keys would go to a window the model did not choose.
func (l *actLoop) focusLeftModel(turn *computeruse.Turn) bool {
	focus := l.r.driver.FocusedWindow()
	if focus.Known() && l.seen.focus.Known() {
		return focus != l.seen.focus
	}
	return usesKeyboard(turn.Actions)
}

// usesKeyboard reports a turn that sends keys to whatever window has the
// focus: typing or key presses, or a pointer action that holds modifiers,
// whose key-down reaches the focus wherever the pointer is.
func usesKeyboard(actions []computeruse.Action) bool {
	for _, action := range actions {
		switch action.Kind {
		case computeruse.KindType, computeruse.KindKey, computeruse.KindHoldKey:
			return true
		case computeruse.KindClick, computeruse.KindMove, computeruse.KindDrag, computeruse.KindScroll:
			if len(action.Modifiers) > 0 {
				return true
			}
		}
	}
	return false
}

// admit rejects a turn whose actions the step may not run, and reports a
// turn whose actions would take the task past its budget.
func (l *actLoop) admit(turn *computeruse.Turn) (over bool, err error) {
	if turn.Confirmation != "" && len(turn.Actions) > 0 && l.r.cfg.OnConfirmation != confirmationAllow {
		return false, fmt.Errorf("the model provider asks a person to confirm the next actions (%s); add an ask operation before this act and set on_confirmation: allow", turn.Confirmation)
	}
	return l.spent+l.outcome.actions+len(turn.Actions) > l.budget, nil
}

// describeRound summarizes a round of actions so two rounds can be compared.
func describeRound(actions []computeruse.Action) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, describeAction(action))
	}
	return strings.Join(parts, "\n")
}

// apply performs a turn's actions and records the ones that completed.
func (l *actLoop) apply(ctx context.Context, turn *computeruse.Turn) []computeruse.Result {
	if early := l.resolveElements(turn); early != nil {
		l.touched = true
		l.outcome.actions += len(turn.Actions)
		return early
	}
	results, recorded := l.r.perform(ctx, l.index, turn.Actions, l.seen, l.limit)
	l.touched = l.touched || len(turn.Actions) > 0
	l.outcome.actions += len(turn.Actions)
	if len(recorded) > 0 {
		l.outcome.recording.Turns = append(l.outcome.recording.Turns, recordedTurn{
			Screen:    desktop.FingerprintOf(l.seen.full),
			Actions:   recorded,
			Confirmed: turn.Confirmation != "",
		})
		for _, rec := range recorded {
			if rec.Element != nil {
				l.elements = append(l.elements, *rec.Element)
			}
		}
	}
	return results
}

// finish ends the act with the model's report.
func (l *actLoop) finish(ctx context.Context, turn *computeruse.Turn) error {
	if !turn.Done.Success {
		return fmt.Errorf("the model could not complete the task: %s", turn.Done.Summary)
	}
	final := l.seen.full
	if len(turn.Actions) > 0 {
		var err error
		if final, err = l.r.settle(ctx); err != nil {
			return err
		}
	}
	l.outcome.recording.Final = desktop.FingerprintOf(final)
	l.outcome.recording.Version = recordingVersion
	if len(l.elements) > 0 {
		l.r.landmarks(&l.outcome.recording, l.elements)
	}
	l.outcome.summary = turn.Done.Summary
	return nil
}

// reminder returns the note sent with the next screen. A model that twice
// answers without acting or reporting the task done is stuck.
func (l *actLoop) reminder(turn *computeruse.Turn) (string, error) {
	if len(turn.Actions) > 0 {
		l.reminded = false
		return "", nil
	}
	if l.reminded {
		return "", fmt.Errorf("the model stopped without reporting the task done: %s", agentstep.QuoteShort(turn.Text))
	}
	l.reminded = true
	return continueNote, nil
}

func skippedResults(actions []computeruse.Action) []computeruse.Result {
	results := make([]computeruse.Result, len(actions))
	for i, action := range actions {
		results[i] = computeruse.Result{CallID: action.CallID, Skipped: true}
	}
	return results
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
