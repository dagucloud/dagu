// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const jpBankingCalendar = `
description: Japanese bank business days
holidays:
  - 2027-01-01
  - date: 2027-01-11
    name: Coming of Age Day
  - from: 2027-12-31
    to: 2028-01-03
    name: Year-end closure
extra_business_days:
  - 2027-10-09
include: [jp-national]
through: 2099-12-31
`

const jpNationalCalendar = `
holidays:
  - date: 2027-02-11
    name: National Foundation Day
`

func writeCalendars(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o600))
	}
	return dir
}

func civil(year int, month time.Month, day int) ir.CivilDate {
	return ir.CivilDate{Year: year, Month: month, Day: day}
}

func TestLoadCalendar(t *testing.T) {
	t.Parallel()

	dir := writeCalendars(t, map[string]string{
		"jp-banking":  jpBankingCalendar,
		"jp-national": jpNationalCalendar,
	})

	cal, err := spec.LoadCalendar(dir, "jp-banking")
	require.NoError(t, err)

	assert.Equal(t, "Japanese bank business days", cal.Description)
	assert.Equal(t, civil(2099, 12, 31), cal.Through)
	assert.False(t, cal.IsBusinessDay(civil(2027, 1, 1)))
	holiday, ok := cal.Holiday(civil(2027, 1, 11))
	require.True(t, ok)
	assert.Equal(t, "Coming of Age Day", holiday.Name)
	for _, day := range []ir.CivilDate{civil(2027, 12, 31), civil(2028, 1, 1), civil(2028, 1, 3)} {
		holiday, ok := cal.Holiday(day)
		require.True(t, ok, day)
		assert.Equal(t, "Year-end closure", holiday.Name)
	}
	assert.True(t, cal.IsBusinessDay(civil(2028, 1, 4)))
	assert.True(t, cal.IsExtraBusinessDay(civil(2027, 10, 9)))
	holiday, ok = cal.Holiday(civil(2027, 2, 11))
	require.True(t, ok, "included holiday")
	assert.Equal(t, "National Foundation Day", holiday.Name)

	builtin, err := spec.LoadCalendar("", ir.CalendarWeekdays)
	require.NoError(t, err)
	assert.Equal(t, ir.CalendarWeekdays, builtin.Name)
}

// A change to an included calendar must count as a change to the calendar
// that includes it, so DAGs using it are planned again.
func TestLoadCalendarDigestCoversIncludes(t *testing.T) {
	t.Parallel()

	dir := writeCalendars(t, map[string]string{
		"jp-banking":  jpBankingCalendar,
		"jp-national": jpNationalCalendar,
	})
	before, err := spec.LoadCalendar(dir, "jp-banking")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "jp-national.yaml"),
		[]byte(jpNationalCalendar+"  - 2027-03-21\n"), 0o600))
	after, err := spec.LoadCalendar(dir, "jp-banking")
	require.NoError(t, err)

	assert.NotEqual(t, before.Digest, after.Digest)
}

// A source parses a calendar once and reuses it until a file it was built
// from changes, so many DAGs on one calendar cost one parse.
func TestCalendarSourceReusesCalendars(t *testing.T) {
	t.Parallel()

	dir := writeCalendars(t, map[string]string{
		"jp-banking":  jpBankingCalendar,
		"jp-national": jpNationalCalendar,
	})
	source := spec.NewCalendarSource(dir)
	write := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o600))
	}

	first, err := source.Calendar("jp-banking")
	require.NoError(t, err)
	again, err := source.Calendar("jp-banking")
	require.NoError(t, err)
	assert.Same(t, first, again)

	write("jp-banking", "holidays: [2027-05-03]\ninclude: [jp-national]\n")
	edited, err := source.Calendar("jp-banking")
	require.NoError(t, err)
	_, ok := edited.Holiday(civil(2027, 5, 3))
	assert.True(t, ok, "edited calendar")

	write("jp-national", jpNationalCalendar+"  - 2027-03-21\n")
	included, err := source.Calendar("jp-banking")
	require.NoError(t, err)
	_, ok = included.Holiday(civil(2027, 3, 21))
	assert.True(t, ok, "edited include")

	write("jp-banking", "holidays: nope\n")
	_, err = source.Calendar("jp-banking")
	require.ErrorContains(t, err, "field 'holidays': must be a list")
	_, err = source.Calendar("jp-banking")
	require.ErrorContains(t, err, "field 'holidays': must be a list")

	require.NoError(t, os.Remove(filepath.Join(dir, "jp-banking.yaml")))
	_, err = source.Calendar("jp-banking")
	require.ErrorIs(t, err, spec.ErrUnknownCalendar)

	write("jp-banking", jpNationalCalendar)
	restored, err := source.Calendar("jp-banking")
	require.NoError(t, err)
	_, ok = restored.Holiday(civil(2027, 2, 11))
	assert.True(t, ok, "recreated calendar")
}

// Fixing a file's permissions changes neither its size nor its modification
// time, so a read failure must not be reused.
func TestCalendarSourceRetriesUnreadableFiles(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not block reads here")
	}

	dir := writeCalendars(t, map[string]string{"jp-national": jpNationalCalendar})
	path := filepath.Join(dir, "jp-national.yaml")
	source := spec.NewCalendarSource(dir)

	require.NoError(t, os.Chmod(path, 0))
	_, err := source.Calendar("jp-national")
	require.ErrorIs(t, err, os.ErrPermission)

	require.NoError(t, os.Chmod(path, 0o600))
	cal, err := source.Calendar("jp-national")
	require.NoError(t, err)
	_, ok := cal.Holiday(civil(2027, 2, 11))
	assert.True(t, ok)
}

// Check reports every problem of an unsaved calendar where the editor can
// mark it, and still returns the calendar built from the valid parts.
func TestCalendarSourceCheck(t *testing.T) {
	t.Parallel()

	dir := writeCalendars(t, map[string]string{
		"ops":         "include: [jp-national]\n",
		"jp-national": jpNationalCalendar,
	})
	source := spec.NewCalendarSource(dir)
	check := func(name, content string) (*ir.Calendar, []string) {
		t.Helper()
		cal, errs := source.Check(name, []byte(content))
		return cal, spec.ErrorMessages([]byte(content), errs)
	}

	t.Run("every problem, in document order", func(t *testing.T) {
		t.Parallel()

		cal, messages := check("draft", `description: Ops
weekdays: [MON, FUN]
holidays:
  - 2027-13-01
  - date: 2027-01-11
    nam: typo
  - date: 2027-05-03
through: someday
`)
		assert.Equal(t, []string{
			`[2:17] field 'weekdays[1]': unknown day FUN; use MON, TUE, WED, THU, FRI, SAT, or SUN`,
			`[4:5] field 'holidays[0]': invalid date "2027-13-01": use YYYY-MM-DD`,
			`[6:10] field 'holidays[1].nam': unknown key "nam"; use date, or from and to, with an optional name`,
			`[8:10] field 'through': invalid date "someday": use YYYY-MM-DD`,
		}, messages)
		require.NotNil(t, cal)
		assert.Equal(t, "Ops", cal.Description)
		assert.True(t, cal.IsBusinessDay(civil(2027, 5, 10)), "Monday")
		assert.False(t, cal.IsBusinessDay(civil(2027, 5, 11)), "Tuesday is not listed")
		assert.False(t, cal.IsBusinessDay(civil(2027, 5, 3)), "valid holiday kept")
	})

	t.Run("syntax errors keep their position", func(t *testing.T) {
		t.Parallel()

		cal, messages := check("draft", "holidays: [2027-01-01\n")
		assert.Nil(t, cal)
		require.Len(t, messages, 1)
		assert.Regexp(t, `^\[\d+:\d+\] `, messages[0])
	})

	t.Run("unknown keys", func(t *testing.T) {
		t.Parallel()

		_, messages := check("draft", "holiday: [2027-01-01]\n'a.b': 1\n")
		require.Len(t, messages, 2)
		assert.True(t, strings.HasPrefix(messages[0], `[1:10] field 'holiday': unknown key "holiday"`), messages[0])
		assert.True(t, strings.HasPrefix(messages[1], `[2:8] `), messages[1])
	})

	t.Run("include problems are marked on the include", func(t *testing.T) {
		t.Parallel()

		_, messages := check("draft", "include: [jp-national, missing]\n")
		require.Len(t, messages, 1)
		assert.True(t, strings.HasPrefix(messages[0], `[1:24] field 'include[1]': unknown calendar "missing"`), messages[0])
	})

	// The saved ops calendar is fine, but the draft of jp-national would
	// make the two include each other.
	t.Run("a draft that closes an include cycle", func(t *testing.T) {
		t.Parallel()

		_, messages := check("jp-national", "include: [ops]\n")
		require.Len(t, messages, 1)
		assert.True(t, strings.HasPrefix(messages[0], `[1:11] field 'include[0]': calendar "ops": `), messages[0])
		assert.Contains(t, messages[0], `calendar "jp-national" includes itself`)
	})

	t.Run("includes and merge keys", func(t *testing.T) {
		t.Parallel()

		cal, messages := check("draft", `include: [jp-national]
holidays:
  - &newyear {date: 2027-01-01, name: New Year}
  - <<: *newyear
    date: 2028-01-01
`)
		assert.Empty(t, messages)
		assert.Equal(t, []string{"jp-national"}, cal.Includes)
		for _, day := range []ir.CivilDate{civil(2027, 1, 1), civil(2028, 1, 1)} {
			holiday, ok := cal.Holiday(day)
			assert.True(t, ok, day)
			assert.Equal(t, "New Year", holiday.Name)
		}
		_, ok := cal.Holiday(civil(2027, 2, 11))
		assert.True(t, ok, "included holiday")
	})

	t.Run("names", func(t *testing.T) {
		t.Parallel()

		_, errs := source.Check("weekdays", []byte("holidays: []\n"))
		require.Len(t, errs, 1)
		assert.ErrorContains(t, errs[0], `"weekdays" is reserved`)
		_, errs = source.Check("../ops", []byte("holidays: []\n"))
		require.Len(t, errs, 1)
		assert.ErrorContains(t, errs[0], "invalid calendar name")
	})

	t.Run("a document that is not a mapping", func(t *testing.T) {
		t.Parallel()

		cal, messages := check("draft", "- 2027-01-01\n")
		assert.Nil(t, cal)
		assert.Equal(t, []string{"a calendar file must be a mapping with keys such as weekdays and holidays"}, messages)
	})

	t.Run("the template is a valid calendar", func(t *testing.T) {
		t.Parallel()

		cal, errs := source.Check("draft", spec.CalendarTemplate(2027))
		require.Empty(t, errs)
		assert.Equal(t, civil(2027, 12, 31), cal.Through)
		assert.True(t, cal.IsBusinessDay(civil(2027, 12, 31)), "Friday")
		assert.False(t, cal.IsBusinessDay(civil(2028, 1, 1)), "Saturday")
	})
}

// Checking a draft leaves the saved calendar's cached copy in place.
func TestCalendarSourceCheckKeepsCache(t *testing.T) {
	t.Parallel()

	source := spec.NewCalendarSource(writeCalendars(t, map[string]string{"jp-national": jpNationalCalendar}))
	saved, err := source.Calendar("jp-national")
	require.NoError(t, err)

	_, errs := source.Check("jp-national", []byte("holidays: [2027-05-03]\n"))
	require.Empty(t, errs)

	again, err := source.Calendar("jp-national")
	require.NoError(t, err)
	assert.Same(t, saved, again)
}

func TestLoadCalendarErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "unknown key", content: "holiday: [2027-01-01]\n", want: `unknown key "holiday"`},
		{name: "malformed date", content: "holidays: [2027-13-01]\n", want: `field 'holidays[0]': invalid date "2027-13-01"`},
		{name: "inverted range", content: "holidays:\n  - {from: 2027-01-05, to: 2027-01-03}\n", want: "range ends on 2027-01-03, before it starts on 2027-01-05"},
		{name: "date and range", content: "holidays:\n  - {date: 2027-01-05, from: 2027-01-05, to: 2027-01-06}\n", want: "give either date, or from and to"},
		{name: "empty weekdays", content: "weekdays: []\n", want: "field 'weekdays': must be a non-empty list"},
		{name: "unknown weekday", content: "weekdays: [MON, FUN]\n", want: "unknown day FUN"},
		{name: "include cycle", content: "include: [cycle]\n", want: `calendar "cycle" includes itself`},
		{name: "unknown include", content: "include: [missing]\n", want: `unknown calendar "missing"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := writeCalendars(t, map[string]string{"cycle": tc.content})
			_, err := spec.LoadCalendar(dir, "cycle")
			require.ErrorContains(t, err, tc.want)
		})
	}

	t.Run("unknown calendar lists the available ones", func(t *testing.T) {
		t.Parallel()

		dir := writeCalendars(t, map[string]string{"jp-banking": jpNationalCalendar})
		_, err := spec.LoadCalendar(dir, "jp-bank")
		require.ErrorIs(t, err, spec.ErrUnknownCalendar)
		require.EqualError(t, err, `unknown calendar "jp-bank"; available: jp-banking, weekdays`)
	})
}

func TestListCalendars(t *testing.T) {
	t.Parallel()

	dir := writeCalendars(t, map[string]string{
		"jp-banking":  jpBankingCalendar,
		"jp-national": jpNationalCalendar,
		"broken":      "holidays: nope\n",
		"weekdays":    jpNationalCalendar,
	})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))

	summaries, err := spec.ListCalendars(dir)
	require.NoError(t, err)

	names := make([]string, len(summaries))
	for i, summary := range summaries {
		names[i] = summary.Name
	}
	assert.Equal(t, []string{"broken", "jp-banking", "jp-national", "weekdays", "weekdays"}, names)
	assert.ErrorContains(t, summaries[0].Err, "field 'holidays': must be a list")
	assert.Equal(t, "Japanese bank business days", summaries[1].Description)
	assert.Equal(t, civil(2099, 12, 31), summaries[1].Through)
	assert.Equal(t, []string{"jp-national"}, summaries[1].Includes)
	builtinSeen := false
	for _, summary := range summaries[3:] {
		if summary.Builtin {
			builtinSeen = true
			continue
		}
		assert.ErrorContains(t, summary.Err, `"weekdays" is reserved`)
	}
	assert.True(t, builtinSeen)

	missing, err := spec.ListCalendars(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	require.Len(t, missing, 1)
	assert.True(t, missing[0].Builtin)
}

func TestScheduleCalendars(t *testing.T) {
	t.Parallel()

	dir := writeCalendars(t, map[string]string{
		"jp-banking":  jpBankingCalendar,
		"jp-national": jpNationalCalendar,
		"expiring":    "through: " + time.Now().AddDate(0, 0, 10).Format(time.DateOnly) + "\n",
	})
	newYear := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("the DAG calendar applies to entries that name none", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(`
calendar: jp-banking
schedule:
  - "0 18 * * *"
  - expression: "0 3 * * *"
    calendar: none
  - expression: "0 23 * * *"
    calendar: weekdays
    business_days: [-1]
steps:
  - run: echo pay
`), spec.WithCalendarsDir(dir))
		require.NoError(t, err)

		require.Len(t, dag.Schedule, 3)
		assert.Equal(t, "jp-banking", dag.Calendar)
		assert.Equal(t, "jp-banking", dag.Schedule[0].Calendar)
		assert.Equal(t, time.Date(2027, 1, 4, 18, 0, 0, 0, time.UTC), dag.Schedule[0].Next(newYear))
		assert.Empty(t, dag.Schedule[1].Calendar)
		assert.Equal(t, time.Date(2027, 1, 1, 3, 0, 0, 0, time.UTC), dag.Schedule[1].Next(newYear))
		assert.Equal(t, time.Date(2027, 1, 29, 23, 0, 0, 0, time.UTC), dag.Schedule[2].Next(newYear))
	})

	t.Run("a base config sets the default calendar", func(t *testing.T) {
		t.Parallel()

		base := filepath.Join(t.TempDir(), "base.yaml")
		require.NoError(t, os.WriteFile(base, []byte("calendar: jp-banking\n"), 0o600))

		dag, err := spec.LoadYAML(t.Context(), []byte(`
schedule: "0 18 * * *"
steps:
  - run: echo pay
`), spec.WithCalendarsDir(dir), spec.WithBaseConfig(base))
		require.NoError(t, err)

		assert.Equal(t, "jp-banking", dag.Schedule[0].Calendar)
		assert.Equal(t, time.Date(2027, 1, 4, 18, 0, 0, 0, time.UTC), dag.Schedule[0].Next(newYear))
	})

	// The base config is built on its own before it is merged, so a
	// default applied then would erase an entry's opt-out and hide a DAG's
	// own default.
	t.Run("base schedules take the final DAG calendar", func(t *testing.T) {
		t.Parallel()

		base := filepath.Join(t.TempDir(), "base.yaml")
		require.NoError(t, os.WriteFile(base, []byte(`
calendar: jp-banking
schedule:
  - "0 18 * * *"
  - expression: "0 3 * * *"
    calendar: none
`), 0o600))

		dag, err := spec.LoadYAML(t.Context(), []byte(`
calendar: weekdays
steps:
  - run: echo pay
`), spec.WithCalendarsDir(dir), spec.WithBaseConfig(base))
		require.NoError(t, err)

		require.Len(t, dag.Schedule, 2)
		assert.Equal(t, ir.CalendarWeekdays, dag.Schedule[0].Calendar)
		assert.Equal(t, time.Date(2027, 1, 1, 18, 0, 0, 0, time.UTC), dag.Schedule[0].Next(newYear))
		assert.Empty(t, dag.Schedule[1].Calendar)
		saturday := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, time.Date(2027, 1, 2, 3, 0, 0, 0, time.UTC), dag.Schedule[1].Next(saturday))
	})

	t.Run("built-in calendars need no directory", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(`
schedule:
  - expression: "0 9 * * *"
    calendar: weekdays
steps:
  - run: echo pay
`))
		require.NoError(t, err)
		assert.Equal(t, time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC), dag.Schedule[0].Next(newYear))
	})

	// Remote workers load DAGs without calendar files; the schedule does
	// not matter there, and must not fire on a holiday anywhere else.
	t.Run("without a calendars directory a calendar file stays unbound", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(`
calendar: jp-banking
schedule: "0 18 * * *"
steps:
  - run: echo pay
`))
		require.NoError(t, err)
		assert.True(t, dag.Schedule[0].Next(newYear).IsZero())
	})

	t.Run("warns when the calendar's holidays run out", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(`
schedule:
  - expression: "0 18 * * *"
    calendar: expiring
  - expression: "0 9 * * *"
    calendar: expiring
steps:
  - run: echo pay
`), spec.WithCalendarsDir(dir))
		require.NoError(t, err)

		var warnings []string
		for _, warning := range dag.BuildWarnings {
			if strings.HasPrefix(warning, "calendar ") {
				warnings = append(warnings, warning)
			}
		}
		// Two schedules on one calendar warn once.
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], `calendar "expiring" lists holidays through`)
	})

	t.Run("warns when a schedule can never fire", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(`
schedule:
  - expression: "0 9 * * 6"
    calendar: weekdays
  - expression: "0 9 * * *"
    calendar: weekdays
steps:
  - run: echo pay
`), spec.WithCalendarsDir(dir))
		require.NoError(t, err)

		assert.Contains(t, dag.BuildWarnings,
			`schedule "0 9 * * 6" never fires: no time it matches falls on a business day of calendar "weekdays"`)
		assert.Len(t, dag.BuildWarnings, 1)
	})

	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unknown calendar",
			yaml: "schedule:\n  - expression: \"0 18 * * *\"\n    calendar: jp-bank\n",
			want: `unknown calendar "jp-bank"; available: expiring, jp-banking, jp-national, weekdays`,
		},
		{
			name: "business days without a calendar",
			yaml: "schedule:\n  - expression: \"0 18 * * *\"\n    business_days: [1]\n",
			want: "business_days needs a calendar",
		},
		{
			name: "business days with calendar none",
			yaml: "calendar: jp-banking\nschedule:\n  - expression: \"0 18 * * *\"\n    calendar: none\n    business_days: [1]\n",
			want: "business_days needs a calendar",
		},
		{
			name: "calendar on a stop schedule",
			yaml: "schedule:\n  stop:\n    expression: \"0 18 * * *\"\n    calendar: jp-banking\n",
			want: "only supported for start schedules",
		},
		{
			name: "invalid DAG calendar",
			yaml: "calendar: ../jp\nschedule: \"0 18 * * *\"\n",
			want: "invalid calendar name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(tc.yaml+"steps:\n  - run: echo pay\n"), spec.WithCalendarsDir(dir))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// Calendar errors name their field, so editors can mark the line: the
// entry's calendar or business_days, or the DAG's calendar when an entry
// took it.
func TestCalendarErrorPositions(t *testing.T) {
	t.Parallel()

	dir := writeCalendars(t, map[string]string{"jp-banking": jpNationalCalendar})
	data := []byte(`calendar: jp-bank
schedule:
  - "0 18 * * *"
  - expression: "0 9 * * *"
    calendar: opps
  - expression: "0 3 * * *"
    calendar: none
    business_days: [1]
steps:
  - run: echo pay
`)

	dag, err := spec.LoadYAML(t.Context(), data, spec.WithCalendarsDir(dir), spec.WithAllowBuildErrors())
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		`[1:11] field 'calendar': unknown calendar "jp-bank"; available: jp-banking, weekdays`,
		`[5:15] field 'schedule[1].calendar': unknown calendar "opps"; available: jp-banking, weekdays`,
		`[8:20] field 'schedule[2].business_days': business_days needs a calendar: set calendar on the entry or the DAG`,
	}, spec.ErrorMessages(data, dag.BuildErrors))
}
