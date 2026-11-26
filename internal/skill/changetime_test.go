// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux || darwin

package skill_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/stretchr/testify/require"
)

// An edit that keeps the size and sets the time stamp back, as tar or cp -p
// does, shows in the next read of a cached folder. The test changes a package
// variable, so it does not run in parallel.
func TestReadSeesAnEditThatKeepsTheTimeStamp(t *testing.T) {
	skill.SettleClock(t)
	dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good, "references/a.md": "one"})
	file := filepath.Join(dir, "references", "a.md")
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(file, old, old))
	info, err := os.Lstat(file)
	require.NoError(t, err)
	cached := skill.ChangeTime(info)
	first, err := skill.Read(dir)
	require.NoError(t, err)

	// A coarse file system clock (Linux before 6.13) can give the edit the
	// cached ctime, so the edit repeats until the ctime moves.
	require.Eventually(t, func() bool {
		if os.WriteFile(file, []byte("two"), 0o644) != nil || os.Chtimes(file, old, old) != nil {
			return false
		}
		info, err := os.Lstat(file)
		return err == nil && !skill.ChangeTime(info).Equal(cached)
	}, 5*time.Second, time.Millisecond)
	next, err := skill.Read(dir)
	require.NoError(t, err)
	require.NotEqual(t, first.Hash, next.Hash)
	data, ok := next.Text("references/a.md")
	require.True(t, ok)
	require.Equal(t, "two", string(data))
}
