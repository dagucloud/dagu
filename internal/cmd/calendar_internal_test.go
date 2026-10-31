// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCalendarCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "jp-banking.yaml"),
		[]byte("holidays: [2027-01-01]\nthrough: 2027-12-31\n"), 0o600))

	var out bytes.Buffer
	require.NoError(t, runCalendarCheck(&out, dir, nil))
	assert.Equal(t, "ok   jp-banking: 1 holidays, through 2027-12-31\n", out.String())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("holidays: [2027-02-30]\n"), 0o600))
	out.Reset()
	err := runCalendarCheck(&out, dir, nil)
	require.EqualError(t, err, "1 of 2 calendars are invalid")
	assert.Contains(t, out.String(), `FAIL broken: calendar "broken": holidays[0]: invalid date "2027-02-30"`)

	// A file given as a path is checked with the calendars beside it.
	out.Reset()
	require.NoError(t, runCalendarCheck(&out, "", []string{filepath.Join(dir, "jp-banking.yaml")}))
	assert.Contains(t, out.String(), "ok   jp-banking")
}

// A file named after a built-in calendar never loads, so the check must not
// report it as the built-in calendar.
func TestRunCalendarCheckRejectsReservedNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	reserved := filepath.Join(dir, "weekdays.yaml")
	require.NoError(t, os.WriteFile(reserved, []byte("holidays: nope\n"), 0o600))

	var out bytes.Buffer
	require.EqualError(t, runCalendarCheck(&out, dir, nil), "1 of 1 calendars are invalid")
	assert.Contains(t, out.String(), `FAIL weekdays: calendar file weekdays.yaml: "weekdays" is reserved`)

	out.Reset()
	require.Error(t, runCalendarCheck(&out, "", []string{reserved}))
	assert.Contains(t, out.String(), "FAIL weekdays")
}

func TestCalendarFireNote(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "3rd business day", calendarFireNote(ir.ScheduleFire{BusinessDay: 3}))
	assert.Equal(t, "last business day", calendarFireNote(ir.ScheduleFire{BusinessDay: -1}))
	assert.Equal(t, "2nd to last business day", calendarFireNote(ir.ScheduleFire{BusinessDay: -2}))
	assert.Equal(t, "11th business day", calendarFireNote(ir.ScheduleFire{BusinessDay: 11}))
	assert.Equal(t, "21st business day", calendarFireNote(ir.ScheduleFire{BusinessDay: 21}))
	assert.Equal(t, "extra business day", calendarFireNote(ir.ScheduleFire{ExtraBusinessDay: true}))
	assert.Equal(t, "skips Fri 2027-01-01; skips Mon 2027-01-11 (Coming of Age Day)", calendarFireNote(ir.ScheduleFire{
		Time: time.Date(2027, 1, 12, 18, 0, 0, 0, time.UTC),
		SkippedHolidays: []ir.Holiday{
			{Date: ir.CivilDate{Year: 2027, Month: time.January, Day: 1}},
			{Date: ir.CivilDate{Year: 2027, Month: time.January, Day: 11}, Name: "Coming of Age Day"},
		},
	}))
}
