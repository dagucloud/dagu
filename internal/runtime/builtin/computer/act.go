// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"cmp"
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

// reportNote asks a model that keeps answering without acting to report the
// task with done, the only tool it is then offered.
const reportNote = "You have not reported the task. Report it now: call done with success set to true if the task is complete, or false with what is wrong or left."

// personNote tells a model why its actions were not run.
const personNote = "A person used the computer after your last screenshot, so your last actions were not run. Continue the task from the current screen."

// divergedNote tells a model its actions were not run because the screen
// changed after the screenshot it chose them on, such as a window that came
// to the front or a dialog that appeared.
const divergedNote = "The screen changed after the screenshot you chose your actions on, so they were not run. Look at this screen and continue the task from here."

// repeatNote tells a model that its last round repeated the one before it
// and changed nothing on the screen.
const repeatNote = "You repeated your previous round of actions, and the screen looks the same as it did before them. Repeating them again will not help. If the task is already complete, report done; otherwise take a different approach."

// stuckNote tells a model that its last round only repeated actions it took
// since the screen last changed, and changed nothing either.
const stuckNote = "You repeated actions you already took, and the screen still looks as it did before them. Doing them again will not help. If the task is already complete, report done; otherwise take a different approach."

// maxStuckRounds is how many rounds that only repeat actions the screen did
// not answer an act allows before it ends. The model is told with each, so
// the rounds after the first are its chance to take a different approach.
const maxStuckRounds = 3

// stillScrollNote tells a model that its scrolls left the window under the
// pointer as it was.
const stillScrollNote = "Your scroll moved nothing on the screen: the spot under the pointer does not scroll, or it is already at its end in that direction. Scroll over the content you mean to move, click inside it first, or use keys such as Page Down."

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

// releaseButtons releases, where the pointer rests, the buttons an act
// pressed and never released, as when a refusal ends the turn before the
// release, so none stays pressed past the act.
func (r *run) releaseButtons(index int) {
	if err := r.driver.ReleaseHeld(); err != nil {
		logAction(r.timeline, index, "a pressed button was not released: "+err.Error())
	}
}

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
	defer r.during(ctx)()
	defer r.releaseButtons(index)
	began, before := time.Now(), r.usage

	choice := r.cfg.actChoice(spec)
	status := agentstep.StatusCompleted
	key := ""
	// replayed holds the recorded turns a partial replay completed, which
	// the healed recording keeps, spent the actions it performed, and
	// handoff what the model is told of them.
	var replayed []recordedTurn
	spent, handoff := 0, ""
	pinned := false
	// A pinned act runs its authored steps first on every device of its
	// system, ahead of this device's own recording.
	if choice != aiEveryRun {
		if steps := spec.Recorded[r.system]; len(steps) > 0 {
			pinned = true
			// A pinned step's placeholders, like the instruction's, must
			// have a value; a missing one means its ask was skipped.
			for _, step := range steps {
				for _, name := range step.references() {
					if _, ok := r.variables[name]; !ok {
						return fmt.Errorf("a pinned step uses %%%s%%, but the ask that sets it did not run", name)
					}
				}
			}
			findWithin, err := r.cfg.findWithin(spec, r.exec.findWithin)
			if err != nil {
				return err
			}
			direct, err := r.cfg.direct(spec)
			if err != nil {
				return err
			}
			outcome, err := r.replayPinned(ctx, index, steps, direct, findWithin)
			if err != nil {
				return err
			}
			if outcome.done {
				r.report(ctx, agentstep.Report{
					Index: index, Kind: opAct, Subject: spec.Instruction, Status: agentstep.StatusCacheHit, Via: agentstep.ViaElement,
					Detail: fmt.Sprintf("replayed %d pinned steps", len(steps)), Duration: time.Since(began),
				})
				return nil
			}
			if choice == aiNever {
				return replayMiss{err: errors.New(outcome.reason), via: agentstep.ViaElement}
			}
			logAction(r.timeline, index, "pinned steps stopped: "+outcome.reason)
			status = agentstep.StatusHealed
			replayed, spent = outcome.ran, outcome.performed
			handoff = replayHandoff(outcome.carried, outcome.reason)
		}
	}
	if !pinned && choice != aiEveryRun {
		key = replayKey(index, spec.Instruction)
		entry, ok := r.cache.Lookup(key)
		switch {
		case !ok || len(entry.Turns) == 0:
			if choice == aiNever {
				return replayMiss{err: errors.New("there is no recording of it on this host"), via: agentstep.ViaScreen}
			}
		case !replayable(entry.Version):
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
			direct, err := r.cfg.direct(spec)
			if err != nil {
				return err
			}
			replay, err := r.replay(ctx, index, entry, r.cfg.maxActions(spec), findWithin, direct)
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
			handoff = replayHandoff(replay.carried, replay.reason)
		}
	}

	outcome, err := r.drive(ctx, index, spec, spent, handoff)
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
// replay performed, which handoff tells the model of. A later model takes
// over only when an earlier one failed before touching the desktop, not
// when a person kept using it.
func (r *run) drive(ctx context.Context, index int, spec actSpec, spent int, handoff string) (actOutcome, error) {
	if len(r.models) == 0 {
		return actOutcome{}, errors.New("no model is configured")
	}
	var errs []error
	for _, m := range r.models {
		outcome, touched, err := r.driveModel(ctx, index, spec, m, spent, handoff)
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
func (r *run) driveModel(ctx context.Context, index int, spec actSpec, m model, spent int, handoff string) (actOutcome, bool, error) {
	session, err := r.exec.newSession(m.providerType, m.provider, r.cfg.mode(), computeruse.Options{
		Model:       m.cfg.Model,
		Task:        spec.Instruction,
		System:      r.environmentNote() + handoff,
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
	choices map[string]desktop.Element
	// stalled counts the model's answers in a row without an action.
	stalled int
	// capped records that the model was told the task has no actions left
	// and asked for its report.
	capped bool
	// lastRound is the previous round of actions, for noticing a repeat.
	lastRound string
	// tried holds the actions the model took since the screen last changed,
	// stillRounds counts the rounds they came in, and stuck the rounds that
	// only repeated tried actions.
	tried       map[string]bool
	stillRounds int
	stuck       int
}

// maxModelElements bounds how many actionable elements an observation
// offers the model, so a large tree does not bloat the prompt, and
// maxModelValue how much of an element's value or hint it shows.
const (
	maxModelElements = 80
	maxModelValue    = 80
)

// run loops between the model and the desktop until the model reports the
// task done.
func (l *actLoop) run(ctx context.Context) error {
	if err := l.r.awaitPerson(ctx); err != nil {
		return err
	}
	// The act works in the application in front when it starts, such as
	// one the step just launched, until its own actions land in another.
	l.r.noteWorkApp(l.r.driver.FocusedWindow())
	offered, err := l.lookAndOffer(ctx)
	if err != nil {
		return err
	}
	l.outcome.recording.Width, l.outcome.recording.Height = l.seen.full.Bounds().Dx(), l.seen.full.Bounds().Dy()
	obs := computeruse.Observation{Screen: l.seen.forModel(), Elements: offered}
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
			// The model acted, though nothing ran, so its answers without an
			// action are counted afresh.
			l.stalled = 0
			logAction(l.r.timeline, l.index, "the task has used the actions it may take; asking the model to report")
			offered, err := l.lookAndOffer(ctx)
			if err != nil {
				return err
			}
			// Only a budget with nothing left takes the actions away; one
			// that a batch overran may still fit a smaller one.
			obs = computeruse.Observation{
				Screen:     l.seen.forModel(),
				Results:    skippedResults(turn.Actions),
				Note:       capNote,
				ReportOnly: l.exhausted(),
				Elements:   offered,
			}
			continue
		}
		// Before running the turn's actions, make sure the world still matches
		// the screen the model chose them on. A person who used the desktop,
		// or any other change that moved or covered the target between the
		// screenshot and now, sends the model a fresh screen instead of acting
		// on a stale one.
		runActions, skipNote, err := l.reconcile(ctx, turn)
		if err != nil {
			return err
		}
		var results []computeruse.Result
		// The screen the actions ran on, and for a turn of scrolls the
		// windows they scroll, tell whether the actions changed anything.
		before := l.seen.full
		var scrolled []view
		if runActions && onlyScrolls(turn.Actions) {
			for _, action := range turn.Actions {
				scrolled = append(scrolled, l.view([]computeruse.Action{action}))
			}
		}
		if runActions {
			results = l.apply(ctx, turn)
		} else {
			results = skippedResults(turn.Actions)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A model that finishes in the same turn as actions has not seen
		// what they did, so it is shown the screen and asked again; one that
		// finishes beside a failed action likewise sees the failure first. A
		// report without actions is believed.
		verify := turn.Done != nil && runActions && !anyFailed(results)
		if verify && len(turn.Actions) == 0 {
			return l.finish(ctx, turn)
		}
		note, reportOnly, err := l.reminder(turn)
		if err != nil {
			return err
		}
		if skipNote != "" {
			note = skipNote
		}
		if verify {
			note = verifyNote
		}
		offered, err := l.lookAndOffer(ctx)
		if err != nil {
			return err
		}
		// Scrolls that left every window they scroll as it was reached
		// nothing that scrolls, which the model is told at once, beside any
		// request to check its report.
		if len(scrolled) > 0 && !anyFailed(results) && allStill(scrolled, before, l.seen.full) {
			logAction(l.r.timeline, l.index, "the scroll moved nothing on the screen")
			note = strings.TrimSpace(note + " " + stillScrollNote)
		}
		// A round that repeats the previous one and leaves the screen as it
		// was is going nowhere; the model is told before it tries a third
		// time. The screens are compared pixel by pixel, since a list that
		// scrolled can keep the screen's fingerprint.
		if round := describeRound(turn.Actions); runActions && len(turn.Actions) > 0 {
			if round == l.lastRound && displayView(before).still(before, l.seen.full) {
				logAction(l.r.timeline, l.index, "the model repeated its previous actions and the screen did not change")
				if note == "" {
					note = repeatNote
				}
			}
			l.lastRound = round
		}
		stuck, err := l.progress(turn.Actions, runActions, before)
		if err != nil {
			return err
		}
		if stuck && note == "" {
			logAction(l.r.timeline, l.index, "the model repeated earlier actions and the screen did not change")
			note = stuckNote
		}
		obs = computeruse.Observation{
			Screen:       l.seen.forModel(),
			Results:      results,
			Acknowledged: turn.Confirmation != "",
			Note:         note,
			ReportOnly:   reportOnly,
			Elements:     offered,
		}
	}
}

// look clears any screen a handler clears, maximizes the front window when
// the step asks for it, then waits for a still screen and captures what the
// model sees next.
func (l *actLoop) look(ctx context.Context) (screen, error) {
	// A window of another app the person had open came forward with nobody
	// at the desktop, as an app that raises itself does: the step's own app
	// comes back before the model looks.
	if front := l.r.driver.FocusedWindow(); l.r.foreign(front) && !l.r.driver.PersonInputSince(l.r.personSince()) {
		l.r.bringForward(ctx, l.index, l.r.homeApp(), "")
	}
	for {
		// A screen a handler clears is cleared before the model sees the
		// screen, so the model never acts on it.
		if _, err := l.r.clearScreens(ctx, l.index); err != nil {
			return screen{}, err
		}
		if err := l.r.fit(ctx, l.index); err != nil {
			return screen{}, err
		}
		shot, err := l.r.observe(ctx, l.limit)
		if err != nil {
			return screen{}, err
		}
		// One that came up while the screen settled is cleared too, and
		// the screen looked at again.
		handled, err := l.r.handleScreens(ctx, l.index)
		if err != nil || !handled {
			return shot, err
		}
	}
}

// lookAndOffer looks at the screen and lists its elements for the model,
// both from one screen with no screen a handler clears: one that comes up
// between the two is cleared, and both are taken again.
func (l *actLoop) lookAndOffer(ctx context.Context) ([]computeruse.Element, error) {
	for {
		var err error
		if l.seen, err = l.look(ctx); err != nil {
			return nil, err
		}
		offered := l.offerElements()
		handled, err := l.r.handleScreens(ctx, l.index)
		if err != nil || !handled {
			return offered, err
		}
	}
}

// offerElements lists the actionable elements of the front window for the
// model to target by id, and remembers which element each id names. It is
// empty when the desktop exposes no elements, so a host without them keeps
// working by pixels.
func (l *actLoop) offerElements() []computeruse.Element {
	l.choices = nil
	// The window in front is another app's the step never operates, so
	// none of its elements is offered.
	if l.r.foreign(l.r.driver.FocusedWindow()) {
		return nil
	}
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
	// The options of an open menu come first, so a long window does not
	// leave them out; the rest follow in reading order.
	slices.SortStableFunc(outline, func(a, b desktop.Element) int {
		return cmp.Compare(menuRank(a), menuRank(b))
	})
	var list []computeruse.Element
	choices := make(map[string]desktop.Element)
	for _, e := range outline {
		// A field with no name of its own goes by the label beside it, and
		// either is shown with declared secrets masked.
		name := strings.TrimSpace(l.r.masker.MaskString(cmp.Or(strings.TrimSpace(e.Name), e.Label)))
		if !actionableRole(e.Role) || name == "" || e.Bounds.Empty() {
			continue
		}
		id := fmt.Sprintf("e%d", len(list)+1)
		list = append(list, computeruse.Element{ID: id, Role: e.Role, Name: name, Value: l.r.modelValue(e, name), Hint: l.r.modelHint(e)})
		choices[id] = e
		if len(list) >= maxModelElements {
			break
		}
	}
	l.choices = choices
	return list
}

// menuRank puts the items of an open menu before every other element.
func menuRank(e desktop.Element) int {
	if e.Role == desktop.RoleMenuItem {
		return 0
	}
	return 1
}

// modelValue is what the model is shown an element holds: its value, with
// declared secrets masked and every value the step types written as its
// placeholder, cut short when long, and nothing when it only repeats the
// element's name. Secrets are masked first, so a typed value within one
// never leaves the rest of it to be seen.
func (r *run) modelValue(e desktop.Element, name string) string {
	value := strings.TrimSpace(r.placeholderIn(r.masker.MaskString(e.Value)))
	if value == "" || value == name {
		return ""
	}
	return shortened(value)
}

// modelHint is the hint an empty field shows in place of a value, with
// declared secrets masked and cut short when long, so the model can tell it
// from a value. It is nothing for a field that holds a value.
func (r *run) modelHint(e desktop.Element) string {
	if strings.TrimSpace(e.Value) != "" {
		return ""
	}
	return shortened(strings.TrimSpace(r.masker.MaskString(e.Placeholder)))
}

// shortened cuts text the model is shown about an element to maxModelValue
// characters.
func shortened(text string) string {
	if runes := []rune(text); len(runes) > maxModelValue {
		return string(runes[:maxModelValue]) + "…"
	}
	return text
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

// elementCentre is the centre of an element's bounds, in display pixels.
func elementCentre(e desktop.Element) image.Point {
	return image.Pt((e.Bounds.Min.X+e.Bounds.Max.X)/2, (e.Bounds.Min.Y+e.Bounds.Max.Y)/2)
}

// describeElement names an element for the step log.
func describeElement(e desktop.Element) string {
	return fmt.Sprintf("%s %q", e.Role, e.Name)
}

// elementAction names how a click on an element of a role is best carried
// out: an invokable control performs its pattern itself, which works wherever
// the window is and does not depend on a pixel; a text field takes the
// keyboard focus. It mirrors the role mapping a replay uses under input:
// direct. Anything else, such as a double or modified click, has no pattern
// and runs on the screen.
func elementAction(action computeruse.Action, role string) (pattern desktop.Pattern, focus bool, ok bool) {
	if !plainClick(action) {
		return "", false, false
	}
	switch role {
	case desktop.RoleButton, desktop.RoleMenuItem, desktop.RoleLink, desktop.RoleOther:
		return desktop.PatternInvoke, false, true
	case desktop.RoleCheckbox:
		return desktop.PatternToggle, false, true
	case desktop.RoleRadio, desktop.RoleListItem, desktop.RoleTab, desktop.RoleCell:
		return desktop.PatternSelect, false, true
	case desktop.RoleTextField:
		return "", true, true
	}
	// A combo box is clicked where it is, as a person would: a click opens
	// one that only offers a list, and puts the caret in one that takes
	// typing, where opening its list would take the typing instead.
	return "", false, false
}

// invokeElement carries out a click the model aimed at an element through the
// element itself, rather than a click at a pixel the window may have moved
// away from or another window may now cover. It reports done=false when the
// host reads no elements or the element offers no pattern for the action, so
// the caller clicks the element's position on the screen instead.
func (l *actLoop) invokeElement(ctx context.Context, els desktop.Elements, action computeruse.Action, e desktop.Element) (done bool, rec *recordedAction, result computeruse.Result) {
	if els == nil {
		return false, nil, computeruse.Result{}
	}
	pattern, focus, ok := elementAction(action, e.Role)
	if !ok {
		return false, nil, computeruse.Result{}
	}
	result = computeruse.Result{CallID: action.CallID}
	var before *bool
	// The element is acted on wherever it is now, so the step makes sure
	// that is not a window it never operates, as a pointer action does.
	if own, owned := l.r.elementOffLimits(e); owned {
		result.Error = l.r.offLimitsMessage(own)
		return true, nil, result
	}
	if why, refused := l.r.refusedElement(e); refused {
		result.Error = why
		return true, nil, result
	}
	if focus {
		if err := els.Focus(e); err != nil {
			result.Error = l.r.masker.MaskString(err.Error())
			return true, nil, result
		}
		logAction(l.r.timeline, l.index, "focus "+describeElement(e))
	} else {
		// The box's state is read right before the click, since an earlier
		// click of this turn may have changed it since the step listed it.
		before = l.r.checkedNow(els, e)
		switch err := l.r.applyPattern(els, e, pattern, ""); {
		case err == nil:
			logAction(l.r.timeline, l.index, string(pattern)+" "+describeElement(e))
		case errors.Is(err, desktop.ErrNoPattern):
			return false, nil, computeruse.Result{}
		default:
			result.Error = l.r.masker.MaskString(err.Error())
			return true, nil, result
		}
	}
	l.touched = true
	recorded := l.recordInvoked(ctx, action, e, before)
	return true, &recorded, result
}

// chooseElement sets the choice control the model named to the option it
// named, and records the choice by the control and the option, the option
// written as its placeholder when it is a variable's value, so a replay
// chooses each row's own.
func (l *actLoop) chooseElement(ctx context.Context, els desktop.Elements, action computeruse.Action, e desktop.Element) (computeruse.Result, *recordedAction) {
	result := computeruse.Result{CallID: action.CallID}
	if els == nil {
		result.Error = "this host cannot read the control to choose in; click through its list instead"
		return result, nil
	}
	if own, owned := l.r.elementOffLimits(e); owned {
		result.Error = l.r.offLimitsMessage(own)
		return result, nil
	}
	if why, refused := l.r.refusedElement(e); refused {
		result.Error = why
		return result, nil
	}
	option := l.r.substitute(action.Text)
	reason, err := l.r.choose(ctx, l.index, els, e, option)
	switch {
	case err != nil:
		result.Error = l.r.masker.MaskString(err.Error())
		return result, nil
	case reason != "":
		result.Error = reason
		return result, nil
	}
	l.touched = true
	centre := elementCentre(e)
	element := l.r.recordElement(e, &centre, l.r.driver.WindowAt(centre).App)
	rec := recordAction(computeruse.Action{Kind: computeruse.KindChoose, Text: l.r.placeholder(option)}, element)
	return result, &rec
}

// recordInvoked records an element the model invoked as the click it asked
// for, landing at the element's centre, so a later run can replay it by the
// element or, on a host without one, by that pixel.
func (l *actLoop) recordInvoked(ctx context.Context, action computeruse.Action, e desktop.Element, before *bool) recordedAction {
	centre := elementCentre(e)
	display := computeruse.Action{Kind: computeruse.KindClick, Count: action.Count, Button: action.Button, Point: &computeruse.Point{X: centre.X, Y: centre.Y}}
	element := l.r.recordElement(e, &centre, l.r.driver.WindowAt(centre).App)
	rec := recordAction(display, element)
	rec.Checked = l.r.checkedAfter(ctx, display, element, before)
	rec.Invoked = true
	return rec
}

// reconcile waits for a person using the desktop to pause, then reports
// whether the turn's actions may run. They run only when the world still
// matches the screen the model chose them on; otherwise the model is sent a
// fresh screen with a note saying why, so a click or keystroke never lands
// on a window that moved or was covered after the screenshot. A person who
// left the screen exactly as the model saw it costs no extra model turn.
func (l *actLoop) reconcile(ctx context.Context, turn *computeruse.Turn) (run bool, note string, err error) {
	if len(turn.Actions) == 0 {
		return true, "", nil
	}
	person, err := l.personUsed(ctx)
	if err != nil {
		return false, "", err
	}
	// The window a person left in front is theirs: the step never maximizes
	// it, whether or not the turn's actions still run.
	if front := l.r.driver.FocusedWindow(); person && front.Known() {
		l.r.fitted[front.Handle] = true
	}
	same, err := l.unchanged(ctx, turn)
	if err != nil {
		return false, "", err
	}
	if same {
		if person {
			logAction(l.r.timeline, l.index, "a person used the desktop, but the screen still looks the same")
		}
		return true, "", nil
	}
	if person {
		logAction(l.r.timeline, l.index, "a person used the desktop; the model's actions were not run")
		l.returnAfterPerson(ctx)
		return false, personNote, nil
	}
	logAction(l.r.timeline, l.index, "the screen changed before the actions ran; they were not run")
	return false, divergedNote, nil
}

// returnAfterPerson makes way for a person whose use of the desktop held
// back a turn: the application the act was working in comes back to the
// front, so the model's next look is at the task rather than at what the
// person was doing.
func (l *actLoop) returnAfterPerson(ctx context.Context) {
	l.r.personHandled = time.Now()
	if l.r.workApp != "" {
		l.r.bringForward(ctx, l.index, l.r.workApp, "")
	}
}

// personUsed waits until nobody has used the desktop for the idle period and
// reports whether a person used it after the screenshot the model saw. With
// idle off it neither waits nor reports a person, so the world check alone
// guards the actions.
func (l *actLoop) personUsed(ctx context.Context) (bool, error) {
	if l.r.cfg.idle() <= 0 {
		return false, nil
	}
	if err := l.r.awaitPerson(ctx); err != nil {
		return false, err
	}
	return l.r.driver.PersonInputSince(l.seen.capturedAt), nil
}

// unchanged reports whether the screen still looks like the one the model
// chose the turn's actions on, overall and where each action lands.
func (l *actLoop) unchanged(ctx context.Context, turn *computeruse.Turn) (bool, error) {
	// A person who clicked another window changed nothing a screenshot
	// shows, but keys and typing now go there. A turn that types or
	// presses keys goes back to the model when the focus is known to have
	// left the window the model saw.
	if l.focusLeftModel(turn) {
		return false, nil
	}
	current, err := l.r.settleClear(ctx, l.index)
	if err != nil {
		return false, err
	}
	// The focus may have moved again while the screen settled, so a turn
	// never runs its keys against a window the model did not see.
	if l.focusLeftModel(turn) {
		return false, nil
	}
	// Only the window the actions land on is compared, so a change
	// elsewhere, such as a clock or a notification, does not send the turn
	// back to the model.
	v := l.view(turn.Actions)
	chosen := recordedTurn{Screen: v.fingerprint(l.seen.full)}
	for _, action := range turn.Actions {
		if display, ok := l.r.toDisplay(action, l.seen); ok {
			chosen.Actions = append(chosen.Actions, v.place(recordAction(display, nil), l.seen.full))
		}
	}
	if current.Bounds() != l.seen.full.Bounds() {
		return false, nil
	}
	return matches(current, v, chosen), nil
}

// view is where a turn's actions are compared and recorded: the application
// window under its first pointer action, or the window with the keyboard
// focus for a turn that only types or presses keys, or the whole display
// where the system names no window.
func (l *actLoop) view(actions []computeruse.Action) view {
	display := displayView(l.seen.full)
	for _, action := range actions {
		if at, ok := l.pointOf(action); ok {
			return l.r.windowView(l.r.driver.WindowAt(at), display)
		}
	}
	return l.r.windowView(l.r.driver.FocusedWindow(), display)
}

// pointerApps is the window each of a turn's pointer actions lands in, as
// the model saw the screen; other actions land in none.
func (l *actLoop) pointerApps(actions []computeruse.Action) []desktop.WindowID {
	apps := make([]desktop.WindowID, len(actions))
	for i, action := range actions {
		if at, ok := l.pointOf(action); ok {
			apps[i], _ = l.r.appWindow(l.r.driver.WindowAt(at))
		}
	}
	return apps
}

// pointOf is where a pointer action lands in display pixels: the centre of
// the element it names, or its position on the screen the model saw.
func (l *actLoop) pointOf(action computeruse.Action) (image.Point, bool) {
	if action.ElementID != "" {
		e, ok := l.choices[action.ElementID]
		if !ok {
			return image.Point{}, false
		}
		return elementCentre(e), true
	}
	display, ok := l.r.toDisplay(action, l.seen)
	if !ok {
		return image.Point{}, false
	}
	return target(display)
}

// focusLeftModel reports that a turn's keys would reach a window other than
// the one the model saw. Only a known focus that differs from the known
// window the model saw is evidence of that: an unknown focus, which is what
// a backend that cannot name the focused window reports, is no evidence, so
// the pixel check alone then guards the turn.
func (l *actLoop) focusLeftModel(turn *computeruse.Turn) bool {
	if !usesKeyboard(turn.Actions) {
		return false
	}
	focus := l.r.driver.FocusedWindow()
	return focus.Known() && l.seen.focus.Known() && focus.Handle != l.seen.focus.Handle
}

// usesKeyboard reports a turn that sends keys to whatever window has the
// focus: typing or key presses, or a pointer action that holds modifiers,
// whose key-down reaches the focus wherever the pointer is.
func usesKeyboard(actions []computeruse.Action) bool {
	for _, action := range actions {
		if sendsKeys(action) {
			return true
		}
		switch action.Kind {
		case computeruse.KindClick, computeruse.KindMove, computeruse.KindDrag, computeruse.KindScroll:
			if len(action.Modifiers) > 0 {
				return true
			}
		}
	}
	return false
}

// sendsKeys reports an action that types or presses keys.
func sendsKeys(action computeruse.Action) bool {
	switch action.Kind {
	case computeruse.KindType, computeruse.KindKey, computeruse.KindHoldKey:
		return true
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

// exhausted reports whether the task has no action left.
func (l *actLoop) exhausted() bool {
	return l.spent+l.outcome.actions >= l.budget
}

// onlyScrolls reports a turn that does nothing but scroll.
func onlyScrolls(actions []computeruse.Action) bool {
	for _, action := range actions {
		if action.Kind != computeruse.KindScroll {
			return false
		}
	}
	return len(actions) > 0
}

// allStill reports whether every view shows the same picture in two
// captures of the display.
func allStill(views []view, before, after *image.RGBA) bool {
	for _, v := range views {
		if !v.still(before, after) {
			return false
		}
	}
	return true
}

// progress follows whether the model's rounds change the screen, and ends
// an act going nowhere rather than letting it run to its timeout. A round
// that leaves the screen as it was and only repeats actions taken since it
// last changed is stuck, as in a cycle of focusing a field, selecting all,
// and clicking it again; maxStuckRounds of them end the act. Rounds that
// only look or wait are not counted, but a change they find is.
func (l *actLoop) progress(actions []computeruse.Action, ran bool, before *image.RGBA) (stuck bool, err error) {
	if !ran || len(actions) == 0 {
		return false, nil
	}
	if !displayView(before).still(before, l.seen.full) {
		l.tried, l.stillRounds, l.stuck = nil, 0, 0
		return false, nil
	}
	if l.tried == nil {
		l.tried = map[string]bool{}
	}
	stuck, acted := true, false
	for _, action := range actions {
		if onlyObserves(action) {
			continue
		}
		acted = true
		key := actionKey(action)
		stuck = stuck && l.tried[key]
		l.tried[key] = true
	}
	if !acted {
		return false, nil
	}
	l.stillRounds++
	if !stuck {
		return false, nil
	}
	if l.stuck++; l.stuck >= maxStuckRounds {
		return true, fmt.Errorf("the model kept acting for %d rounds without changing the screen", l.stillRounds)
	}
	return true, nil
}

// onlyObserves reports an action that looks at the screen or waits, which
// is not expected to change it.
func onlyObserves(action computeruse.Action) bool {
	switch action.Kind {
	case computeruse.KindWait, computeruse.KindScreenshot, computeruse.KindZoom, computeruse.KindCursorPosition:
		return true
	}
	return false
}

// describeRound summarizes a round of actions so two rounds can be compared.
func describeRound(actions []computeruse.Action) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, actionKey(action))
	}
	return strings.Join(parts, "\n")
}

// actionKey tells actions apart when looking for a repeat: what an action
// does and the element it names, whose id stays the same while the screen
// does, so clicks on two elements differ.
func actionKey(action computeruse.Action) string {
	return describeAction(action) + " " + action.ElementID
}

// apply performs a turn's actions and records the ones that completed; the
// application the last of them operated becomes the one the act works in.
func (l *actLoop) apply(ctx context.Context, turn *computeruse.Turn) []computeruse.Result {
	l.touched = l.touched || len(turn.Actions) > 0
	l.outcome.actions += len(turn.Actions)
	// The windows pointer actions land in are read before any action runs,
	// as the model saw them.
	v := l.view(turn.Actions)
	apps := l.pointerApps(turn.Actions)
	els, _ := l.r.elementsIfAvailable()
	results := make([]computeruse.Result, len(turn.Actions))
	var recorded []recordedAction
	failed := false
	for i := range turn.Actions {
		action := turn.Actions[i]
		if failed {
			results[i] = computeruse.Result{CallID: action.CallID, Skipped: true}
			continue
		}
		// Keys reach the window in front as they are sent; an action that
		// only looks or waits operates no application.
		app := apps[i]
		if !app.Known() && sendsKeys(action) {
			app = l.r.driver.FocusedWindow()
		}
		var rec *recordedAction
		if action.ElementID != "" {
			e, ok := l.choices[action.ElementID]
			if !ok {
				results[i] = computeruse.Result{CallID: action.CallID, Error: fmt.Sprintf("no element %q is on the screen now; look at the latest list and choose one of its ids", action.ElementID)}
				failed = true
				continue
			}
			logAction(l.r.timeline, l.index, fmt.Sprintf("element %s is %s %q", action.ElementID, e.Role, e.Name))
			if action.Kind == computeruse.KindChoose {
				results[i], rec = l.chooseElement(ctx, els, action, e)
				if results[i].Failed() {
					logAction(l.r.timeline, l.index, "  failed: "+results[i].Error)
					failed = true
					continue
				}
				recorded = appendRecorded(recorded, rec)
				l.r.noteWorkApp(app)
				continue
			}
			if invoked, invokedRec, invokedResult := l.invokeElement(ctx, els, action, e); invoked {
				results[i], rec = invokedResult, invokedRec
				if invokedResult.Failed() {
					logAction(l.r.timeline, l.index, "  failed: "+invokedResult.Error)
					failed = true
					continue
				}
				recorded = appendRecorded(recorded, rec)
				l.r.noteWorkApp(app)
				continue
			}
			// No pattern, or a host without elements: click the element's
			// position on the screen instead.
			point := l.seen.toModel(elementCentre(e))
			action.Point = &point
			action.ElementID = ""
		}
		results[i], rec = l.r.performOne(ctx, l.index, action, l.seen, l.limit)
		if results[i].Failed() {
			failed = true
			continue
		}
		recorded = appendRecorded(recorded, rec)
		l.r.noteWorkApp(app)
	}
	if len(recorded) > 0 {
		for i := range recorded {
			recorded[i] = v.place(recorded[i], l.seen.full)
		}
		l.outcome.recording.Turns = append(l.outcome.recording.Turns, recordedTurn{
			Screen:    v.fingerprint(l.seen.full),
			Window:    v.window,
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

// appendRecorded adds a recorded action when the action produced one.
func appendRecorded(recorded []recordedAction, rec *recordedAction) []recordedAction {
	if rec != nil {
		recorded = append(recorded, *rec)
	}
	return recorded
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
	v := l.r.windowView(l.r.driver.FocusedWindow(), displayView(final))
	l.outcome.recording.Final, l.outcome.recording.FinalWindow = v.fingerprint(final), v.window
	l.outcome.recording.Version = recordingVersion
	l.outcome.recording.System, l.outcome.recording.SavedAt = l.r.system, time.Now().UTC()
	if len(l.elements) > 0 {
		l.r.landmarks(&l.outcome.recording, l.elements)
	}
	l.outcome.summary = turn.Done.Summary
	return nil
}

// reminder returns the note sent with the next screen, and whether it asks
// for the model's report alone. A model that answers without acting or
// reporting the task done is reminded to continue. A model that cannot be
// forced to call a tool may end with text again, so it is then asked for its
// report with nothing else to call; one that still does not report is stuck.
// A model with no action left is not reminded to continue but asked for
// its report alone at once.
func (l *actLoop) reminder(turn *computeruse.Turn) (note string, reportOnly bool, err error) {
	if len(turn.Actions) > 0 {
		l.stalled = 0
		return "", false, nil
	}
	l.stalled++
	if l.exhausted() {
		l.stalled = max(l.stalled, 2)
	}
	switch l.stalled {
	case 1:
		return continueNote, false, nil
	case 2:
		logAction(l.r.timeline, l.index, "the model answered without acting or reporting; asking it to report")
		return reportNote, true, nil
	default:
		return "", false, fmt.Errorf("the model stopped without reporting the task done: %s", agentstep.QuoteShort(turn.Text))
	}
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

// replayHandoff tells the model, when a replay stopped after carrying out
// some of the task, what it already did and why it stopped, so the model
// carries on from the screen rather than doing the task again.
func replayHandoff(carried []recordedAction, reason string) string {
	var done []string
	for _, rec := range carried {
		// Recorded positions are of another screen; the element says what
		// an action was on.
		action := rec.Action
		action.Point, action.Path = nil, nil
		step := describeAction(action)
		if rec.Element != nil {
			step += " on " + rec.Element.describe()
		}
		done = append(done, step)
	}
	if len(done) == 0 {
		return ""
	}
	return "\nThe task was started by replaying what an earlier run did, which already carried out: " + strings.Join(done, "; ") +
		". The replay stopped because " + reason + ". Look at the screen and do only what is still needed; if the task is already done, finish without acting."
}

// environmentNote tells the model about the desktop and the placeholders it
// may type, and what the visible ones stand for.
func (r *run) environmentNote() string {
	note := "The computer runs " + osName() + ". Do not ask questions; work with what is on the screen."
	if len(r.variables) > 0 {
		note += "\nValues written as %name% are placeholders for values you cannot see. To enter one, type the placeholder exactly as written, including the percent signs; it is replaced when typed."
	}
	var shown []string
	for _, name := range slices.Sorted(slices.Values(r.cfg.Visible)) {
		if value, ok := r.variables[name]; ok {
			shown = append(shown, fmt.Sprintf("%%%s%% is %q", name, value))
		}
	}
	if len(shown) > 0 {
		note += "\nYou may see what these placeholders stand for, to find or choose them on the screen, such as an item in a list: " + strings.Join(shown, ", ") + ". To type one, still type its placeholder."
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
