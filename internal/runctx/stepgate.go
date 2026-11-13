// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runctx

import (
	"context"
	"sync"
)

// StepGate lets an isolated untrusted step hold its run alone (Spec 092,
// "Isolation"). A trusted step is a reader: trusted steps run at the same time.
// An isolated untrusted step is the writer: it waits for the end of the
// running steps of its run, then no other step of the run starts until it
// ends. A waiting writer stops new readers, so that a stream of trusted steps
// cannot delay it for ever. A nil gate lets each step pass.
type StepGate struct {
	mu      sync.Mutex
	readers int
	writer  bool
	waiting int
	changed chan struct{}
}

// NewStepGate returns an open gate.
func NewStepGate() *StepGate {
	return &StepGate{changed: make(chan struct{})}
}

// notify wakes each waiter. The caller holds mu.
func (g *StepGate) notify() {
	close(g.changed)
	g.changed = make(chan struct{})
}

// Read waits until no untrusted step holds or waits for the run, and takes a
// place for a trusted step. onWait runs once when the step must wait. The
// returned function gives the place back.
func (g *StepGate) Read(ctx context.Context, onWait func()) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	waited := false
	for {
		g.mu.Lock()
		if !g.writer && g.waiting == 0 {
			g.readers++
			g.mu.Unlock()
			return sync.OnceFunc(g.releaseRead), nil
		}
		ch := g.changed
		g.mu.Unlock()
		if !waited && onWait != nil {
			onWait()
		}
		waited = true
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Write waits until no step of the run runs, and holds the run. onWait runs
// once when the step must wait. The returned function gives the run back.
func (g *StepGate) Write(ctx context.Context, onWait func()) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	g.mu.Lock()
	g.waiting++
	g.mu.Unlock()
	waited := false
	for {
		g.mu.Lock()
		if !g.writer && g.readers == 0 {
			g.writer = true
			g.waiting--
			g.mu.Unlock()
			return sync.OnceFunc(g.releaseWrite), nil
		}
		ch := g.changed
		g.mu.Unlock()
		if !waited && onWait != nil {
			onWait()
		}
		waited = true
		select {
		case <-ch:
		case <-ctx.Done():
			g.mu.Lock()
			g.waiting--
			g.notify()
			g.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

func (g *StepGate) releaseRead() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.readers--
	g.notify()
}

func (g *StepGate) releaseWrite() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writer = false
	g.notify()
}
