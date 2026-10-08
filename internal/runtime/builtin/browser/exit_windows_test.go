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
	"time"

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
	t.Cleanup(tree.release)
	require.Equal(t, []int{min(browser.Process.Pid, helperPID), max(browser.Process.Pid, helperPID)}, tree.running())
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

	require.Nil(t, recordBrowserProcessTree(other.Process.Pid, startedAt-1))
	require.Nil(t, recordBrowserProcessTree(other.Process.Pid, 0), "an unknown start time tracks nothing")

	err := closeBrowser(context.Background(), other.Process.Pid, startedAt-1, func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.True(t, procutil.IsAlive(other.Process.Pid), "the process keeps running")
	assert.True(t, procutil.IsAlive(helperPID), "its child keeps running")
}

// While the runtime is still closing, a tree that has exited does not end
// the close on Windows: the runtime ends helpers the tree did not record.
func TestCloseBrowserWaitsForRuntimeOnWindows(t *testing.T) {
	t.Parallel()

	browser := startSleeper(t)
	startedAt, ok := procutil.StartTime(browser.Process.Pid)
	require.True(t, ok)
	runtimeBlocked := make(chan struct{})
	closed := make(chan error, 1)
	go func() {
		closed <- closeBrowser(context.Background(), browser.Process.Pid, startedAt, func(context.Context) error {
			<-runtimeBlocked
			return nil
		})
	}()

	require.NoError(t, browser.Process.Kill())
	_ = browser.Wait()
	select {
	case err := <-closed:
		t.Fatalf("returned before the runtime closed: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	close(runtimeBlocked)
	select {
	case err := <-closed:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after the runtime closed")
	}
}

// A browser still running after the runtime returned, as after a reattached
// close that only asks the browser to close, is left to finish on its own.
// Its helpers are ended once the browser has gone.
func TestCloseBrowserLeavesRunningBrowserToExit(t *testing.T) {
	t.Parallel()

	browser, helperPID := startSleeperTree(t)
	startedAt, ok := procutil.StartTime(browser.Process.Pid)
	require.True(t, ok)
	closed := make(chan error, 1)
	go func() {
		closed <- closeBrowser(context.Background(), browser.Process.Pid, startedAt, func(context.Context) error { return nil })
	}()

	time.Sleep(helperExitGrace + time.Second)
	select {
	case err := <-closed:
		t.Fatalf("returned while the browser was running: %v", err)
	default:
	}
	require.True(t, procutil.IsAlive(browser.Process.Pid), "the browser keeps running")
	require.True(t, procutil.IsAlive(helperPID), "the helper keeps running with it")

	require.NoError(t, browser.Process.Kill())
	_ = browser.Wait()
	select {
	case err := <-closed:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after the browser exited")
	}
	assert.False(t, procutil.IsAlive(helperPID), "the helper is ended")
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
