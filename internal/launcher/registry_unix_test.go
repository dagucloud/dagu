// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package launcher_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/launcher"
)

type propagationWriter struct {
	ctx  context.Context
	reg  *launcher.ProcessRegistry
	once sync.Once
}

func (w *propagationWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { w.reg.Propagate(w.ctx, syscall.SIGTERM) })
	return len(p), nil
}

// A child can emit readiness while the launcher is still returning from Start.
func TestPropagationDuringStart(t *testing.T) {
	for _, asynchronous := range []bool{false, true} {
		t.Run(strconv.FormatBool(asynchronous), func(t *testing.T) {
			for range 30 {
				reg := launcher.NewProcessRegistry()
				ctx := launcher.ContextWithProcessRegistry(t.Context(), reg)
				pidFile := filepath.Join(t.TempDir(), "pid")
				spec := launcher.CmdSpec{
					Executable: "sh",
					Args:       []string{"-c", `printf '%s' $$ > "$1"; printf ready; exec sleep 30`, "probe", pidFile},
					Stdout:     &propagationWriter{ctx: ctx, reg: reg},
				}
				done := make(chan error, 1)
				go func() {
					if !asynchronous {
						done <- launcher.Run(ctx, spec)
						return
					}
					result, err := launcher.StartProcess(ctx, spec)
					if err != nil {
						done <- err
						return
					}
					done <- <-result.Done
				}()
				select {
				case err := <-done:
					require.Error(t, err)
				case <-time.After(3 * time.Second):
					data, err := os.ReadFile(pidFile)
					require.NoError(t, err)
					pid, err := strconv.Atoi(string(data))
					require.NoError(t, err)
					_ = syscall.Kill(-pid, syscall.SIGKILL)
					<-done
					t.Fatal("shutdown missed a child that had already emitted readiness")
				}
			}
		})
	}
}

func TestPropagationWaitsForAllRuns(t *testing.T) {
	reg := launcher.NewProcessRegistry()
	ctx, cancel := context.WithCancel(launcher.ContextWithProcessRegistry(t.Context(), reg))
	defer cancel()
	type run struct {
		result  *launcher.StartResult
		release string
	}
	runs := make([]run, 2)
	for i := range runs {
		dir := t.TempDir()
		ready := filepath.Join(dir, "ready")
		release := filepath.Join(dir, "release")
		result, err := launcher.StartProcess(ctx, launcher.CmdSpec{
			Executable: "sh",
			Args:       []string{"-c", `release=$2; cleanup() { trap '' TERM INT; while [ ! -f "$release" ]; do sleep 0.05; done; exit 42; }; trap cleanup TERM INT; printf ready > "$1"; while :; do sleep 0.05; done`, "probe", ready, release},
		})
		require.NoError(t, err)
		runs[i] = run{result: result, release: release}
		t.Cleanup(func() { _ = syscall.Kill(-result.PID, syscall.SIGKILL) })
		require.Eventually(t, func() bool {
			_, err := os.Stat(ready)
			return err == nil
		}, 5*time.Second, 10*time.Millisecond)
	}
	cancel()
	for _, sig := range []os.Signal{syscall.SIGHUP, syscall.SIGQUIT} {
		select {
		case <-launcher.PropagateSignal(ctx, sig):
		default:
			t.Fatal("unsupported signal must not wait for runners")
		}
	}

	var callers sync.WaitGroup
	completions := make([]<-chan struct{}, 8)
	for i := range completions {
		callers.Go(func() { completions[i] = launcher.PropagateSignal(ctx, syscall.SIGTERM) })
	}
	callers.Wait()
	done := completions[0]
	for _, completion := range completions {
		require.Equal(t, done, completion)
	}
	for i, run := range runs {
		select {
		case <-done:
			t.Fatal("propagation completed before all runners exited")
		case <-time.After(100 * time.Millisecond):
		}
		require.NoError(t, os.WriteFile(run.release, nil, 0o600))
		select {
		case err := <-run.result.Done:
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr, "run %d lost its exit result", i)
			require.Equal(t, 42, exitErr.ExitCode())
		case <-time.After(5 * time.Second):
			t.Fatal("released runner did not exit")
		}
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("propagation did not complete after all runners exited")
	}
}

// The registry signals the whole process group, so children spawned by the
// launched command terminate too. A shell running a nested sleep proves the
// signal reached past the directly started process.
func TestProcessRegistryPropagatesToProcessGroup(t *testing.T) {
	reg := launcher.NewProcessRegistry()
	ctx := launcher.ContextWithProcessRegistry(context.Background(), reg)

	res, err := launcher.StartProcess(ctx, launcher.CmdSpec{
		Executable: "sh",
		Args:       []string{"-c", "sleep 30"},
	})
	require.NoError(t, err)

	reg.Propagate(ctx, syscall.SIGTERM)

	select {
	case <-res.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("propagated signal did not terminate the process group")
	}

	// Once every group member exited, probing the empty group fails with ESRCH.
	require.Eventually(t, func() bool {
		return syscall.Kill(-res.PID, 0) == syscall.ESRCH
	}, 3*time.Second, 50*time.Millisecond)
}

// Cancellation must leave a registered run alive so shutdown can forward the
// actual signal and the run can complete its own cleanup.
func TestRunCancellationWithRegistry(t *testing.T) {
	reg := launcher.NewProcessRegistry()
	ctx, cancel := context.WithCancel(launcher.ContextWithProcessRegistry(t.Context(), reg))
	defer cancel()
	ready := filepath.Join(t.TempDir(), "ready")
	stopped := filepath.Join(t.TempDir(), "stopped")
	done := make(chan error, 1)
	go func() {
		done <- launcher.Run(ctx, launcher.CmdSpec{
			Executable: "sh",
			Args:       []string{"-c", `trap 'printf TERM > "$2"; exit 0' TERM; printf ready > "$1"; while :; do sleep 0.05; done`, "probe", ready, stopped},
		})
	}()
	t.Cleanup(func() {
		reg.Propagate(context.Background(), syscall.SIGKILL)
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		t.Fatalf("cancellation terminated the registered run: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	reg.Propagate(context.Background(), syscall.SIGTERM)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("registered run did not finish after SIGTERM")
	}
	data, err := os.ReadFile(stopped)
	require.NoError(t, err)
	require.Equal(t, "TERM", string(data))
}

func TestRunCancellationWithoutRegistry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := filepath.Join(t.TempDir(), "ready")
	done := make(chan error, 1)
	go func() {
		done <- launcher.Run(ctx, launcher.CmdSpec{
			Executable: "sh",
			Args:       []string{"-c", `printf ready > "$1"; exec sleep 30`, "probe", ready},
		})
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop the unregistered run")
	}
}
