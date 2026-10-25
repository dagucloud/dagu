// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// Systems a pin can be keyed by.
const (
	systemWindows = "windows"
	systemMacOS   = "macos"
)

// hostSystem is the system whose pinned steps and fields this device runs,
// or "" on a system that reads no elements.
func hostSystem() string {
	switch runtime.GOOS {
	case "windows":
		return systemWindows
	case "darwin":
		return systemMacOS
	default:
		return ""
	}
}

// pinnedStep is one authored step of a pinned act: exactly one action.
type pinnedStep struct {
	Click       *desktop.Selector `json:"click,omitempty"`
	DoubleClick *desktop.Selector `json:"double_click,omitempty"`
	RightClick  *desktop.Selector `json:"right_click,omitempty"`
	// Check and Uncheck leave a checkbox checked or unchecked, clicking it
	// only when it is not already, so running the step again changes
	// nothing.
	Check   *desktop.Selector `json:"check,omitempty"`
	Uncheck *desktop.Selector `json:"uncheck,omitempty"`
	Type    *pinnedType       `json:"type,omitempty"`
	Key     string            `json:"key,omitempty"`
	Scroll  *pinnedScroll     `json:"scroll,omitempty"`
	Wait    string            `json:"wait,omitempty"`
	// Focus is the element a key step focuses before it presses its keys,
	// so the keys reach the element they went to when the step was learned.
	Focus *desktop.Selector `json:"focus,omitempty"`
}

type pinnedType struct {
	Into desktop.Selector `json:"into"`
	Text string           `json:"text"`
}

type pinnedScroll struct {
	On desktop.Selector `json:"on"`
	Dy int              `json:"dy"`
}

// pinnedField is an authored extract binding: a selector for a scalar, or a
// container and its columns for a table.
type pinnedField struct {
	desktop.Selector
	Container *desktop.Selector `json:"container,omitempty"`
	Columns   map[string]string `json:"columns,omitempty"`
}

// MarshalJSON writes a table field as its container and columns alone, and
// a scalar field as its selector.
func (f pinnedField) MarshalJSON() ([]byte, error) {
	if f.Container != nil {
		return json.Marshal(struct {
			Container *desktop.Selector `json:"container"`
			Columns   map[string]string `json:"columns"`
		}{f.Container, f.Columns})
	}
	return json.Marshal(f.Selector)
}

// pinnedStep.UnmarshalJSON enforces exactly one action key, so a malformed
// pin is caught at load, not at replay.
func (s *pinnedStep) UnmarshalJSON(data []byte) error {
	type raw pinnedStep
	var r raw
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return err
	}
	*s = pinnedStep(r)
	if n := s.actionCount(); n != 1 {
		return fmt.Errorf("a pinned step sets %d actions; it must set exactly one of click, double_click, right_click, check, uncheck, type, key, scroll, or wait", n)
	}
	return nil
}

func (s pinnedStep) actionCount() int {
	n := 0
	for _, set := range []bool{s.Click != nil, s.DoubleClick != nil, s.RightClick != nil, s.Check != nil, s.Uncheck != nil, s.Type != nil, s.Key != "", s.Scroll != nil, s.Wait != ""} {
		if set {
			n++
		}
	}
	return n
}

// validate checks a pinned step's selectors and key chord.
func (s pinnedStep) validate() error {
	if n := s.actionCount(); n != 1 {
		return fmt.Errorf("a pinned step must set exactly one action, not %d", n)
	}
	if s.Focus != nil {
		if s.Key == "" {
			return errors.New("focus applies to a key step only")
		}
		if err := s.Focus.Validate(); err != nil {
			return fmt.Errorf("focus: %w", err)
		}
	}
	for _, sel := range []*desktop.Selector{s.Check, s.Uncheck} {
		if sel != nil && sel.Role != desktop.RoleCheckbox {
			return fmt.Errorf("check and uncheck take a checkbox, not a %s", sel.Role)
		}
	}
	for _, sel := range []*desktop.Selector{s.Click, s.DoubleClick, s.RightClick, s.Check, s.Uncheck} {
		if sel != nil {
			return sel.Validate()
		}
	}
	switch {
	case s.Type != nil:
		return s.Type.Into.Validate()
	case s.Scroll != nil:
		return s.Scroll.On.Validate()
	case s.Key != "":
		if _, err := desktop.ParseKeys(strings.Split(s.Key, "+")); err != nil {
			return fmt.Errorf("key %q: %w", s.Key, err)
		}
	case s.Wait != "":
		return agentstep.ValidateDuration("wait", s.Wait)
	}
	return nil
}

// references returns the variable placeholders a pinned step's texts use:
// the selector names and labels and a typed text. They are checked like the
// instruction's, so a typo or a reference to a skipped ask is caught.
func (s pinnedStep) references() []string {
	var texts []string
	for _, sel := range []*desktop.Selector{s.Click, s.DoubleClick, s.RightClick, s.Check, s.Uncheck, s.Focus} {
		if sel != nil {
			texts = append(texts, selectorTexts(*sel)...)
		}
	}
	if s.Type != nil {
		texts = append(texts, s.Type.Text)
		texts = append(texts, selectorTexts(s.Type.Into)...)
	}
	if s.Scroll != nil {
		texts = append(texts, selectorTexts(s.Scroll.On)...)
	}
	var names []string
	for _, text := range texts {
		names = append(names, agentstep.VariableReferences(text)...)
	}
	return names
}

// validate checks a pinned field's selector or container.
func (f pinnedField) validate() error {
	if f.Container != nil {
		return f.Container.Validate()
	}
	return f.Selector.Validate()
}

// validSystem reports whether a pin's system key is one the engine knows.
func validSystem(system string) bool {
	return system == systemWindows || system == systemMacOS
}

// validateRecorded checks an act's pinned steps: a known system, at least
// one step, and each step valid.
func validateRecorded(recorded map[string][]pinnedStep) error {
	for system, steps := range recorded {
		if !validSystem(system) {
			return fmt.Errorf("recorded has an unknown system %q; use windows or macos", system)
		}
		if len(steps) == 0 {
			return fmt.Errorf("recorded.%s has no steps", system)
		}
		for i, step := range steps {
			if err := step.validate(); err != nil {
				return fmt.Errorf("recorded.%s[%d]: %w", system, i, err)
			}
		}
	}
	return nil
}

// validateFields checks an extract's pinned fields: a known system, each
// field's selector valid, and each field readable as the schema types it, so
// a pin that cannot bind fails at load rather than silently at replay.
func validateFields(fields map[string]map[string]pinnedField, schema map[string]any) error {
	properties := schemaProperties(schema)
	for system, byName := range fields {
		if !validSystem(system) {
			return fmt.Errorf("fields has an unknown system %q; use windows or macos", system)
		}
		for name, field := range byName {
			if err := field.validate(); err != nil {
				return fmt.Errorf("fields.%s.%s: %w", system, name, err)
			}
			property, ok := properties[name]
			if !ok {
				return fmt.Errorf("fields.%s.%s is not a property of the extract schema", system, name)
			}
			if field.Container != nil {
				columns, ok := tableColumns(property)
				if !ok {
					return fmt.Errorf("fields.%s.%s pins a container, but %s is not an array of objects with scalar properties", system, name, name)
				}
				if len(field.Columns) == 0 {
					return fmt.Errorf("fields.%s.%s pins a container but names no columns", system, name)
				}
				for column := range field.Columns {
					if _, ok := columns[column]; !ok {
						return fmt.Errorf("fields.%s.%s column %q is not a property of %s's items", system, name, column, name)
					}
				}
				continue
			}
			if _, ok := bindableKind(property); !ok {
				return fmt.Errorf("fields.%s.%s cannot read %s from an element; its type is not a string, number, integer, or boolean", system, name, name)
			}
		}
	}
	return nil
}

// recordedElement describes the element a pinned selector names, for the
// same replay path a learned recording uses. The pointer lands at the
// element's centre, since a pin carries no fraction.
func pinnedElement(sel desktop.Selector) recordedElement {
	inner := sel
	inner.App, inner.Window = "", ""
	return recordedElement{Selector: inner, App: sel.App, Window: sel.Window, FractionX: 0.5, FractionY: 0.5}
}

// recordedAction builds the action and element a pinned step replays.
func (s pinnedStep) recordedAction() (recordedAction, bool) {
	// A pointer step carries a zero Point so the replay rewrites it to the
	// element's own position, the same as a recorded pointer action.
	switch {
	case s.Click != nil:
		return recordedAction{Action: computeruse.Action{Kind: computeruse.KindClick, Point: &computeruse.Point{}}, Element: new(pinnedElement(*s.Click))}, true
	case s.DoubleClick != nil:
		return recordedAction{Action: computeruse.Action{Kind: computeruse.KindClick, Count: 2, Point: &computeruse.Point{}}, Element: new(pinnedElement(*s.DoubleClick))}, true
	case s.RightClick != nil:
		return recordedAction{Action: computeruse.Action{Kind: computeruse.KindClick, Button: "right", Point: &computeruse.Point{}}, Element: new(pinnedElement(*s.RightClick))}, true
	case s.Check != nil:
		return recordedAction{Action: computeruse.Action{Kind: computeruse.KindClick, Point: &computeruse.Point{}}, Element: new(pinnedElement(*s.Check)), Checked: new(true)}, true
	case s.Uncheck != nil:
		return recordedAction{Action: computeruse.Action{Kind: computeruse.KindClick, Point: &computeruse.Point{}}, Element: new(pinnedElement(*s.Uncheck)), Checked: new(false)}, true
	case s.Type != nil:
		return recordedAction{Action: computeruse.Action{Kind: computeruse.KindType, Text: s.Type.Text}, Element: new(pinnedElement(s.Type.Into))}, true
	case s.Scroll != nil:
		return recordedAction{Action: computeruse.Action{Kind: computeruse.KindScroll, ScrollY: s.Scroll.Dy, Point: &computeruse.Point{}}, Element: new(pinnedElement(s.Scroll.On))}, true
	case s.Key != "":
		rec := recordedAction{Action: computeruse.Action{Kind: computeruse.KindKey, Keys: strings.Split(s.Key, "+")}}
		if s.Focus != nil {
			rec.Element = new(pinnedElement(*s.Focus))
		}
		return rec, true
	}
	return recordedAction{}, false
}

//go:fix inline
func ptr[T any](v T) *T { return new(v) }

// pinnedOutcome is how far an act's pinned steps got.
type pinnedOutcome struct {
	done bool
	// performed counts the steps that acted, waits aside.
	performed int
	// ran holds the steps that ran as one recorded turn, so the recording of
	// an act the model finishes covers the whole task. It is empty when none
	// of them acted on an element, since such a turn could replay only by
	// pixels it never recorded.
	ran    []recordedTurn
	reason string
}

// replayPinned runs an act's pinned steps in order through the same element
// replay a learned recording uses. It reports whether every step ran, which
// ran, and the reason one missed.
func (r *run) replayPinned(ctx context.Context, index int, steps []pinnedStep, direct bool, findWithin time.Duration) (pinnedOutcome, error) {
	var result pinnedOutcome
	els, err := r.elementsIfAvailable()
	if err != nil {
		// A pin replays only by element; a host that reads none, such as a
		// 32-bit Windows, misses rather than failing, so on_miss hands the
		// act to the model and never reports the miss.
		result.reason = fmt.Sprintf("pinned steps need the window's elements, which this host cannot read: %v", err)
		return result, nil
	}
	total := len(steps)
	var outcome replayOutcome
	var ran recordedTurn
	byElement := false
	keepRan := func() {
		if byElement {
			result.ran = []recordedTurn{ran}
		}
	}
	for i, step := range steps {
		if step.Wait != "" {
			wait, _ := time.ParseDuration(step.Wait)
			if err := sleep(ctx, wait); err != nil {
				return result, err
			}
			ran.Actions = append(ran.Actions, recordedAction{Action: computeruse.Action{Kind: computeruse.KindWait, Duration: wait}})
			continue
		}
		rec, ok := step.recordedAction()
		if !ok {
			return result, fmt.Errorf("a pinned step sets no action")
		}
		turn := recordedTurn{Actions: []recordedAction{rec}}
		reason, err := r.replayByElement(ctx, index, i, total, turn, els, findWithin, direct, &outcome)
		if err != nil {
			return result, err
		}
		if reason != "" {
			result.reason = reason
			keepRan()
			return result, nil
		}
		result.performed++
		ran.Actions = append(ran.Actions, rec)
		byElement = byElement || rec.Element != nil
	}
	result.done = true
	return result, nil
}

// pinnedBindings turns an extract's pinned fields for a system into field
// bindings, taking each field's type from the schema.
func pinnedBindings(spec extractSpec, system string) []fieldBinding {
	fields := spec.Fields[system]
	if len(fields) == 0 {
		return nil
	}
	properties := schemaProperties(spec.Schema)
	var bindings []fieldBinding
	for name, field := range fields {
		if field.Container != nil {
			columns, ok := tableColumns(properties[name])
			if !ok {
				continue
			}
			bindings = append(bindings, fieldBinding{Name: name, Kind: "table", Table: pinnedTable(field, columns)})
			continue
		}
		kind, ok := bindableKind(properties[name])
		if !ok {
			continue
		}
		bindings = append(bindings, fieldBinding{Name: name, Kind: string(kind), Element: pinnedElement(field.Selector)})
	}
	return bindings
}

// pinnedTable builds a table binding from a pinned field's container and
// columns, dropping the app and window into the binding as a recorded one
// carries them.
func pinnedTable(field pinnedField, columns map[string]workbook.ColumnType) *tableBinding {
	container := *field.Container
	app, window := container.App, container.Window
	container.App, container.Window = "", ""
	tbl := &tableBinding{Container: container, App: app, Window: window}
	for property, header := range field.Columns {
		kind, ok := columns[property]
		if !ok {
			continue
		}
		tbl.Columns = append(tbl.Columns, columnBinding{Property: property, Header: header, Kind: string(kind)})
	}
	return tbl
}

// mergeFields overlays pinned bindings on a device's, so a field a step
// pins is read from the pin and any other from the device.
func mergeFields(device, pinned []fieldBinding) []fieldBinding {
	if len(pinned) == 0 {
		return device
	}
	byName := map[string]fieldBinding{}
	order := []string{}
	for _, f := range device {
		if _, seen := byName[f.Name]; !seen {
			order = append(order, f.Name)
		}
		byName[f.Name] = f
	}
	for _, f := range pinned {
		if _, seen := byName[f.Name]; !seen {
			order = append(order, f.Name)
		}
		byName[f.Name] = f
	}
	merged := make([]fieldBinding, 0, len(order))
	for _, name := range order {
		merged = append(merged, byName[name])
	}
	return merged
}
