// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package notification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Opening a FIFO waits for a writer, so a run log that is one must be skipped
// instead of stalling delivery.
func TestDeliverCommandSkipsFIFOLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "run.log")
	require.NoError(t, syscall.Mkfifo(logPath, 0o600))
	stdoutPath := filepath.Join(dir, "build.stdout")
	require.NoError(t, os.WriteFile(stdoutPath, []byte("step output\n"), 0o644))
	outPath := filepath.Join(dir, "captured.txt")

	svc := newCommandService()
	done := make(chan error, 1)
	go func() {
		done <- svc.deliverTarget(context.Background(), notificationmodel.Target{
			Type: notificationmodel.ProviderCommand,
			Command: &notificationmodel.CommandTarget{
				Command: captureCommand(outPath),
			},
		}, []chatbridge.NotificationEvent{{
			Type: eventstore.TypeDAGRunFailed,
			Status: &ir.DAGRunStatus{
				Name:   "daily-report",
				Status: ir.Failed,
				Log:    logPath,
				Nodes: []*ir.Node{{
					Step:   ir.Step{Name: "build"},
					Stdout: stdoutPath,
				}},
			},
		}})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("delivery blocked on the FIFO log")
	}

	body, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), "step output")
	assert.NotContains(t, string(body), "# DAG log")
}

// No process the command started keeps running after delivery returns,
// whether or not it still holds the shell's output pipes.
func TestDeliverCommandStopsLeftoverChildren(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		redirect string
		wantErr  bool
	}{
		// A child holding the pipes outlasts WaitDelay, which fails delivery.
		{name: "holding output", wantErr: true},
		{name: "detached output", redirect: " >/dev/null 2>&1", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			marker := filepath.Join(t.TempDir(), "late")
			start := time.Now()
			err := newCommandService().deliverTarget(context.Background(), notificationmodel.Target{
				Type: notificationmodel.ProviderCommand,
				Command: &notificationmodel.CommandTarget{
					Command: fmt.Sprintf("(sleep 2; touch %q)%s &", marker, tt.redirect),
				},
			}, []chatbridge.NotificationEvent{{
				Type:   eventstore.TypeDAGRunFailed,
				Status: &ir.DAGRunStatus{Name: "daily-report", Status: ir.Failed},
			}})
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			time.Sleep(time.Until(start.Add(3 * time.Second)))
			_, statErr := os.Stat(marker)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}
