// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
)

const (
	// quietAfterInput is how long the screen is left alone after input or a
	// pattern before its elements are read, so a window the input closes or
	// opens is under way by the time they are.
	quietAfterInput = 400 * time.Millisecond
	// windowSettle is how long the windows on the screen stay as they are,
	// after one opened or closed, before their elements are read, looked at
	// every windowSettlePoll; windowSettleLimit bounds the wait.
	windowSettle      = 500 * time.Millisecond
	windowSettlePoll  = 50 * time.Millisecond
	windowSettleLimit = 3 * time.Second
)

// steadyElements reads the screen's elements only once the screen is
// steady: quietAfterInput after the last input or pattern, and windowSettle
// after the windows last changed. An application tears a closed window down,
// or builds a new one, for a moment around the change the window server
// shows; elements read or acted on then can be half gone, and Electron
// applications have crashed when read in that moment. A model, which looks
// at a settled screenshot first, never meets it; a replay, which moves on as
// soon as an action is done, would.
type steadyElements struct {
	desktop.Elements
	// windows lists the windows on the screen, inputAt says when the step
	// last sent input, and operation is the context of the operation
	// running now, whose end cuts a wait short.
	windows   func() []desktop.WindowID
	inputAt   func() time.Time
	operation func() context.Context
	// quiet, settleFor, and settleLimit are quietAfterInput, windowSettle,
	// and windowSettleLimit.
	quiet, settleFor, settleLimit time.Duration
	mu                            sync.Mutex
	seen                          []uint64
	// unsettled marks windows still changing when a wait reached its
	// limit, so the next read waits again.
	unsettled bool
	acted     time.Time
}

func newSteadyElements(els desktop.Elements, windows func() []desktop.WindowID, inputAt func() time.Time, operation func() context.Context) *steadyElements {
	return &steadyElements{
		Elements: els, windows: windows, inputAt: inputAt, operation: operation,
		quiet: quietAfterInput, settleFor: windowSettle, settleLimit: windowSettleLimit,
	}
}

// settle waits until the screen is steady, or the operation ends.
func (s *steadyElements) settle() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := s.operation()
	last := s.acted
	if at := s.inputAt(); at.After(last) {
		last = at
	}
	if wait := s.quiet - time.Since(last); wait > 0 {
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
	current := windowHandles(s.windows())
	if s.seen != nil && (s.unsettled || !slices.Equal(current, s.seen)) {
		deadline := time.Now().Add(s.settleLimit)
		stable := time.Now()
		for time.Since(stable) < s.settleFor && time.Now().Before(deadline) {
			if err := sleep(ctx, windowSettlePoll); err != nil {
				return err
			}
			if next := windowHandles(s.windows()); !slices.Equal(next, current) {
				current, stable = next, time.Now()
			}
		}
		s.unsettled = time.Since(stable) < s.settleFor
	}
	s.seen = current
	return nil
}

// act records input the step gave through the reader.
func (s *steadyElements) act() {
	s.mu.Lock()
	s.acted = time.Now()
	s.mu.Unlock()
}

func (s *steadyElements) At(x, y int) (desktop.Element, error) {
	if err := s.settle(); err != nil {
		return desktop.Element{}, err
	}
	return s.Elements.At(x, y)
}

func (s *steadyElements) Focused() (desktop.Element, error) {
	if err := s.settle(); err != nil {
		return desktop.Element{}, err
	}
	return s.Elements.Focused()
}

func (s *steadyElements) Find(sel desktop.Selector) ([]desktop.Element, error) {
	if err := s.settle(); err != nil {
		return nil, err
	}
	return s.Elements.Find(sel)
}

func (s *steadyElements) FrontWindow() (desktop.Element, error) {
	if err := s.settle(); err != nil {
		return desktop.Element{}, err
	}
	return s.Elements.FrontWindow()
}

func (s *steadyElements) Outline(window desktop.Element, limit int) ([]desktop.Element, error) {
	if err := s.settle(); err != nil {
		return nil, err
	}
	return s.Elements.Outline(window, limit)
}

// Focus gives an element the keyboard focus on a steady screen, and
// counts it as input the next read waits after.
func (s *steadyElements) Focus(target desktop.Element) error {
	if err := s.settle(); err != nil {
		return err
	}
	defer s.act()
	return s.Elements.Focus(target)
}

// Apply performs a pattern on a steady screen, and counts it as input the
// next read waits after.
func (s *steadyElements) Apply(target desktop.Element, pattern desktop.Pattern, value string) error {
	if err := s.settle(); err != nil {
		return err
	}
	defer s.act()
	return s.Elements.Apply(target, pattern, value)
}

func (s *steadyElements) Rows(ctx context.Context, container desktop.Selector, columns []string, maxRows int) ([][]string, error) {
	if err := s.settle(); err != nil {
		return nil, err
	}
	return s.Elements.Rows(ctx, container, columns, maxRows)
}

// windowHandles lists the handles of windows, sorted, to tell one set of
// windows from another.
func windowHandles(windows []desktop.WindowID) []uint64 {
	handles := make([]uint64, 0, len(windows))
	for _, w := range windows {
		handles = append(handles, w.Handle)
	}
	slices.Sort(handles)
	return handles
}
