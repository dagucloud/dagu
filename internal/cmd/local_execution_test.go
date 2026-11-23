// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileproc "github.com/dagucloud/dagu/v2/internal/persis/file/proc"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A process record that cannot be released is logged with its run, and the
// run's result is the one the run returned.
func TestLocalExecutionReleaseFailure(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "release", "steps:\n  - run: echo ok\n")
	ctx.Persistence.ProcRepository = persis.NewProcRepository(stopFailingProcStore{fileproc.New(t.TempDir())})
	var logs bytes.Buffer
	ctx.Context = logger.WithFixedLogger(ctx.Context, logger.NewLogger(logger.WithWriter(&logs), logger.WithQuiet()))

	const dagRunID = "release-run"
	runErr := errors.New("run failed")
	err := withPreparedLocalExecution(ctx, dag, dagRunID, runOptions{},
		func(execCtx context.Context) (dagrun.Attempt, error) {
			return ctx.Persistence.DAGRunRepository.CreateAttempt(execCtx, dag, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{})
		},
		func(dagrun.Attempt) error { return runErr },
	)
	require.ErrorIs(t, err, runErr)
	assert.NotErrorIs(t, err, errReleaseFailed)

	var entry map[string]any
	require.NoError(t, json.Unmarshal(logs.Bytes(), &entry), logs.String())
	assert.Equal(t, map[string]any{
		"level":  "WARN",
		"msg":    "Failed to release the process record of the dag-run",
		"dag":    "release",
		"run-id": dagRunID,
		"err":    errReleaseFailed.Error(),
	}, map[string]any{
		"level":  entry["level"],
		"msg":    entry["msg"],
		"dag":    entry["dag"],
		"run-id": entry["run-id"],
		"err":    entry["err"],
	})
}

var errReleaseFailed = errors.New("process record not removed")

// stopFailingProcStore acquires process records whose release fails.
type stopFailingProcStore struct {
	persis.ProcStore
}

func (s stopFailingProcStore) Acquire(ctx context.Context, groupName string, meta proc.ProcMeta) (proc.ProcHandle, error) {
	handle, err := s.ProcStore.Acquire(ctx, groupName, meta)
	if err != nil {
		return nil, err
	}
	return stopFailingHandle{handle}, nil
}

type stopFailingHandle struct {
	proc.ProcHandle
}

func (h stopFailingHandle) Stop(ctx context.Context) error {
	return errors.Join(h.ProcHandle.Stop(ctx), errReleaseFailed)
}
