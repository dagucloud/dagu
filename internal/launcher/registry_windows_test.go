// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package launcher_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/stretchr/testify/require"
)

const (
	processTreePIDEnv   = "DAGU_TEST_PROCESS_TREE_PID"
	processTreeChildEnv = "DAGU_TEST_PROCESS_TREE_CHILD"
)

func TestProcessTreeHelper(t *testing.T) {
	pidFile := os.Getenv(processTreePIDEnv)
	if pidFile == "" {
		return
	}
	if os.Getenv(processTreeChildEnv) == "true" {
		require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600))
	} else {
		executable, err := os.Executable()
		require.NoError(t, err)
		child := exec.Command(executable, "-test.run=^TestProcessTreeHelper$") //nolint:gosec // Test launches its own executable.
		child.Env = append(os.Environ(), processTreeChildEnv+"=true")
		require.NoError(t, child.Start())
		defer func() {
			_ = child.Process.Kill()
			_ = child.Wait()
		}()
	}
	time.Sleep(time.Minute)
}

// Windows propagation uses forced process-tree termination for both signals.
func TestPropagationStopsProcessTree(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			reg := launcher.NewProcessRegistry()
			ctx := launcher.ContextWithProcessRegistry(t.Context(), reg)
			pidFile := filepath.Join(t.TempDir(), "child-pid")
			executable, err := os.Executable()
			require.NoError(t, err)
			result, err := launcher.StartProcess(ctx, launcher.CmdSpec{
				Executable: executable,
				Args:       []string{"-test.run=^TestProcessTreeHelper$"},
				Env:        append(os.Environ(), processTreePIDEnv+"="+pidFile),
			})
			require.NoError(t, err)
			var childPID int
			t.Cleanup(func() {
				for _, pid := range []int{result.PID, childPID} {
					if procutil.IsAlive(pid) {
						_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
					}
				}
			})
			require.Eventually(t, func() bool {
				data, err := os.ReadFile(pidFile)
				if err != nil {
					return false
				}
				childPID, err = strconv.Atoi(string(data))
				return err == nil && procutil.IsAlive(childPID)
			}, 20*time.Second, 20*time.Millisecond)
			require.True(t, procutil.IsAlive(result.PID))
			done := launcher.PropagateSignal(ctx, sig)
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("propagation did not complete after process-tree termination")
			}
			select {
			case err := <-result.Done:
				require.Error(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("tracked process did not report its exit")
			}
			require.Eventually(t, func() bool {
				return !procutil.IsAlive(result.PID) && !procutil.IsAlive(childPID)
			}, 5*time.Second, 20*time.Millisecond, "propagation left part of the process tree running")
		})
	}
}
