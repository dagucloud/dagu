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

// recordingVersion is the format of the recordings written now. A
// recording of another version is ignored once and replaced.
const recordingVersion = 2

// recording is what an act did, so a later run can repeat it without a
// model when the screens, or the elements, match.
type recording struct {
	Version int                 `json:"version"`
	Width   int                 `json:"width"`
	Height  int                 `json:"height"`
	Turns   []recordedTurn      `json:"turns"`
	Final   desktop.Fingerprint `json:"final"`
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
}

// recordedTurn is a screen the model saw and the actions it chose.
type recordedTurn struct {
	Screen  desktop.Fingerprint `json:"screen"`
	Actions []recordedAction    `json:"actions"`
	// Confirmed marks a turn the model provider asked a person to confirm.
	Confirmed bool `json:"confirmed,omitempty"`
}

// recordedAction is an action in display pixels. Typed text keeps its
// %name% placeholders, so secrets are never stored.
type recordedAction struct {
	Action computeruse.Action `json:"action"`
	// Target fingerprints the area the action lands on.
	Target *desktop.Fingerprint `json:"target,omitempty"`
	// Element is the element the action landed on or typed into, when the
	// host could read it.
	Element *recordedElement `json:"element,omitempty"`
}

func recordAction(action computeruse.Action, full *image.RGBA, element *recordedElement) recordedAction {
	recorded := recordedAction{Action: action, Element: element}
	if at, ok := target(action); ok {
		fingerprint := desktop.FingerprintAround(full, at, targetRadius(full))
		recorded.Target = &fingerprint
	}
	return recorded
}

func targetRadius(full *image.RGBA) int {
	return max(minTargetRadius, full.Bounds().Dx()/targetRadiusDivisor)
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
// for what it needs. The replay stops, leaving the desktop as it is, when
// a turn misses, an action fails, or the step's settings would stop a
// model's turn: the turn needs more than budget actions in all, or a
// confirmation on_confirmation does not allow. The model then continues
// from there under the same settings.
func (r *run) replay(ctx context.Context, index int, entry recording, budget int, findWithin time.Duration) (replayOutcome, error) {
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
		if err := r.awaitPerson(ctx); err != nil {
			return outcome, err
		}
		if elementTurn(turn) {
			if els == nil {
				els, _ = r.elementsIfAvailable()
			}
			if els != nil {
				outcome.elementTurns++
				reason, err := r.replayByElement(ctx, index, i, total, turn, els, findWithin, &outcome)
				if err != nil || reason != "" {
					outcome.reason = reason
					return outcome, err
				}
				outcome.turns++
				continue
			}
		}
		outcome.pixelTurns++
		current, err := r.awaitScreen(ctx, entry, turn, findWithin)
		if err != nil {
			return outcome, err
		}
		if current == nil {
			outcome.reason = fmt.Sprintf("the screen differs from the recording at turn %d of %d", i+1, total)
			return outcome, nil
		}
		for _, recorded := range turn.Actions {
			logAction(r.timeline, index, "replay by pixels: "+describeAction(recorded.Action))
			outcome.actions++
			if result := r.runAction(ctx, recorded.Action, identity, nil, computeruse.ImageLimit{}); result.Failed() {
				outcome.reason = fmt.Sprintf("%s failed at turn %d of %d: %s", describeAction(recorded.Action), i+1, total, result.Error)
				return outcome, ctx.Err()
			}
		}
		outcome.turns++
	}
	// A replay by elements alone ends on its landmarks, when the recording
	// has some; any other ends on the recorded final screen, which a
	// pixel turn needs anyway.
	if outcome.pixelTurns == 0 && outcome.elementTurns > 0 && len(entry.Landmarks) > 0 && els != nil {
		reason, err := r.awaitLandmarks(ctx, els, entry, findWithin)
		if err != nil {
			return outcome, err
		}
		outcome.complete, outcome.reason = reason == "", reason
		return outcome, nil
	}
	final, err := r.settle(ctx)
	if err != nil {
		return outcome, err
	}
	outcome.complete = desktop.FingerprintOf(final).Distance(entry.Final) <= replayScreenDistance
	if !outcome.complete {
		outcome.reason = "the screen after the last turn differs from the recording"
	}
	return outcome, nil
}

// awaitScreen waits for the screen to look like a recorded turn's,
// capturing again until it does or findWithin passes. It returns nil when
// the screen still differs.
func (r *run) awaitScreen(ctx context.Context, entry recording, turn recordedTurn, findWithin time.Duration) (*image.RGBA, error) {
	deadline := time.Now().Add(findWithin)
	for {
		current, err := r.settle(ctx)
		if err != nil {
			return nil, err
		}
		if matches(current, entry, turn) {
			return current, nil
		}
		if !time.Now().Before(deadline) {
			return nil, nil
		}
		if err := sleep(ctx, min(exactPollInterval, time.Until(deadline))); err != nil {
			return nil, err
		}
	}
}

// matches reports whether a screen looks like the one a recorded turn was
// chosen on, overall and where each action lands.
func matches(current *image.RGBA, entry recording, turn recordedTurn) bool {
	if current.Bounds().Dx() != entry.Width || current.Bounds().Dy() != entry.Height {
		return false
	}
	if desktop.FingerprintOf(current).Distance(turn.Screen) > replayScreenDistance {
		return false
	}
	for _, recorded := range turn.Actions {
		if recorded.Target == nil {
			continue
		}
		at, _ := target(recorded.Action)
		if desktop.FingerprintAround(current, at, targetRadius(current)).Distance(*recorded.Target) > replayTargetDistance {
			return false
		}
	}
	return true
}
