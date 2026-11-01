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
