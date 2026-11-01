// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package frontend

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/signalctx"
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

// Force the cancellation path so a simultaneous signal subscription cannot
// hide replacement of SIGTERM with the fallback SIGINT.
func TestShutdownPreservesSignalCause(t *testing.T) {
	reg := launcher.NewProcessRegistry()
	ctx, cancel := context.WithCancelCause(signalctx.WithOSSignalsDisabled(launcher.ContextWithProcessRegistry(t.Context(), reg)))
	defer cancel(nil)
	ready := filepath.Join(t.TempDir(), "ready")
	stopped := filepath.Join(t.TempDir(), "stopped")
	res, err := launcher.StartProcess(ctx, launcher.CmdSpec{
		Executable: "sh",
		Args:       []string{"-c", `trap 'printf TERM > "$2"; exit 0' TERM; trap 'printf INT > "$2"; exit 0' INT; printf ready > "$1"; while :; do sleep 0.05; done`, "probe", ready, stopped},
	})
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
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	cancel(shutdownSignalCause{signal: syscall.SIGTERM})
	srv := &Server{}
	require.NoError(t, srv.setupGracefulShutdown(ctx, nil))
	select {
	case err := <-res.Done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not stop the registered run")
	}
	data, err := os.ReadFile(stopped)
	require.NoError(t, err)
	require.Equal(t, "TERM", string(data))
}

func TestShutdownWithoutSignalKeepsRuns(t *testing.T) {
	reg := launcher.NewProcessRegistry()
	ctx, cancel := context.WithCancel(signalctx.WithOSSignalsDisabled(launcher.ContextWithProcessRegistry(t.Context(), reg)))
	defer cancel()
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
	cancel()
	srv := &Server{}
	require.NoError(t, srv.setupGracefulShutdown(ctx, nil))
	select {
	case err := <-res.Done:
		t.Fatalf("ordinary cancellation stopped the run: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}
