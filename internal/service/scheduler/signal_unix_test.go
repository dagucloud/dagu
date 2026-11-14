// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package scheduler

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/stretchr/testify/require"
)

type shutdownSignalCause struct {
	signal os.Signal
}

func (e shutdownSignalCause) Error() string { return e.signal.String() }

func (e shutdownSignalCause) As(target any) bool {
	sig, ok := target.(*os.Signal)
	if ok {
		*sig = e.signal
	}
	return ok
}

func TestWaitForTickCancellation(t *testing.T) {
	for _, signalReceived := range []bool{false, true} {
		name := "LockLoss"
		if signalReceived {
			name = "SIGTERM"
		}
		t.Run(name, func(t *testing.T) {
			reg := launcher.NewProcessRegistry()
			ctx, cancel := context.WithCancelCause(launcher.ContextWithProcessRegistry(t.Context(), reg))
			defer cancel(nil)
			res, err := launcher.StartProcess(ctx, launcher.CmdSpec{Executable: "sleep", Args: []string{"30"}})
			require.NoError(t, err)
			t.Cleanup(func() {
				select {
				case <-res.Done:
					return
				default:
				}
				if proc, err := os.FindProcess(res.PID); err == nil {
					_ = proc.Kill()
				}
			})
			if signalReceived {
				cancel(shutdownSignalCause{signal: syscall.SIGTERM})
			} else {
				cancel(nil)
			}
			timer := time.NewTimer(time.Hour)
			defer timer.Stop()
			sc := &Scheduler{}
			require.False(t, sc.waitForTick(ctx, make(chan os.Signal), timer))
			if !signalReceived {
				select {
				case err := <-res.Done:
					t.Fatalf("lock loss terminated the run: %v", err)
				case <-time.After(100 * time.Millisecond):
				}
				return
			}
			select {
			case err := <-res.Done:
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr)
				status, ok := exitErr.Sys().(syscall.WaitStatus)
				require.True(t, ok)
				require.Equal(t, syscall.SIGTERM, status.Signal())
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation lost the shutdown signal")
			}
		})
	}
}
