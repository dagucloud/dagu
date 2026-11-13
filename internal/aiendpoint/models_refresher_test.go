// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
)

// scheduler drives a Refresher with a fake clock. Wait never sleeps: it records
// the pause, moves the clock and ends the run after a number of pauses.
type scheduler struct {
	clock  *fakeClock
	cancel context.CancelFunc
	pauses []time.Duration
	// stopAfter is the number of pauses after which the run ends.
	stopAfter int
}

func (s *scheduler) wait(ctx context.Context, d time.Duration) error {
	s.pauses = append(s.pauses, d)
	if len(s.pauses) >= s.stopAfter {
		s.cancel()
		return ctx.Err()
	}
	s.clock.Advance(d)
	return nil
}

func (f *modelsFixture) refresher(period time.Duration, stopAfter int) (*aiendpoint.Refresher, *scheduler, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &scheduler{clock: f.clock, cancel: cancel, stopAfter: stopAfter}
	return &aiendpoint.Refresher{
		Service: f.svc,
		Period:  period,
		Now:     f.clock.Now,
		Wait:    s.wait,
		Jitter:  func(time.Duration) time.Duration { return 0 },
	}, s, ctx
}

func TestRefresherReadsOnceEveryPeriod(t *testing.T) {
	f := newModelsFixture(t)
	f.create(t, "cle1")
	r, s, ctx := f.refresher(24*time.Hour, 4)

	r.Run(ctx)

	// The first scan reads the endpoint, which was never read. The wait is capped
	// at one hour, so a period of 24 hours takes 24 pauses. The pauses of this
	// test end before the period does, so no second read happens.
	assert.Equal(t, int32(1), f.provider.calls.Load(), "one read at the first scan, none before the period ends")
	require.Len(t, s.pauses, 4)
	assert.Equal(t, time.Duration(0), s.pauses[0], "the start jitter is zero in the test")
	for _, pause := range s.pauses[1:] {
		assert.Equal(t, time.Hour, pause)
	}
}

func TestRefresherReadsAgainWhenThePeriodEnds(t *testing.T) {
	f := newModelsFixture(t)
	f.create(t, "cle1")
	r, s, ctx := f.refresher(2*time.Hour, 6)

	r.Run(ctx)

	// Reads at hour 0 and hour 2 and hour 4: the clock moves 1 hour for each pause.
	assert.Equal(t, int32(3), f.provider.calls.Load())
	require.GreaterOrEqual(t, len(s.pauses), 5)
}

func TestRefresherWithPeriodZeroDoesNothing(t *testing.T) {
	f := newModelsFixture(t)
	f.create(t, "cle1")
	r, s, ctx := f.refresher(0, 3)

	r.Run(ctx)

	assert.Zero(t, f.provider.calls.Load())
	assert.Empty(t, s.pauses)
}

func TestRefresherSkipsAListThatIsNotDue(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	_, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	calls := f.provider.calls.Load()

	r, _, _ := f.refresher(24*time.Hour, 1)
	f.clock.Advance(23 * time.Hour)
	next := r.Scan(ctx)

	assert.Equal(t, calls, f.provider.calls.Load(), "the list is read less than a period ago")
	assert.Equal(t, time.Hour, next, "the next read is due in one hour")

	f.clock.Advance(time.Hour)
	next = r.Scan(ctx)
	assert.Equal(t, calls+1, f.provider.calls.Load())
	assert.Equal(t, time.Hour, next, "the wait is capped at one hour, so a new endpoint is seen")
}

func TestRefresherCountsThePeriodFromAFailedReadToo(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	f.provider.set(http.StatusInternalServerError, "")
	r, _, _ := f.refresher(24*time.Hour, 1)

	r.Scan(ctx)
	r.Scan(ctx)

	assert.EqualValues(t, 1, f.provider.calls.Load(), "a failing provider is not called at each scan")
	ep, err := f.svc.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	got, err := f.svc.ModelsOf(ctx, ep)
	require.NoError(t, err)
	assert.Contains(t, got.LastError, "500")
}

func TestRefresherIgnoresEndpointsWithoutAModelsURL(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	_, err := f.svc.Create(ctx, aiendpoint.CreateInput{
		Name: "zai1", Preset: "zai", Protocol: aiendpoint.ProtocolOpenAI, Scope: aiendpoint.GlobalScope(),
		Allow: aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"*"}}, Key: modelsKey, Actor: "admin",
	})
	require.NoError(t, err)
	r, _, _ := f.refresher(time.Hour, 1)

	next := r.Scan(ctx)

	assert.Zero(t, f.provider.calls.Load())
	assert.Equal(t, time.Hour, next, "an idle scan waits the longest time")
}

func TestRefresherStopsWithItsContext(t *testing.T) {
	f := newModelsFixture(t)
	f.create(t, "cle1")
	r, _, ctx := f.refresher(time.Hour, 100)
	r.Wait = nil // the real wait ends with the context
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresher did not stop with its context")
	}
	assert.Zero(t, f.provider.calls.Load())
}

func TestRefresherAddsJitterToTheWait(t *testing.T) {
	f := newModelsFixture(t)
	f.create(t, "cle1")
	r, s, ctx := f.refresher(10*time.Hour, 3)
	r.Jitter = func(max time.Duration) time.Duration { return max / 2 }

	r.Run(ctx)

	// The start jitter is half of 30 s. The wait is capped at one hour. The jitter
	// is half of its largest value, which is 5 minutes.
	require.Len(t, s.pauses, 3)
	assert.Equal(t, 15*time.Second, s.pauses[0])
	assert.Equal(t, time.Hour+150*time.Second, s.pauses[1])
}
