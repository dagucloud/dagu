// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
)

// A stop that comes after the wait ended and before the next cycle starts must
// not be lost: the cycle does not start, and the run is aborted.
func TestStartCycleDoesNotStartAfterAStop(t *testing.T) {
	t.Parallel()

	a := &Agent{stopped: make(chan struct{}), cycles: &cycleState{number: 1, waiting: true}}
	close(a.stopped)
	previous := &runtime.Plan{}
	a.plan = previous

	assert.False(t, a.startCycle(context.Background(), &runtime.Plan{}))
	assert.Same(t, previous, a.plan, "the plan of the run does not change")
	assert.Equal(t, 1, a.cycles.number)
	assert.True(t, a.cycles.aborted)
	assert.False(t, a.cycles.waiting)
}

func TestStartCycleStartsTheNextCycle(t *testing.T) {
	t.Parallel()

	a := &Agent{stopped: make(chan struct{}), cycles: &cycleState{number: 1, waiting: true}}
	next := &runtime.Plan{}

	assert.True(t, a.startCycle(context.Background(), next))
	assert.Same(t, next, a.plan)
	assert.Equal(t, 2, a.cycles.number)
	assert.False(t, a.cycles.waiting)
}

// A loop that ends between two cycles, for any reason, must not leave the run
// marked as waiting: that mark makes the stored final status "running".
func TestStopWaitingClearsTheWaitOfAnEndedLoop(t *testing.T) {
	t.Parallel()

	a := &Agent{cycles: &cycleState{number: 3, waiting: true}}
	a.stopWaiting()
	assert.False(t, a.cycles.waiting)

	// A run that does not loop has no cycle state.
	(&Agent{}).stopWaiting()
}
