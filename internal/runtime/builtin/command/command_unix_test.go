// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package command

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

func TestCommandExecutor_CleansProcessGroupWhenParentDies(t *testing.T) {
	if os.Getenv("DAGU_COMMAND_PARENT_DEATH_HELPER") == "1" {
		runCommandParentDeathHelper(t)
		return
	}

	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "script.pid")
	readyFile := filepath.Join(tmpDir, "helper.ready")

	cmd := exec.Command(os.Args[0], "-test.run=^TestCommandExecutor_CleansProcessGroupWhenParentDies$")
	cmd.Env = append(os.Environ(),
		"DAGU_COMMAND_PARENT_DEATH_HELPER=1",
		"DAGU_COMMAND_PARENT_DEATH_DIR="+tmpDir,
	)
	cmdutil.SetupCommand(cmd)

	require.NoError(t, cmd.Start())
	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Wait()
	}()

	helperCmd := cmd
	t.Cleanup(func() {
		if helperCmd == nil || helperCmd.Process == nil {
			return
		}
		_ = cmdutil.TerminateProcessGroup(helperCmd, cmdutil.ForceTermination())
		select {
		case <-cmdDone:
		case <-time.After(2 * time.Second):
		}
	})

	require.Eventually(t, func() bool {
		_, err := os.Stat(readyFile)
		return err == nil
	}, 5*time.Second, 25*time.Millisecond, "helper did not start command script")

	pidData, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	scriptPID, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	require.NoError(t, err)
	t.Cleanup(func() {
		if scriptPID > 0 && processGroupAlive(scriptPID) {
			_ = syscall.Kill(-scriptPID, syscall.SIGKILL)
		}
	})

	require.NoError(t, cmdutil.TerminateProcessGroup(cmd, cmdutil.ForceTermination()))
	select {
	case <-cmdDone:
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit after SIGKILL")
	}
	helperCmd = nil

	require.Eventually(t, func() bool {
		return !processGroupAlive(scriptPID)
	}, 2*time.Second, 25*time.Millisecond, "script process group should die with its parent")
	scriptPID = 0
}

func runCommandParentDeathHelper(t *testing.T) {
	t.Helper()

	tmpDir := os.Getenv("DAGU_COMMAND_PARENT_DEATH_DIR")
	require.NotEmpty(t, tmpDir)
	pidFile := filepath.Join(tmpDir, "script.pid")
	readyFile := filepath.Join(tmpDir, "helper.ready")

	step := ir.Step{
		Name: "parent-death",
		Dir:  tmpDir,
		Script: "echo $$ > " + pidFile + "\n" +
			"while true; do sleep 1; done\n",
	}

	ctx := setupTestContext(t, nil, step)
	env := runtime.GetEnv(ctx)
	env.WorkingDir = tmpDir
	ctx = runtime.WithEnv(ctx, env)

	commandExec, err := NewCommand(ctx, step)
	require.NoError(t, err)
	errCh := make(chan error, 1)
	go func() {
		errCh <- commandExec.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		_, err := os.Stat(pidFile)
		return err == nil
	}, 5*time.Second, 25*time.Millisecond, "command script did not start")
	require.NoError(t, os.WriteFile(readyFile, []byte("ok"), 0600))

	select {
	case err := <-errCh:
		if err != nil {
			require.Truef(t, isSIGKILLExitError(err), "expected SIGKILL exit error, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("helper was not killed by parent test")
	}
}

func processGroupAlive(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || err == syscall.EPERM
}

func isSIGKILLExitError(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}

// A step can be stopped after its executor exists but before its process
// starts, for example when the coordinator rejects the attempt while the
// step is being set up. That stop must not be lost: the command must not
// start and run to completion as if nothing had been asked.
func TestCommandExecutor_StopBeforeStart(t *testing.T) {
	newSleep := func(t *testing.T) (context.Context, executor.Executor) {
		t.Helper()
		ctx := setupTestContext(t, nil, ir.Step{})
		exec, err := NewCommand(ctx, ir.Step{
			Name:     "sleep",
			Commands: []ir.CommandEntry{{Command: "sleep", Args: []string{"30"}, CmdWithArgs: "sleep 30"}},
		})
		require.NoError(t, err)
		return ctx, exec
	}

	t.Run("TerminationSkipsTheCommand", func(t *testing.T) {
		ctx, exec := newSleep(t)
		require.NoError(t, exec.(executor.Stopper).Stop(cmdutil.TerminationFromSignal(syscall.SIGTERM)))

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err := exec.Run(ctx)
		require.ErrorIs(t, err, errStoppedBeforeStart)
		require.NoError(t, ctx.Err(), "the command ran instead of honouring the stop")
	})

	t.Run("TerminationSkipsRemainingCommands", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "second-ran")
		ctx := setupTestContext(t, nil, ir.Step{})
		exec, err := NewCommand(ctx, ir.Step{
			Name: "two",
			Commands: []ir.CommandEntry{
				{Command: "true", CmdWithArgs: "true"},
				{Command: "touch", Args: []string{marker}, CmdWithArgs: "touch " + marker},
			},
		})
		require.NoError(t, err)
		require.NoError(t, exec.Kill(syscall.SIGTERM))

		err = exec.Run(ctx)
		require.ErrorIs(t, err, errStoppedBeforeStart)
		require.NoFileExists(t, marker)
	})

	t.Run("NonTerminationSignalStillRuns", func(t *testing.T) {
		ctx := setupTestContext(t, nil, ir.Step{})
		exec, err := NewCommand(ctx, ir.Step{
			Name:     "true",
			Commands: []ir.CommandEntry{{Command: "true", CmdWithArgs: "true"}},
		})
		require.NoError(t, err)
		require.NoError(t, exec.Kill(syscall.SIGWINCH))
		require.NoError(t, exec.Run(ctx))
	})
}
