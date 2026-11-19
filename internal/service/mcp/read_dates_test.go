// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"testing"
	"time"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/stretchr/testify/assert"
)

// A read gives a run's dates by name: a date, or a list for an entry that
// steps through the window.
func TestRunDatesMap(t *testing.T) {
	t.Parallel()

	got := runDatesMap([]daguapi.RunDateValue{
		{Name: "target_day", Value: new("2026-11-09")},
		{Name: "window_days", List: new(true), Values: &[]string{"2026-11-06", "2026-11-07"}},
		{Name: "empty_window", List: new(true)},
	})
	assert.Equal(t, map[string]any{
		"target_day":   "2026-11-09",
		"window_days":  []string{"2026-11-06", "2026-11-07"},
		"empty_window": []string{},
	}, got)
}

// Only the upcoming runs of a DAG with dates carry them.
func TestUpcomingRunDates(t *testing.T) {
	t.Parallel()

	fire := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	got := upcomingRunDates([]daguapi.UpcomingRun{
		{Time: fire, Dates: &[]daguapi.RunDateValue{{Name: "target_day", Value: new("2026-11-13")}}},
		{Time: fire.Add(24 * time.Hour)},
	})
	assert.Equal(t, []map[string]any{
		{"time": "2026-11-14T02:00:00Z", "dates": map[string]any{"target_day": "2026-11-13"}},
	}, got)
	assert.Empty(t, upcomingRunDates([]daguapi.UpcomingRun{{Time: fire}}))
}
