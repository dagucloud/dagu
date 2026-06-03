// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmdutil_test

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/stretchr/testify/require"
)

const parentExitWatcherHelperEnv = "DAGU_PARENT_EXIT_WATCHER_HELPER"

func TestParentExitWatcherIgnoresInheritedShellTimeout(t *testing.T) {
	t.Setenv("TMOUT", "1")

	cmd := exec.Command("/bin/sh", "-c", "sleep 2") //nolint:gosec
	cmd.Env = []string{"PATH=/usr/bin:/bin"}

	startedAt := time.Now()
	proc, err := cmdutil.StartManagedProcess(cmd)
	require.NoError(t, err)
	defer func() { _ = proc.Release() }()

	require.NoError(t, proc.Wait())
	require.GreaterOrEqual(t, time.Since(startedAt), 2*time.Second)
}

func TestParentExitWatcherTerminatesChildWhenParentExits(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)

	helper := exec.Command(executable, "-test.run=^TestParentExitWatcherHelper$") //nolint:gosec
	helper.Env = append(os.Environ(), parentExitWatcherHelperEnv+"=1")

	stdout, err := helper.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, helper.Start())

	scanner := bufio.NewScanner(stdout)
	require.True(t, scanner.Scan(), "helper did not print child pid")
	childPID, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	require.NoError(t, err)

	require.NoError(t, helper.Wait())
	require.Eventually(t, func() bool {
		return !processExists(childPID)
	}, 3*time.Second, 50*time.Millisecond)
}

func TestParentExitWatcherHelper(t *testing.T) {
	if os.Getenv(parentExitWatcherHelperEnv) != "1" {
		t.Skip("helper only")
	}

	cmd := exec.Command("/bin/sh", "-c", "sleep 30") //nolint:gosec
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	proc, err := cmdutil.StartManagedProcess(cmd)
	require.NoError(t, err)

	fmt.Println(proc.PID())
	os.Exit(0)
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}
