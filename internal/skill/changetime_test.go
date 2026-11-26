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
	first, err := skill.Read(dir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(file, []byte("two"), 0o644))
	require.NoError(t, os.Chtimes(file, old, old))
	next, err := skill.Read(dir)
	require.NoError(t, err)
	require.NotEqual(t, first.Hash, next.Hash)
	data, ok := next.Text("references/a.md")
	require.True(t, ok)
	require.Equal(t, "two", string(data))
}
