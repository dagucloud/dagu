// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package rundates_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/rundates"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Business days follow the calendar of the schedule that owns the slot. A
// slot taken from a parent run has no such schedule, so it follows the
// DAG's calendar.
func TestResolveCalendar(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plant.yaml"), []byte("holidays:\n  - 2026-11-06\n"), 0o600))
	calendars := spec.NewCalendarSource(dir)
	dag, err := spec.LoadYAML(t.Context(), []byte(`
calendar: weekdays
schedule:
  - expression: "0 2 * * *"
    calendar: plant
dates:
  last_open_day: -1bd
steps:
  - run: echo load
`), spec.WithCalendarsDir(dir), spec.OnlyMetadata())
	require.NoError(t, err)

	monday := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	request := rundates.Request{
		DAG:          dag,
		ScheduleTime: monday.Format(time.RFC3339),
		Window:       ir.ScheduleWindowAt(dag.Schedule, "", time.UTC, monday),
		CreatedAt:    monday,
		Location:     time.UTC,
		Calendars:    calendars,
	}

	own, err := rundates.Resolve(request)
	require.NoError(t, err)
	assert.Equal(t, []ir.DateValue{{Name: "last_open_day", Value: "2026-11-05"}}, own)

	request.Inherited = true
	inherited, err := rundates.Resolve(request)
	require.NoError(t, err)
	assert.Equal(t, []ir.DateValue{{Name: "last_open_day", Value: "2026-11-06"}}, inherited)
}

func TestResolveWithoutSlot(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
dates:
  today: { format: datetime }
  since: { from: interval_start }
steps:
  - run: echo load
`), spec.OnlyMetadata())
	require.NoError(t, err)

	_, err = rundates.Resolve(rundates.Request{DAG: dag, CreatedAt: time.Now(), Location: time.UTC})
	require.EqualError(t, err,
		"dates.since: interval_start needs the window of a schedule slot, and the run has no slot; the DAG has no schedule")

	dag.Dates = dag.Dates[:1]
	dates, err := rundates.Resolve(rundates.Request{
		DAG:       dag,
		CreatedAt: time.Date(2026, 11, 12, 10, 15, 42, 0, time.UTC),
		Location:  time.UTC,
	})
	require.NoError(t, err)
	assert.Equal(t, []ir.DateValue{{Name: "today", Value: "2026-11-12 10:15:00"}}, dates)
}

func TestResolveWithoutDates(t *testing.T) {
	t.Parallel()

	dates, err := rundates.Resolve(rundates.Request{DAG: &ir.DAG{Name: "plain"}})
	require.NoError(t, err)
	assert.Nil(t, dates)
}
