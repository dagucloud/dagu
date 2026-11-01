// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enqueueWebhookRun(th test.Helper, dag *ir.DAG, runID, params string) error {
	return scheduler.EnqueueWebhookRun(
		th.Context,
		th.DAGRunRepository,
		th.QueueStore,
		th.Config.Paths.LogDir,
		th.Config.Paths.ArtifactDir,
		th.Config.Paths.BaseConfig,
		"",
		dag.FileName(),
		dag,
		runID,
		params,
		"prod",
		time.Now(),
	)
}

// A delivery sent again names the same run, which is queued once.
func TestEnqueueWebhookRunQueuesOnce(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `name: webhook-enqueue
steps:
  - name: step1
    run: echo "$WEBHOOK_PAYLOAD"
`)
	params := intake.WebhookRuntimeParams(`{"ref":"main"}`, `{}`)
	for range 2 {
		require.NoError(t, enqueueWebhookRun(th, dag.DAG, "delivery-1", params))
	}

	attempt, err := th.DAGRunRepository.FindAttempt(th.Context, ir.NewDAGRunRef(dag.Name, "delivery-1"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, status.Status)
	assert.Equal(t, ir.TriggerTypeWebhook, status.TriggerType)
	assert.Equal(t, "prod", status.ProfileName)
	assert.Equal(t, dag.FileName(), status.DefinitionID)
	assert.Contains(t, status.ParamsList, `WEBHOOK_PAYLOAD={"ref":"main"}`)

	items, err := th.QueueStore.List(th.Context, dag.ProcGroup())
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

// A run the DAG cannot accept is reported as such, so the delivery is not
// retried for a run that can never start.
func TestEnqueueWebhookRunRejectsAnInvalidRun(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `name: webhook-required-param
params:
  - name: region
    type: string
    required: true
steps:
  - name: step1
    run: echo "$region"
`)
	err := enqueueWebhookRun(th, dag.DAG, "delivery-1", intake.WebhookRuntimeParams(`{}`, `{}`))
	require.ErrorIs(t, err, publicwebhook.ErrInvalidRun)
	assert.ErrorContains(t, err, "region")

	items, err := th.QueueStore.List(th.Context, dag.ProcGroup())
	require.NoError(t, err)
	assert.Empty(t, items)
}
