// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package computeruse lets a model operate a desktop through screenshots and
// pointer and keyboard actions.
//
// A Session covers one task. The caller sends the current screen, the model
// answers with actions, the caller performs them and reports the results
// with the next screen, until the model reports the task done:
//
//	caller                              session (model)
//	  |-- Observation{Screen} ----------->|
//	  |<--------- Turn{Actions} ----------|
//	  |   perform actions in order        |
//	  |-- Observation{Results, Screen} -->|
//	  |<--------- Turn{Done} -------------|
//
// Coordinates always refer to the pixel space of the screenshots the
// session was sent.
package computeruse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
)

// Kind names a desktop action.
type Kind string

const (
	// KindClick clicks Button Count times at Point, or at the cursor when
	// Point is nil, holding Modifiers.
	KindClick Kind = "click"
	// KindMove moves the pointer to Point.
	KindMove Kind = "move"
	// KindDrag presses the left button at the first point of Path, moves
	// through the rest and releases at the last, holding Modifiers.
	KindDrag Kind = "drag"
	// KindMouseDown presses Button at the cursor.
	KindMouseDown Kind = "mouse_down"
	// KindMouseUp releases Button at the cursor.
	KindMouseUp Kind = "mouse_up"
	// KindScroll scrolls ScrollX and ScrollY wheel notches at Point, or at
	// the cursor when Point is nil. Positive values scroll right and down.
	KindScroll Kind = "scroll"
	// KindType types Text at the keyboard focus.
	KindType Kind = "type"
	// KindKey presses Keys together Repeat times.
	KindKey Kind = "key"
	// KindHoldKey holds Keys down for Duration.
	KindHoldKey Kind = "hold_key"
	// KindWait pauses for Duration.
	KindWait Kind = "wait"
	// KindScreenshot reports the current screen.
	KindScreenshot Kind = "screenshot"
	// KindZoom reports Region of the screen at a higher resolution.
	KindZoom Kind = "zoom"
	// KindCursorPosition reports the pointer position.
	KindCursorPosition Kind = "cursor_position"
)

// Pointer buttons.
const (
	ButtonLeft   = "left"
	ButtonRight  = "right"
	ButtonMiddle = "middle"
)

// Point is a screenshot pixel position.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Rect is a screenshot region from its top-left to its bottom-right corner.
type Rect struct {
	Min Point `json:"min"`
	Max Point `json:"max"`
}

// Action is one thing the model asks the caller to do. Key names are the
// model's own spelling, such as "Return", "ctrl" or "PageDown".
type Action struct {
	// CallID identifies the model call the action belongs to; the result
	// of the action carries the same ID.
	CallID    string        `json:"call_id,omitempty"`
	Kind      Kind          `json:"kind"`
	Point     *Point        `json:"point,omitempty"`
	Path      []Point       `json:"path,omitempty"`
	Button    string        `json:"button,omitempty"`
	Count     int           `json:"count,omitempty"`
	Modifiers []string      `json:"modifiers,omitempty"`
	Text      string        `json:"text,omitempty"`
	Keys      []string      `json:"keys,omitempty"`
	Repeat    int           `json:"repeat,omitempty"`
	ScrollX   int           `json:"scroll_x,omitempty"`
	ScrollY   int           `json:"scroll_y,omitempty"`
	Duration  time.Duration `json:"duration,omitempty"`
	Region    *Rect         `json:"region,omitempty"`
}

// Turn is the model's answer to an observation.
type Turn struct {
	// Actions are performed in order, stopping at the first failure.
	Actions []Action
	// Done is set when the model finished the task; any Actions of the
	// same turn are performed first.
	Done *Done
	// Confirmation, when set, is the model provider's explanation of why
	// Actions need a person's approval before they run.
	Confirmation string
	// Text is what the model wrote alongside its actions.
	Text string
	// Usage counts the tokens of this turn.
	Usage llm.Usage
}

// Done is the model's report that the task finished.
type Done struct {
	Success bool   `json:"success"`
	Summary string `json:"summary"`
}

// Screen is a screenshot and its pixel size.
type Screen struct {
	Image  llm.Image
	Width  int
	Height int
}

// Observation reports the desktop to the model.
type Observation struct {
	// Screen is the current screen.
	Screen Screen
	// Results answer the previous turn's actions, one per action, in order.
	Results []Result
	// Acknowledged approves the previous turn's Confirmation.
	Acknowledged bool
	// Note is an instruction for the model, such as a reminder to report
	// the task done.
	Note string
}

// Result reports how one action went.
type Result struct {
	CallID string
	// Output is text the action produced, such as a cursor position.
	Output string
	// Image is the picture a screenshot or zoom action produced.
	Image *llm.Image
	// Error describes why the action failed.
	Error string
	// Skipped marks an action not performed because an earlier one failed.
	Skipped bool
}

// SkippedText is how a skipped action is reported to a model.
const SkippedText = "Not executed: an earlier action in this turn failed."

// Text returns the result as the text a model is sent.
func (r Result) Text() string {
	switch {
	case r.Skipped:
		return SkippedText
	case r.Error != "":
		return "Error: " + r.Error
	case r.Output != "":
		return r.Output
	default:
		return "OK"
	}
}

// Failed reports whether the action did not complete.
func (r Result) Failed() bool {
	return r.Skipped || r.Error != ""
}

// ImageLimit is the largest screenshot a session accepts. Callers scale
// screenshots down to fit before sending them.
type ImageLimit struct {
	LongEdge  int
	MaxPixels int
}

// Session drives one task.
type Session interface {
	// ImageLimit reports the largest screenshot the session accepts.
	ImageLimit() ImageLimit
	// Next sends an observation and returns the model's next turn.
	Next(ctx context.Context, obs Observation) (*Turn, error)
}

// Options configure a session.
type Options struct {
	// Model is the model name.
	Model string
	// Task is what the model should accomplish.
	Task string
	// System adds instructions about the environment, such as the
	// operating system.
	System string
	// MaxTokens caps each model response.
	MaxTokens *int
	// Temperature applies to models that accept it.
	Temperature *float64
}

// Mode chooses how a session talks to the model.
type Mode string

const (
	// ModeAuto uses the provider's native computer-use tool when it has one.
	ModeAuto Mode = "auto"
	// ModeNative requires the provider's native computer-use tool.
	ModeNative Mode = "native"
	// ModeGeneric uses plain function tools, which works with any
	// tool-calling model that accepts images.
	ModeGeneric Mode = "generic"
)

// ErrModelNotSupported reports a model that the provider's native
// computer-use tool does not support. In ModeAuto, New then uses the
// generic session.
var ErrModelNotSupported = errors.New("the model does not support the provider's native computer use")

// NativeFactory starts a session with a provider's native computer-use
// tool. provider is the value llm.NewProvider returned for the type the
// factory was registered for.
type NativeFactory func(provider llm.Provider, opts Options) (Session, error)

var (
	nativeMu        sync.RWMutex
	nativeFactories = map[llm.ProviderType]NativeFactory{}
)

// RegisterNative registers the native session factory of a provider type.
func RegisterNative(providerType llm.ProviderType, factory NativeFactory) {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	nativeFactories[providerType] = factory
}

// HasNative reports whether a provider type has a native session factory.
func HasNative(providerType llm.ProviderType) bool {
	nativeMu.RLock()
	defer nativeMu.RUnlock()
	_, ok := nativeFactories[providerType]
	return ok
}

// New starts a session for a provider of the given type.
func New(providerType llm.ProviderType, provider llm.Provider, mode Mode, opts Options) (Session, error) {
	if mode != ModeGeneric {
		nativeMu.RLock()
		factory, ok := nativeFactories[providerType]
		nativeMu.RUnlock()
		if ok {
			session, err := factory(provider, opts)
			if !errors.Is(err, ErrModelNotSupported) || mode == ModeNative {
				return session, err
			}
		} else if mode == ModeNative {
			return nil, fmt.Errorf("provider %q has no native computer use; use mode %q", providerType, ModeGeneric)
		}
	}
	return newGenericSession(provider, opts), nil
}

// pixelsPerNotch approximates how far one mouse wheel notch scrolls.
const pixelsPerNotch = 100

// PixelsToNotches converts a scroll distance in pixels to wheel notches,
// keeping any nonzero distance at least one notch.
func PixelsToNotches(pixels int) int {
	notches := (abs(pixels) + pixelsPerNotch/2) / pixelsPerNotch
	if notches == 0 && pixels != 0 {
		notches = 1
	}
	if pixels < 0 {
		return -notches
	}
	return notches
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// SplitKeys splits a key combination such as "ctrl+shift+t" into key
// names. A trailing "+" is the plus key itself, as in "ctrl++".
func SplitKeys(combo string) []string {
	combo = strings.TrimSpace(combo)
	if combo == "" {
		return nil
	}
	if combo == "+" {
		return []string{"+"}
	}
	plus := strings.HasSuffix(combo, "++")
	if plus {
		combo = strings.TrimSuffix(combo, "++")
	}
	var keys []string
	for key := range strings.SplitSeq(combo, "+") {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	if plus {
		keys = append(keys, "+")
	}
	return keys
}
