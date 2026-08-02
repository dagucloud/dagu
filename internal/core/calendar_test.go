// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCalendarDayFilter(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"", "business-days", "last-business-day", "first-business-day"} {
		filter, err := ParseCalendarDayFilter(valid)
		require.NoError(t, err)
		assert.Equal(t, CalendarDayFilter(valid), filter)
	}

	_, err := ParseCalendarDayFilter("weekdays")
	assert.ErrorContains(t, err, "invalid calendar day filter")
}

func TestValidCalendarName(t *testing.T) {
	t.Parallel()

	assert.True(t, ValidCalendarName("jp-banking"))
	assert.True(t, ValidCalendarName("ops_2026.v1"))
	assert.False(t, ValidCalendarName(""))
	assert.False(t, ValidCalendarName(".hidden"))
	assert.False(t, ValidCalendarName("-lead"))
	assert.False(t, ValidCalendarName("../escape"))
	assert.False(t, ValidCalendarName("a/b"))
}

func TestDAGCloneDeepCopiesCalendar(t *testing.T) {
	t.Parallel()

	original := &DAG{Calendar: &CalendarConfig{Name: "jp-banking", Days: CalendarDayFilterBusinessDays}}
	cloned := original.Clone()
	require.NotNil(t, cloned.Calendar)

	cloned.Calendar.Name = "other"
	cloned.Calendar.Days = CalendarDayFilterNone
	assert.Equal(t, "jp-banking", original.Calendar.Name)
	assert.Equal(t, CalendarDayFilterBusinessDays, original.Calendar.Days)
}
