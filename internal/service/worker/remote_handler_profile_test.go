// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/worker"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportTaskLoadFailurePreservesProfileName(t *testing.T) {
	t.Parallel()

	status, err := worker.ReportTaskLoadFailureStatusForTest(
		context.Background(),
		&coordinatorv1.Task{
			Target:    "child",
			DagRunId:  "run-1",
			AttemptId: "attempt-1",
			Params:    "ENV=prod",
		},
		exec.NewDAGRunRef("root", "root-run"),
		exec.NewDAGRunRef("parent", "parent-run"),
		errors.New("load failed"),
		"prod",
	)
	require.NoError(t, err)

	assert.Equal(t, core.Failed, status.Status)
	assert.Equal(t, "prod", status.ProfileName)
	assert.Equal(t, exec.NewDAGRunRef("root", "root-run"), status.Root)
	assert.Equal(t, exec.NewDAGRunRef("parent", "parent-run"), status.Parent)
}

func TestReportTaskInitFailurePreservesProfileName(t *testing.T) {
	t.Parallel()

	status, err := worker.ReportTaskInitFailureStatusForTest(
		context.Background(),
		&coordinatorv1.Task{
			Target:    "child",
			DagRunId:  "run-1",
			AttemptId: "attempt-1",
			Params:    "ENV=prod",
		},
		exec.NewDAGRunRef("root", "root-run"),
		exec.NewDAGRunRef("parent", "parent-run"),
		errors.New("init failed"),
		"prod",
	)
	require.NoError(t, err)

	assert.Equal(t, core.Failed, status.Status)
	assert.Equal(t, "prod", status.ProfileName)
	assert.Equal(t, exec.NewDAGRunRef("root", "root-run"), status.Root)
	assert.Equal(t, exec.NewDAGRunRef("parent", "parent-run"), status.Parent)
}
