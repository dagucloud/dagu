// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

// PinnedOperation is a device's recording of one operation in the form a
// step pins it, for a person to review and write into the step under
// recorded.<system> or fields.<system>.
type PinnedOperation struct {
	// Op is the operation's position in with.do, counted from 1.
	Op int `json:"op"`
	// Kind is act or extract.
	Kind string `json:"kind"`
	// System is the system the recording was made on, windows or macos.
	System string `json:"system,omitempty"`
	// SavedAt is when the run that made the recording ran; it is zero for a
	// recording that does not say.
	SavedAt time.Time `json:"saved_at,omitzero"`
	// Recorded holds an act's pinned steps, set only when every recorded
	// action has a pinned form.
	Recorded []pinnedStep `json:"recorded,omitempty"`
	// Fields holds an extract's pinned fields by output name.
	Fields map[string]pinnedField `json:"fields,omitempty"`
	// Unpinnable lists an act's recorded actions that have no pinned form.
	Unpinnable []UnpinnableAction `json:"unpinnable,omitempty"`
	// Assumes names the variables the act's instruction names that its
	// recorded actions never use, such as an item its list already showed:
	// pinned, the steps leave each as a run finds it, whatever its value.
	Assumes []string `json:"assumes,omitempty"`
	// AssumesUnknown marks an act recorded before recordings noted those
	// variables, whose steps may leave some of them alone.
	AssumesUnknown bool `json:"assumes_unknown,omitempty"`
}

// UnpinnableAction is a recorded action with no pinned form and why: Code
// for a program to act on, and Reason for a person to read.
type UnpinnableAction struct {
	// Turn is the recorded turn the action belongs to, counted from 1.
	Turn   int    `json:"turn"`
	Action string `json:"action"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// Codes of why a recorded action has no pinned form.
const (
	unpinnableNoElement     = "no_element"
	unpinnableEdgeClick     = "edge_click"
	unpinnableClickWithKeys = "click_with_keys"
	unpinnableClick         = "click"
	unpinnableNoField       = "no_field"
	unpinnableKeys          = "keys"
	unpinnableEdgeScroll    = "edge_scroll"
	unpinnableScroll        = "scroll"
	unpinnableMove          = "move"
	unpinnableDrag          = "drag"
	unpinnableHoldKey       = "hold_key"
	unpinnableButton        = "button"
	unpinnableUnsupported   = "unsupported"
)

// unpinnableReasons words each code for a person.
var unpinnableReasons = map[string]string{
	unpinnableNoElement:     "no element could be read where it landed",
	unpinnableEdgeClick:     "the click landed near the edge of its element, and a pin clicks its centre",
	unpinnableClickWithKeys: "a click holding keys has no pinned form",
	unpinnableClick:         "only a single or double left click and a single right click have a pinned form",
	unpinnableNoField:       "the field it typed into could not be read",
	unpinnableKeys:          "the keys cannot be written as a chord such as ctrl+s",
	unpinnableEdgeScroll:    "the scroll landed near the edge of its element, and a pin scrolls at its centre",
	unpinnableScroll:        "only a plain vertical scroll has a pinned form",
	unpinnableMove:          "moving the pointer alone, such as to open a menu by hovering, has no pinned form",
	unpinnableDrag:          "a drag has no pinned form",
	unpinnableHoldKey:       "holding keys has no pinned form",
	unpinnableButton:        "pressing or releasing a mouse button alone has no pinned form",
}

// PinRecording converts one entry of a step's replay cache into its pinned
// form. It reports false for an entry that is not a recording of the current
// format, or one that recorded nothing.
func PinRecording(entry json.RawMessage) (PinnedOperation, bool) {
	var rec recording
	if err := json.Unmarshal(entry, &rec); err != nil || !replayable(rec.Version) {
		return PinnedOperation{}, false
	}
	system := rec.System
	if system == "" {
		// A recording that does not name its system was made here.
		system = hostSystem()
	}
	pinned := PinnedOperation{Op: rec.Op + 1, System: system, SavedAt: rec.SavedAt}
	switch {
	case len(rec.Turns) > 0:
		pinned.Kind = opAct
		pinned.Recorded, pinned.Unpinnable = pinAct(rec.Turns)
		switch {
		case rec.Version < assumingVersion:
			pinned.AssumesUnknown = true
		case rec.Assumed != nil:
			pinned.Assumes = slices.Sorted(maps.Keys(rec.Assumed.Digests))
		}
	case len(rec.Fields) > 0:
		pinned.Kind = opExtract
		pinned.Fields = pinFields(rec.Fields)
	default:
		return PinnedOperation{}, false
	}
	return pinned, true
}

// numberedAction is a recorded action with the turn it belongs to.
type numberedAction struct {
	turn int
	recordedAction
}

// pinAct converts an act's recorded actions into pinned steps in order. Any
// action with no pinned form leaves the act without steps, since a pin that
// skipped one would report success without doing the task.
func pinAct(turns []recordedTurn) ([]pinnedStep, []UnpinnableAction) {
	var all []numberedAction
	for i, turn := range turns {
		for _, rec := range turn.Actions {
			all = append(all, numberedAction{turn: i + 1, recordedAction: rec})
		}
	}
	var steps []pinnedStep
	var unpinnable []UnpinnableAction
	for i, rec := range all {
		if rec.Action.Kind == computeruse.KindMove && i+1 < len(all) && sameTarget(rec.recordedAction, all[i+1].recordedAction) {
			// A pointer step moves the pointer onto its element itself.
			continue
		}
		pinned, code := pinAction(rec.recordedAction)
		if code != "" {
			reason, ok := unpinnableReasons[code]
			if !ok {
				reason = string(rec.Action.Kind) + " has no pinned form"
			}
			unpinnable = append(unpinnable, UnpinnableAction{Turn: rec.turn, Action: describeAction(rec.Action), Code: code, Reason: reason})
			continue
		}
		steps = append(steps, pinned...)
	}
	if len(unpinnable) > 0 {
		return nil, unpinnable
	}
	return steps, nil
}

// sameTarget reports whether next is a pointer action on the element move
// went to.
func sameTarget(move, next recordedAction) bool {
	if _, pointer := target(next.Action); !pointer || move.Element == nil || next.Element == nil {
		return false
	}
	return move.Element.App == next.Element.App && move.Element.Selector == next.Element.Selector
}

// pinAction converts one recorded action into pinned steps, or returns the
// code of why it has no pinned form. A wait of no time converts into no
// steps.
func pinAction(rec recordedAction) ([]pinnedStep, string) {
	action := rec.Action
	switch action.Kind {
	case computeruse.KindClick:
		if rec.Element == nil {
			return nil, unpinnableNoElement
		}
		if offCentre(*rec.Element) {
			return nil, unpinnableEdgeClick
		}
		if len(action.Modifiers) > 0 {
			return nil, unpinnableClickWithKeys
		}
		sel := pinnedSelector(*rec.Element)
		left := action.Button == "" || action.Button == computeruse.ButtonLeft
		switch {
		case rec.Checked != nil && left && action.Count <= 1 && *rec.Checked:
			return []pinnedStep{{Check: &sel}}, ""
		case rec.Checked != nil && left && action.Count <= 1:
			return []pinnedStep{{Uncheck: &sel}}, ""
		case left && action.Count <= 1:
			return []pinnedStep{{Click: &sel}}, ""
		case left && action.Count == 2:
			return []pinnedStep{{DoubleClick: &sel}}, ""
		case action.Button == computeruse.ButtonRight && action.Count <= 1:
			return []pinnedStep{{RightClick: &sel}}, ""
		}
		return nil, unpinnableClick
	case computeruse.KindType:
		if rec.Element == nil {
			return nil, unpinnableNoField
		}
		return []pinnedStep{{Type: &pinnedType{Into: pinnedSelector(*rec.Element), Text: action.Text}}}, ""
	case computeruse.KindChoose:
		if rec.Element == nil {
			return nil, unpinnableNoElement
		}
		return []pinnedStep{{Choose: &pinnedChoose{From: pinnedSelector(*rec.Element), Option: action.Text}}}, ""
	case computeruse.KindKey:
		step := pinnedStep{Key: strings.Join(action.Keys, "+")}
		if step.validate() != nil {
			return nil, unpinnableKeys
		}
		steps := slices.Repeat([]pinnedStep{step}, max(action.Repeat, 1))
		if rec.Element != nil {
			// Only the first press focuses the element the keys went to; each
			// press after it starts from where the one before left the focus,
			// as the recording did.
			steps[0].Focus = new(pinnedSelector(*rec.Element))
		}
		return steps, ""
	case computeruse.KindScroll:
		if rec.Element == nil {
			return nil, unpinnableNoElement
		}
		if offCentre(*rec.Element) {
			return nil, unpinnableEdgeScroll
		}
		if action.ScrollX != 0 || len(action.Modifiers) > 0 {
			return nil, unpinnableScroll
		}
		return []pinnedStep{{Scroll: &pinnedScroll{On: pinnedSelector(*rec.Element), Dy: action.ScrollY}}}, ""
	case computeruse.KindWait:
		if action.Duration <= 0 {
			return nil, ""
		}
		return []pinnedStep{{Wait: min(action.Duration, maxActionPause).String()}}, ""
	case computeruse.KindMove:
		return nil, unpinnableMove
	case computeruse.KindDrag:
		return nil, unpinnableDrag
	case computeruse.KindHoldKey:
		return nil, unpinnableHoldKey
	case computeruse.KindMouseDown, computeruse.KindMouseUp:
		return nil, unpinnableButton
	}
	return nil, unpinnableUnsupported
}

// maxCentreOffset is how far from its element's centre, as a share of the
// element's width or height, a pointer action may have landed and still be
// pinned to the centre.
const maxCentreOffset = 0.3

// offCentre reports a pointer action that landed near its element's edge,
// where a pin, which acts at the centre, may reach another part of it, such
// as a split button's arrow. A text field takes the focus wherever it is
// clicked.
func offCentre(rec recordedElement) bool {
	if rec.Selector.Role == desktop.RoleTextField {
		return false
	}
	return math.Abs(rec.FractionX-0.5) > maxCentreOffset || math.Abs(rec.FractionY-0.5) > maxCentreOffset
}

// containerRoles are the roles of the elements a pinned selector is looked
// for in: the parts a window is divided into, never a row or a cell, whose
// names are the data shown.
var containerRoles = []string{desktop.RoleGroup, desktop.RoleList, desktop.RoleTab, desktop.RoleOther}

// pinnedSelector names a recorded element the way a pin does: by its role,
// name, and id, in its application, or in its window's title when the
// application could not be read. An element without an id is looked for in
// its closest named container, since a name alone often repeats in a
// window, such as a 保存 button in two groups. An element a pinned step
// named keeps its selector as written.
func pinnedSelector(rec recordedElement) desktop.Selector {
	sel := rec.Selector
	sel.App, sel.Window = rec.App, ""
	if sel.App == "" {
		sel.Window = rec.Window
	}
	if sel.ID == "" && sel.In == nil {
		sel.In = namedContainer(rec.Path)
	}
	return sel
}

// namedContainer is the closest ancestor on a recorded path that has a name
// and a container role, or nil. The path's last step is the element itself.
func namedContainer(path []desktop.PathStep) *desktop.Selector {
	for i := len(path) - 2; i >= 0; i-- {
		if step := path[i]; step.Name != "" && slices.Contains(containerRoles, step.Role) {
			return &desktop.Selector{Role: step.Role, Name: step.Name}
		}
	}
	return nil
}

// pinFields converts an extract's bindings into pinned fields: a scalar's
// element into its selector, a table into its container and the header of
// each column.
func pinFields(fields []fieldBinding) map[string]pinnedField {
	pinned := make(map[string]pinnedField, len(fields))
	for _, field := range fields {
		if field.Table == nil {
			pinned[field.Name] = pinnedField{Selector: pinnedSelector(field.Element)}
			continue
		}
		container := field.Table.Container
		container.App = field.Table.App
		if container.App == "" {
			container.Window = field.Table.Window
		}
		columns := make(map[string]string, len(field.Table.Columns))
		for _, column := range field.Table.Columns {
			columns[column.Property] = column.Header
		}
		pinned[field.Name] = pinnedField{Container: &container, Columns: columns}
	}
	return pinned
}
