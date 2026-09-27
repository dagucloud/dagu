// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
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

// recording is what an act did, so a later run can repeat it without a
// model when the screens match.
type recording struct {
	Width  int                 `json:"width"`
	Height int                 `json:"height"`
	Turns  []recordedTurn      `json:"turns"`
	Final  desktop.Fingerprint `json:"final"`
}

// recordedTurn is a screen the model saw and the actions it chose.
type recordedTurn struct {
	Screen  desktop.Fingerprint `json:"screen"`
	Actions []recordedAction    `json:"actions"`
}

// recordedAction is an action in display pixels. Typed text keeps its
// %name% placeholders, so secrets are never stored.
type recordedAction struct {
	Action computeruse.Action `json:"action"`
	// Target fingerprints the area the action lands on.
	Target *desktop.Fingerprint `json:"target,omitempty"`
}

func recordAction(action computeruse.Action, full *image.RGBA) recordedAction {
	recorded := recordedAction{Action: action}
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
// operation position, instruction and screen size, so an edited
// instruction or a different display misses.
type replayCache = replaycache.Recordings[recording]

func openReplayCache(computerDir, dagName, stepKey string) (*replayCache, error) {
	return replaycache.Open[recording](replaycache.New(computerDir).Path(dagName, stepKey))
}

// replayKey identifies an act operation on a display size.
func replayKey(index int, instruction string, size image.Point) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(index) + "\x00" + instruction + "\x00" + size.String()))
	return hex.EncodeToString(sum[:])
}

// replay repeats a recording while every screen matches what the model saw
// and returns how many turns it completed. It reports false, leaving the
// desktop as it is, when a screen differs or an action fails; the model then
// continues from there.
func (r *run) replay(ctx context.Context, index int, entry recording) (completed int, ok bool, err error) {
	for _, turn := range entry.Turns {
		current, err := r.settle(ctx)
		if err != nil {
			return completed, false, err
		}
		if !matches(current, entry, turn) {
			return completed, false, nil
		}
		for _, recorded := range turn.Actions {
			logAction(r.timeline, index, "replay "+describeAction(recorded.Action))
			if result := r.runAction(ctx, recorded.Action, identity, nil, computeruse.ImageLimit{}); result.Failed() {
				return completed, false, ctx.Err()
			}
		}
		completed++
	}
	final, err := r.settle(ctx)
	if err != nil {
		return completed, false, err
	}
	return completed, desktop.FingerprintOf(final).Distance(entry.Final) <= replayScreenDistance, nil
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
