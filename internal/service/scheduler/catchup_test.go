// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseSchedule(t *testing.T, expr string) core.Schedule {
	t.Helper()
	parsed, err := cron.ParseStandard(expr)
	require.NoError(t, err)
	return core.Schedule{
		Expression: expr,
		Parsed:     parsed,
	}
}

func TestComputeReplayFrom(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		catchupWindow     time.Duration
		lastTick          time.Time
		lastScheduledTime time.Time
		want              time.Time
	}{
		{
			name:              "window is the constraint",
			catchupWindow:     6 * time.Hour,
			lastTick:          time.Time{},
			lastScheduledTime: time.Time{},
			want:              now.Add(-6 * time.Hour),
		},
		{
			name:              "lastTick is most recent",
			catchupWindow:     6 * time.Hour,
			lastTick:          now.Add(-2 * time.Hour),
			lastScheduledTime: time.Time{},
			want:              now.Add(-2 * time.Hour),
		},
		{
			name:              "lastScheduledTime is most recent",
			catchupWindow:     6 * time.Hour,
			lastTick:          now.Add(-5 * time.Hour),
			lastScheduledTime: now.Add(-1 * time.Hour),
			want:              now.Add(-1 * time.Hour),
		},
		{
			name:              "all zero except window",
			catchupWindow:     24 * time.Hour,
			lastTick:          time.Time{},
			lastScheduledTime: time.Time{},
			want:              now.Add(-24 * time.Hour),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeReplayFrom(tt.catchupWindow, tt.lastTick, tt.lastScheduledTime, now)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestComputeMissedIntervals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		schedules  []string
		replayFrom time.Time
		replayTo   time.Time
		wantCount  int
		wantFirst  time.Time
		wantLast   time.Time
	}{
		{
			name:       "hourly schedule 3h gap",
			schedules:  []string{"0 * * * *"},
			replayFrom: time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			wantCount:  3,
			wantFirst:  time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC),
			wantLast:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		},
		{
			name:       "daily schedule 3d gap",
			schedules:  []string{"0 9 * * *"},
			replayFrom: time.Date(2026, 2, 4, 9, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			wantCount:  3,
			wantFirst:  time.Date(2026, 2, 5, 9, 0, 0, 0, time.UTC),
			wantLast:   time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC),
		},
		{
			name:       "no missed intervals",
			schedules:  []string{"0 * * * *"},
			replayFrom: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			wantCount:  0,
		},
		{
			name:       "multiple schedules merged and deduped",
			schedules:  []string{"0 * * * *", "30 * * * *"},
			replayFrom: time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC),
			wantCount:  4,
			wantFirst:  time.Date(2026, 2, 7, 9, 30, 0, 0, time.UTC),
			wantLast:   time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC),
		},
		{
			name:       "replayFrom equals a schedule point is excluded",
			schedules:  []string{"0 * * * *"},
			replayFrom: time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			wantCount:  2,
			wantFirst:  time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC),
			wantLast:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var schedules []core.Schedule
			for _, expr := range tt.schedules {
				schedules = append(schedules, mustParseSchedule(t, expr))
			}

			got := ComputeMissedIntervals(schedules, tt.replayFrom, tt.replayTo)
			require.Len(t, got, tt.wantCount)

			if tt.wantCount > 0 {
				assert.Equal(t, tt.wantFirst, got[0])
				assert.Equal(t, tt.wantLast, got[len(got)-1])

				// Verify chronological order
				for i := 1; i < len(got); i++ {
					assert.True(t, got[i].After(got[i-1]),
						"expected %v after %v", got[i], got[i-1])
				}
			}
		})
	}
}

func TestComputeMissedIntervals_CappedAtMax(t *testing.T) {
	t.Parallel()

	// Per-minute schedule over 30 days = 43,200 intervals, should be capped at MaxMissedRuns
	schedules := []core.Schedule{mustParseSchedule(t, "* * * * *")}
	replayFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	replayTo := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	got := ComputeMissedIntervals(schedules, replayFrom, replayTo)
	assert.LessOrEqual(t, len(got), MaxMissedRuns)
	// Should keep the most recent runs (closest to replayTo)
	if len(got) > 0 {
		assert.True(t, got[len(got)-1].Before(replayTo) || got[len(got)-1].Equal(replayTo))
	}
}
