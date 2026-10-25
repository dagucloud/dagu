// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"strconv"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	// replayScreenDistance is how far a screen's fingerprint may drift from
	// the recorded one, such as for a clock, and still replay.
	replayScreenDistance = 8
	// replayTargetDistance is the same bound for the area an action lands
	// on, which must match more closely.
	replayTargetDistance = 6
	// minTargetRadius is the smallest half-width of the area compared
	// around an action's position; larger screens compare a wider area.
	minTargetRadius     = 32
	targetRadiusDivisor = 40
)

// recordingVersion is the format of the recordings written now. Version 3
// adds the window a pixel turn was recorded on. A version 2 recording, which
// has none, still replays on the whole display as it was recorded; an older
// recording is ignored once and replaced. An engine that knows only version
// 2 ignores a version 3 recording rather than click at the window's own
// positions as if they were the display's.
const (
	recordingVersion       = 3
	oldestRecordingVersion = 2
)

// replayable reports a recording version this engine replays.
func replayable(version int) bool {
	return version >= oldestRecordingVersion && version <= recordingVersion
}

// recording is what an act did, so a later run can repeat it without a
// model when the screens, or the elements, match.
type recording struct {
	// Op is the position of the act in with.do, from 0, so one task's
	// recording can be forgotten on its own.
	Op      int                 `json:"op"`
	Version int                 `json:"version"`
	Width   int                 `json:"width"`
	Height  int                 `json:"height"`
	Turns   []recordedTurn      `json:"turns"`
	Final   desktop.Fingerprint `json:"final"`
	// FinalWindow is the window the act ended on, which Final fingerprints
	// alone; Final covers the whole display when it is nil.
	FinalWindow *recordedWindow `json:"final_window,omitempty"`
	// App is the application of the front window when the act ended: the
	// process image name without its extension, which a replay by elements
	// matches instead of the title, since a title often shows the
	// document's contents and changes every run. Window is that window's
	// title, kept to tell apart several windows of the same app and as a
	// fallback when the app cannot be read; Landmarks are up to three
	// elements the act touched that were still there, which a replay by
	// elements checks in place of the final screen.
	App       string            `json:"app,omitempty"`
	Window    string            `json:"window,omitempty"`
	Landmarks []recordedElement `json:"landmarks,omitempty"`
	// Fields are what an extract bound: for each output the element it read
	// the value from, so a later run reads it again with no model. An act
	// has none; an extract has no turns.
	Fields []fieldBinding `json:"fields,omitempty"`
	// System is the system the recording was made on, windows or macos,
	// which keys its pinned form. SavedAt is when the run that made it ran.
	System  string    `json:"system,omitempty"`
	SavedAt time.Time `json:"saved_at,omitzero"`
}

// fieldBinding ties one extract output to the element its value was read
// from, with the type to convert the element's text to. A table field
// binds to a container and its columns instead of a single element.
type fieldBinding struct {
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	Element recordedElement `json:"element"`
	Table   *tableBinding   `json:"table,omitempty"`
}

// tableBinding ties an array-of-objects output to a grid, table, or list:
// the container to read, the application it belongs to, and the column
// each item property reads from, by the header that names it.
type tableBinding struct {
	Container desktop.Selector `json:"container"`
	App       string           `json:"app,omitempty"`
	Window    string           `json:"window,omitempty"`
	Columns   []columnBinding  `json:"columns"`
}

// columnBinding maps an item property to the column header it reads from
// and the type to convert each cell to.
type columnBinding struct {
	Property string `json:"property"`
	Header   string `json:"header"`
	Kind     string `json:"kind"`
}

// recordedTurn is a screen the model saw and the actions it chose. With a
// Window, Screen fingerprints that window alone and the actions' positions
// are relative to its top left; without one, Screen covers the whole
// display and positions are display pixels.
type recordedTurn struct {
	Screen  desktop.Fingerprint `json:"screen"`
	Window  *recordedWindow     `json:"window,omitempty"`
	Actions []recordedAction    `json:"actions"`
	// Confirmed marks a turn the model provider asked a person to confirm.
	Confirmed bool `json:"confirmed,omitempty"`
}

// recordedAction is an action in the pixels of its turn's view. Typed text
// keeps its %name% placeholders, so secrets are never stored.
type recordedAction struct {
	Action computeruse.Action `json:"action"`
	// Target fingerprints the area the action lands on.
	Target *desktop.Fingerprint `json:"target,omitempty"`
	// Element is the element the action landed on or typed into, when the
	// host could read it.
	Element *recordedElement `json:"element,omitempty"`
}

// recordAction records an action in display pixels; its turn's view later
// places it.
func recordAction(action computeruse.Action, element *recordedElement) recordedAction {
	return recordedAction{Action: action, Element: element}
}

// targetRadius is the half-width of the area compared around an action's
// position: wider for a wider view, the same for the same size.
func targetRadius(rect image.Rectangle) int {
	return max(minTargetRadius, rect.Dx()/targetRadiusDivisor)
}

// replayCache stores the recordings of act operations. Entries are keyed by
// operation position and instruction, so an edited instruction misses. A
// recording keeps its display size, which only its pixel turns require.
type replayCache = replaycache.Recordings[recording]

func openReplayCache(computerDir, dagName, stepKey string) *replayCache {
	return replaycache.Open[recording](replaycache.New(computerDir).Path(dagName, stepKey))
}

// replayKey identifies an act operation.
func replayKey(index int, instruction string) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(index) + "\x00" + instruction))
	return hex.EncodeToString(sum[:])
}

// replayOutcome is how far a replay got.
type replayOutcome struct {
	// turns counts the recorded turns replayed in full.
	turns int
	// actions counts the actions performed, including those of a turn cut
	// short by a failed action.
	actions int
	// complete reports that every turn replayed and the final screen
	// matched.
	complete bool
	// reason says why the replay stopped short, for a step that cannot
	// hand the task to the model.
	reason string
	// elementTurns and pixelTurns count the turns tried each way,
	// including one that missed.
	elementTurns, pixelTurns int
}

// via is how the replay ran: by elements when every turn was, else by the
// screen.
func (o replayOutcome) via() string {
	if o.elementTurns > 0 && o.pixelTurns == 0 {
		return agentstep.ViaElement
	}
	return agentstep.ViaScreen
}

// replay repeats a recording while its elements or screens are found. A
// turn whose pointer actions all landed on elements replays on those
// elements, wherever they are now; any other turn replays on the pixels
// it recorded, which need the same display. Each waits up to findWithin
// for what it needs; under direct, an element performs an action itself
// when it can. The replay stops, leaving the desktop as it is, when
// a turn misses, an action fails, or the step's settings would stop a
// model's turn: the turn needs more than budget actions in all, or a
// confirmation on_confirmation does not allow. The model then continues
// from there under the same settings.
func (r *run) replay(ctx context.Context, index int, entry recording, budget int, findWithin time.Duration, direct bool) (replayOutcome, error) {
	var outcome replayOutcome
	total := len(entry.Turns)
	var els desktop.Elements
	for i, turn := range entry.Turns {
		switch {
		case outcome.actions+len(turn.Actions) > budget:
			outcome.reason = fmt.Sprintf("turn %d of %d needs more than max_actions (%d) actions", i+1, total, budget)
			return outcome, nil
		case turn.Confirmed && r.cfg.OnConfirmation != confirmationAllow:
			outcome.reason = fmt.Sprintf("turn %d of %d needs a person's confirmation", i+1, total)
			return outcome, nil
		}
		if _, err := r.resumeAfterPerson(ctx, index); err != nil {
			return outcome, err
		}
		if err := r.fit(ctx, index); err != nil {
			return outcome, err
		}
		if elementTurn(turn) {
			if els == nil {
				els, _ = r.elementsIfAvailable()
			}
			if els != nil {
				outcome.elementTurns++
				reason, err := r.replayByElement(ctx, index, i, total, turn, els, findWithin, direct, &outcome)
				if err != nil || reason != "" {
					outcome.reason = reason
					return outcome, err
				}
				outcome.turns++
				continue
			}
		}
		outcome.pixelTurns++
		v, found, err := r.awaitScreen(ctx, index, entry, turn, findWithin)
		if err != nil {
			return outcome, err
		}
		if !found {
			outcome.reason = fmt.Sprintf("the screen differs from the recording at turn %d of %d", i+1, total)
			if turn.Window != nil {
				outcome.reason = fmt.Sprintf("the window of %s differs from the recording at turn %d of %d", turn.Window.App, i+1, total)
			}
			return outcome, nil
		}
		for j, recorded := range turn.Actions {
			// A screen an earlier action of the turn brought up is cleared
			// before the next lands on it; the turn's screen was checked
			// before its first.
			if j > 0 {
				if err := r.clearScreens(ctx, index); err != nil {
					return outcome, err
				}
			}
			action := v.absolute(recorded.Action)
			logAction(r.timeline, index, "replay by pixels: "+describeAction(action))
			outcome.actions++
			if result := r.runAction(ctx, action, identity, nil, computeruse.ImageLimit{}); result.Failed() {
				outcome.reason = fmt.Sprintf("%s failed at turn %d of %d: %s", describeAction(action), i+1, total, result.Error)
				return outcome, ctx.Err()
			}
		}
		outcome.turns++
	}
	// A replay by elements alone ends on its landmarks, when the recording
	// has some; any other ends on the recorded final screen, which a
	// pixel turn needs anyway.
	if outcome.pixelTurns == 0 && outcome.elementTurns > 0 && len(entry.Landmarks) > 0 && els != nil {
		reason, err := r.awaitLandmarks(ctx, index, els, entry, findWithin)
		if err != nil {
			return outcome, err
		}
		outcome.complete, outcome.reason = reason == "", reason
		return outcome, nil
	}
	final, err := r.settleClear(ctx, index)
	if err != nil {
		return outcome, err
	}
	v, found := r.finalView(final, entry)
	outcome.complete = found && v.fingerprint(final).Distance(entry.Final) <= replayScreenDistance
	if !outcome.complete {
		outcome.reason = "the screen after the last turn differs from the recording"
	}
	return outcome, nil
}

// awaitScreen waits for the screen to look like a recorded turn's,
// capturing again until it does or findWithin passes. It returns the view
// the turn replays in, and false when the screen still differs.
func (r *run) awaitScreen(ctx context.Context, index int, entry recording, turn recordedTurn, findWithin time.Duration) (view, bool, error) {
	deadline := time.Now().Add(findWithin)
	for {
		// A screen a handler clears may cover the window the turn replays
		// in; the wait starts over once it is cleared.
		handled, err := r.handleScreens(ctx, index)
		if err != nil {
			return view{}, false, err
		}
		if handled {
			deadline = time.Now().Add(findWithin)
		}
		// A window that appears while the turn waits, such as one an
		// application is still opening, is maximized as it was when the
		// turn was recorded.
		if err := r.fit(ctx, index); err != nil {
			return view{}, false, err
		}
		current, err := r.settle(ctx)
		if err != nil {
			return view{}, false, err
		}
		if v, ok := r.turnView(current, entry, turn); ok && matches(current, v, turn) {
			return v, true, nil
		}
		if !time.Now().Before(deadline) {
			return view{}, false, nil
		}
		if err := sleep(ctx, min(exactPollInterval, time.Until(deadline))); err != nil {
			return view{}, false, err
		}
	}
}

// turnView finds where a recorded turn is compared now: its window, wherever
// the window is, or the whole display for a turn recorded without one, which
// needs the display size it was recorded on.
func (r *run) turnView(current *image.RGBA, entry recording, turn recordedTurn) (view, bool) {
	if turn.Window == nil {
		return displayView(current), current.Bounds().Dx() == entry.Width && current.Bounds().Dy() == entry.Height
	}
	return r.placedView(current, *turn.Window)
}

// finalView finds where the screen an act ended on is compared now.
func (r *run) finalView(final *image.RGBA, entry recording) (view, bool) {
	if entry.FinalWindow == nil {
		return displayView(final), true
	}
	return r.placedView(final, *entry.FinalWindow)
}

// placedView finds a recorded window now. A window off the display shows
// nothing to compare, so it is not found.
func (r *run) placedView(current *image.RGBA, rec recordedWindow) (view, bool) {
	rect, ok := r.findWindow(rec)
	return view{rect: rect, window: &rec}, ok && rect.Overlaps(current.Bounds())
}

// matches reports whether a screen looks like the one a recorded turn was
// chosen on, within the turn's view, overall and where each action lands.
func matches(current *image.RGBA, v view, turn recordedTurn) bool {
	if v.fingerprint(current).Distance(turn.Screen) > replayScreenDistance {
		return false
	}
	for _, recorded := range turn.Actions {
		if recorded.Target == nil {
			continue
		}
		at, _ := target(v.absolute(recorded.Action))
		if v.targetPrint(current, at).Distance(*recorded.Target) > replayTargetDistance {
			return false
		}
	}
	return true
}
