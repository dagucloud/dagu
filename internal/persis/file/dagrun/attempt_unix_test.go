// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package dagrun

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestAttempt_WriteAfterWriteError checks that one failed write does not fail
// the writes after it, nor the compaction on close.
func TestAttempt_WriteAfterWriteError(t *testing.T) {
	ctx := context.Background()
	// failNextWrite puts a read-only descriptor of the same file in place of
	// the writer's, so the next write fails while the handle still names the
	// file, as it does on an I/O error.
	failNextWrite := func(t *testing.T, att *Attempt, status ir.DAGRunStatus) {
		t.Helper()
		readOnly, err := os.Open(att.file)
		require.NoError(t, err)
		defer func() { _ = readOnly.Close() }()
		require.NoError(t, unix.Dup2(int(readOnly.Fd()), int(att.writer.file.Fd())))
		require.ErrorIs(t, att.Write(ctx, status), ErrWriteFailed)
	}
	// A status larger than the write buffer goes to the file while it is
	// encoded; a smaller one goes when the buffer is flushed.
	large := statusWithPID(2)
	large.Log = strings.Repeat("x", 64*1024)

	for name, failed := range map[string]ir.DAGRunStatus{
		"FailedFlush":  statusWithPID(2),
		"FailedEncode": large,
	} {
		t.Run(name, func(t *testing.T) {
			att := openAttemptForTest(t)
			require.NoError(t, att.Write(ctx, statusWithPID(1)))
			failNextWrite(t, att, failed)

			require.NoError(t, att.Write(ctx, statusWithPID(3)))

			status, err := att.ReadStatus(ctx)
			require.NoError(t, err)
			assert.Equal(t, ir.PID(3), status.PID)
		})
	}

	t.Run("FailedOpenRetries", func(t *testing.T) {
		att := openAttemptForTest(t)
		require.NoError(t, att.Write(ctx, statusWithPID(1)))
		failNextWrite(t, att, statusWithPID(2))
		blockStatusFile(t, att.file)
		require.ErrorIs(t, att.Write(ctx, statusWithPID(3)), ErrWriteFailed)

		require.NoError(t, os.Remove(att.file))
		require.NoError(t, att.Write(ctx, statusWithPID(4)))

		status, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.PID(4), status.PID)
	})

	t.Run("CloseCompacts", func(t *testing.T) {
		att := openAttemptForTest(t)
		require.NoError(t, att.Write(ctx, statusWithPID(1)))
		require.NoError(t, att.Write(ctx, statusWithPID(2)))
		failNextWrite(t, att, statusWithPID(3))

		require.NoError(t, att.Close(ctx))

		data, err := os.ReadFile(att.file)
		require.NoError(t, err)
		assert.Equal(t, 1, bytes.Count(data, []byte("\n")))
		status, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.PID(2), status.PID)
	})
}
