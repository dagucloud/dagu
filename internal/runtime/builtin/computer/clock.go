// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"sync"
	"time"
)

// activeContext bounds an operation by the time it spends working. The
// time a step spends waiting for a person to stop using the desktop is
// paused out, so a task is not failed for someone typing in another window
// while it runs; the wait itself has its own cap.
type activeContext struct {
	context.Context
	mu    sync.Mutex
	done  chan struct{}
	err   error
	timer *time.Timer
	// left is the working time the operation may still use.
	left time.Duration
	// since is when the current span of working time began, or zero while
	// the clock is paused.
	since time.Time
}

// withActiveTimeout returns a context that expires after timeout of working
// time, and a function that releases it.
func withActiveTimeout(parent context.Context, timeout time.Duration) (*activeContext, context.CancelFunc) {
	c := &activeContext{Context: parent, done: make(chan struct{}), left: timeout, since: time.Now()}
	c.timer = time.AfterFunc(timeout, func() { c.finish(context.DeadlineExceeded) })
	stop := context.AfterFunc(parent, func() { c.finish(parent.Err()) })
	return c, func() {
		stop()
		c.timer.Stop()
		c.finish(context.Canceled)
	}
}

func (c *activeContext) finish(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
		close(c.done)
	}
}

func (c *activeContext) Done() <-chan struct{} { return c.done }

func (c *activeContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Deadline is known only while the clock runs.
func (c *activeContext) Deadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.since.IsZero() {
		return time.Time{}, false
	}
	return c.since.Add(c.left), true
}

// pause stops the clock while the step waits for something that is not its
// own work.
func (c *activeContext) pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.since.IsZero() || c.err != nil {
		return
	}
	c.timer.Stop()
	c.left = max(c.left-time.Since(c.since), 0)
	c.since = time.Time{}
}

// resume restarts the clock with the working time that was left.
func (c *activeContext) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.since.IsZero() || c.err != nil {
		return
	}
	c.since = time.Now()
	c.timer.Reset(c.left)
}
