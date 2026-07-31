// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestCoordinator_GetWorkers(t *testing.T) {
	t.Run("returnsRegisteredWorkers", func(t *testing.T) {
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())

		conn, err := grpc.NewClient(
			coord.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		require.NoError(t, err)
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("Failed to close connection: %v", err)
			}
		}()

		client := coordinatorv1.NewCoordinatorServiceClient(conn)

		resp, err := client.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Empty(t, resp.Workers)

		_, err = client.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{
			WorkerId: "test-worker-1",
			Labels:   map[string]string{"type": "compute", "region": "us-east"},
			Stats: &coordinatorv1.WorkerStats{
				TotalPollers: 2,
				BusyPollers:  0,
			},
		})
		require.NoError(t, err)

		_, err = client.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{
			WorkerId: "test-worker-2",
			Labels:   map[string]string{"type": "storage", "region": "us-west"},
			Stats: &coordinatorv1.WorkerStats{
				TotalPollers: 3,
				BusyPollers:  1,
				RunningTasks: []*coordinatorv1.RunningTask{
					{
						DagRunId:  "run-456",
						DagName:   "backup.yaml",
						StartedAt: time.Now().Unix(),
					},
				},
			},
		})
		require.NoError(t, err)

		resp, err = client.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Workers, 2)

		workerMap := make(map[string]*coordinatorv1.WorkerInfo)
		for _, w := range resp.Workers {
			workerMap[w.WorkerId] = w
		}

		w1, ok := workerMap["test-worker-1"]
		require.True(t, ok)
		require.Equal(t, map[string]string{"type": "compute", "region": "us-east"}, w1.Labels)
		require.Equal(t, int32(2), w1.TotalPollers)
		require.Equal(t, int32(0), w1.BusyPollers)
		require.Empty(t, w1.RunningTasks)

		w2, ok := workerMap["test-worker-2"]
		require.True(t, ok)
		require.Equal(t, map[string]string{"type": "storage", "region": "us-west"}, w2.Labels)
		require.Equal(t, int32(3), w2.TotalPollers)
		require.Equal(t, int32(1), w2.BusyPollers)
		require.Len(t, w2.RunningTasks, 1)
		require.Equal(t, "run-456", w2.RunningTasks[0].DagRunId)

		resp, err = client.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Workers, 2)
	})
}

func TestCoordinator_Heartbeat(t *testing.T) {
	t.Run("updatesWorkerStats", func(t *testing.T) {
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())

		conn, err := grpc.NewClient(
			coord.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		require.NoError(t, err)
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("Failed to close connection: %v", err)
			}
		}()

		client := coordinatorv1.NewCoordinatorServiceClient(conn)

		resp, err := client.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Empty(t, resp.Workers)

		_, err = client.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{
			WorkerId: "test-worker-1",
			Labels:   map[string]string{"type": "compute", "region": "us-east"},
			Stats: &coordinatorv1.WorkerStats{
				TotalPollers: 10,
				BusyPollers:  3,
				RunningTasks: []*coordinatorv1.RunningTask{
					{
						DagRunId:  "run-123",
						DagName:   "etl-pipeline.yaml",
						StartedAt: time.Now().Unix(),
					},
				},
			},
		})
		require.NoError(t, err)

		resp, err = client.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Workers, 1)

		worker := resp.Workers[0]
		require.Equal(t, "test-worker-1", worker.WorkerId)
		require.Equal(t, map[string]string{"type": "compute", "region": "us-east"}, worker.Labels)
		require.Equal(t, int32(10), worker.TotalPollers)
		require.Equal(t, int32(3), worker.BusyPollers)
		require.Len(t, worker.RunningTasks, 1)
		require.Equal(t, "run-123", worker.RunningTasks[0].DagRunId)
		require.Greater(t, worker.LastHeartbeatAt, int64(0))

		_, err = client.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{
			WorkerId: "test-worker-1",
			Labels:   map[string]string{"type": "compute", "region": "us-east"},
			Stats: &coordinatorv1.WorkerStats{
				TotalPollers: 10,
				BusyPollers:  0,
				RunningTasks: []*coordinatorv1.RunningTask{},
			},
		})
		require.NoError(t, err)

		resp, err = client.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Workers, 1)
		require.Equal(t, int32(0), resp.Workers[0].BusyPollers)
		require.Empty(t, resp.Workers[0].RunningTasks)
	})
}
