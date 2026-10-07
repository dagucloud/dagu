// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package browser

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

// A helper started under the browser is part of its recorded process tree.
// When the runtime ends the browser but leaves the helper behind, closing
// ends the helper before it returns, so the profile can be removed.
func TestCloseBrowserEndsHelpers(t *testing.T) {
	t.Parallel()

	browser, helperPID := startSleeperTree(t)
	tree := recordBrowserProcessTree(browser.Process.Pid)
	require.Contains(t, tree.startedAt, browser.Process.Pid)
	require.Contains(t, tree.startedAt, helperPID)
	require.False(t, tree.exited())

	err := closeBrowser(context.Background(), browser.Process.Pid, func(context.Context) error {
		if err := browser.Process.Kill(); err != nil {
			return err
		}
		_ = browser.Wait()
		return nil
	})
	require.NoError(t, err)
	assert.False(t, procutil.IsAlive(helperPID), "the helper is ended")
	assert.True(t, tree.exited())
}

// profileHolders reports the processes whose command line names dir.
func profileHolders(dir string) string {
	script := fmt.Sprintf(`Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*%s*' } | ForEach-Object { "$($_.ProcessId) $($_.ParentProcessId) $($_.Name) $($_.CommandLine)" }`,
		strings.ReplaceAll(dir, "'", "''"))
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("list processes using the profile: %v\n%s", err, out)
	}
	return "processes using the profile:\n" + string(out)
}
