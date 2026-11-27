// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// workersClient is a coordinator that lists fixed workers.
type workersClient struct {
	coordinator.Client
	workers []*coordinatorv1.WorkerInfo
}

func (c *workersClient) GetWorkers(context.Context) ([]*coordinatorv1.WorkerInfo, error) {
	return c.workers, nil
}

// A restart stops a worker-bound run only while a dispatch can reach the
// worker: the coordinator lists a stale heartbeat until it sweeps it, and
// that worker counts as offline, as it does for the dispatch.
func TestRequireWorkerOnline(t *testing.T) {
	t.Parallel()

	now := time.Now()
	stale := now.Add(-2 * dispatch.DefaultStaleWorkerHeartbeatThreshold)
	client := &workersClient{workers: []*coordinatorv1.WorkerInfo{
		{WorkerId: "gw-1", LastHeartbeatAt: now.Unix()},
		{WorkerId: "gw-2", LastHeartbeatAt: stale.Unix()},
	}}

	require.NoError(t, requireWorkerOnline(context.Background(), client, "gw-1"))
	require.EqualError(t, requireWorkerOnline(context.Background(), client, "gw-2"), `worker node "gw-2" is offline`)
	require.EqualError(t, requireWorkerOnline(context.Background(), client, "gw-3"), `worker node "gw-3" is offline`)
}
