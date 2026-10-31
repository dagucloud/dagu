// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Calendars that share includes form a diamond graph. A cache hit checks
// every file the calendar was read from, so each file must count once, not
// once per include path.
func TestCalendarSharedIncludesCountOnce(t *testing.T) {
	t.Parallel()

	const depth = 10
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+CalendarFileExtension), []byte(content), 0o600))
	}
	level := func(i int) string { return fmt.Sprintf("[l%da, l%db]", i, i) }
	write("top", "include: "+level(1)+"\n")
	for i := 1; i < depth; i++ {
		write(fmt.Sprintf("l%da", i), "include: "+level(i+1)+"\n")
		write(fmt.Sprintf("l%db", i), "include: "+level(i+1)+"\n")
	}
	write(fmt.Sprintf("l%da", depth), "include: [bottom]\n")
	write(fmt.Sprintf("l%db", depth), "include: [bottom]\n")
	write("bottom", "holidays: [2027-01-11]\n")

	source := NewCalendarSource(dir)
	cal, err := source.Calendar("top")
	require.NoError(t, err)
	_, ok := cal.Holiday(ir.CivilDate{Year: 2027, Month: time.January, Day: 11})
	assert.True(t, ok)
	assert.Len(t, source.entries["top"].files, 2*depth+2)
}

// A rewrite in place can keep a file's identity, size and modification
// time, which lookups compare on every call; the content is rechecked too,
// at most once per interval.
func TestCalendarSourceRechecksContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "jp"+CalendarFileExtension)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	write := func(content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		require.NoError(t, os.Chtimes(path, stamp, stamp))
	}
	holiday := ir.CivilDate{Year: 2027, Month: time.January, Day: 11}

	write("holidays: [2027-01-01]\n")
	source := NewCalendarSource(dir)
	source.contentRecheck = time.Hour
	_, err := source.Calendar("jp")
	require.NoError(t, err)

	write("holidays: [2027-01-11]\n")
	cached, err := source.Calendar("jp")
	require.NoError(t, err)
	_, ok := cached.Holiday(holiday)
	assert.False(t, ok, "rechecked before the interval passed")

	source.contentRecheck = 0
	cal, err := source.Calendar("jp")
	require.NoError(t, err)
	_, ok = cal.Holiday(holiday)
	assert.True(t, ok)
}

// The expiry warning starts CalendarExpiryWarningDays before the through
// date, that day included.
func TestCalendarExpiryWarningStartsOnItsFirstDay(t *testing.T) {
	t.Parallel()

	today := ir.DateOf(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC))
	assert.NotEmpty(t, CalendarExpiryWarning("jp", today.AddDays(CalendarExpiryWarningDays), today))
	assert.Empty(t, CalendarExpiryWarning("jp", today.AddDays(CalendarExpiryWarningDays+1), today))
}
