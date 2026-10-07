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
	startedAt, ok := procutil.StartTime(browser.Process.Pid)
	require.True(t, ok)
	tree := recordBrowserProcessTree(browser.Process.Pid, startedAt)
	require.Contains(t, tree.startedAt, browser.Process.Pid)
	require.Contains(t, tree.startedAt, helperPID)
	require.False(t, tree.exited())

	err := closeBrowser(context.Background(), browser.Process.Pid, startedAt, func(context.Context) error {
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

// A browser that was ended before closing, as by a crash, leaves its helpers
// running under its old process ID. Closing still ends them.
func TestCloseBrowserEndsHelpersOfEndedBrowser(t *testing.T) {
	t.Parallel()

	browser, helperPID := startSleeperTree(t)
	startedAt, ok := procutil.StartTime(browser.Process.Pid)
	require.True(t, ok)
	require.NoError(t, browser.Process.Kill())
	_ = browser.Wait()

	err := closeBrowser(context.Background(), browser.Process.Pid, startedAt, func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.False(t, procutil.IsAlive(helperPID), "the helper is ended")
}

// A process ID that no longer belongs to the browser identifies nothing, so
// closing leaves the process that holds it, and its children, alone.
func TestCloseBrowserLeavesReusedProcessIDAlone(t *testing.T) {
	t.Parallel()

	other, helperPID := startSleeperTree(t)
	startedAt, ok := procutil.StartTime(other.Process.Pid)
	require.True(t, ok)

	err := closeBrowser(context.Background(), other.Process.Pid, startedAt-1, func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.True(t, procutil.IsAlive(other.Process.Pid), "the process keeps running")
	assert.True(t, procutil.IsAlive(helperPID), "its child keeps running")
}

// profileHolders reports the processes whose command line names dir, and
// every Chrome process, since Chrome helpers do not all name the profile.
func profileHolders(dir string) string {
	escaped := strings.ReplaceAll(dir, "'", "''")
	script := fmt.Sprintf(`$all = Get-CimInstance Win32_Process
"processes naming the profile:"
$all | Where-Object { $_.CommandLine -like '*%s*' } | ForEach-Object { "  $($_.ProcessId) $($_.ParentProcessId) $($_.Name)" }
"chrome processes:"
$all | Where-Object { $_.Name -eq 'chrome.exe' } | ForEach-Object { "  $($_.ProcessId) $($_.ParentProcessId) $($_.CreationDate) $($_.CommandLine.Substring(0, [Math]::Min(160, $_.CommandLine.Length)))" }`, escaped)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("list processes: %v\n%s", err, out)
	}
	return string(out)
}
