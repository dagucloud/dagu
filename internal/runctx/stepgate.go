// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runctx

import (
	"context"
	"sync"
)

// StepGate lets an isolated untrusted step hold its run alone (Spec 103,
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
// returned function gives the place back. Read is not reentrant: a caller
// that holds a place and calls Read again waits for ever once a writer waits.
func (g *StepGate) Read(ctx context.Context, onWait func()) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	waited := false
	for {
		// A wake-up and the end of ctx can come together; the end wins.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		// A wake-up and the end of ctx can come together; the end wins.
		if err := ctx.Err(); err != nil {
			g.waiting--
			g.notify()
			g.mu.Unlock()
			return nil, err
		}
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
