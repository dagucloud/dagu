// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func parseHostPort(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func TestClientNew(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	monitor := &mockServiceMonitor{}

	client := coordinator.New(monitor, config)
	require.NotNil(t, client)

	// Check initial metrics
	metrics := client.Metrics()
	assert.True(t, metrics.IsConnected)
	assert.Equal(t, 0, metrics.ConsecutiveFails)
	assert.Equal(t, 0, metrics.FailCount)
	assert.Nil(t, metrics.LastError)
}

func TestClientDispatch(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		config := coordinator.DefaultConfig()
		config.MaxRetries = 0
		config.RequestTimeout = 100 * time.Millisecond

		mockCoord := &mockCoordinatorService{
			dispatchFunc: func(_ context.Context, req *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
				assert.Equal(t, "test-dag-run", req.Task.DagRunId)
				assert.Equal(t, "test.yaml", req.Task.Target)
				return &coordinatorv1.DispatchResponse{}, nil
			},
		}

		server, addr := startMockServer(t, mockCoord)
		defer server.Stop()

		host, port := parseHostPort(addr)
		monitor := &mockServiceMonitor{
			members: []exec.HostInfo{
				{ID: "coord-1", Host: host, Port: port, Status: exec.ServiceStatusActive},
			},
		}

		client := coordinator.New(monitor, config)

		task := &coordinatorv1.Task{
			DagRunId: "test-dag-run",
			Target:   "test.yaml",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		err := client.Dispatch(ctx, task)
		require.NoError(t, err)
	})

	t.Run("NoCoordinators", func(t *testing.T) {
		t.Parallel()

		config := coordinator.DefaultConfig()
		config.MaxRetries = 0
		config.RequestTimeout = 100 * time.Millisecond

		monitor := &mockServiceMonitor{
			members: []exec.HostInfo{}, // No coordinators
		}

		client := coordinator.New(monitor, config)

		task := &coordinatorv1.Task{
			DagRunId: "test-dag-run",
			Target:   "test.yaml",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		err := client.Dispatch(ctx, task)
		require.Error(t, err)
		// Could be either error depending on timing
		assert.True(t, strings.Contains(err.Error(), "no coordinators available") ||
			strings.Contains(err.Error(), "context deadline exceeded"))
	})

	t.Run("StaleQueueDispatchReturnsPermanentTypedError", func(t *testing.T) {
		t.Parallel()

		config := coordinator.DefaultConfig()
		config.MaxRetries = 0
		config.RequestTimeout = 100 * time.Millisecond

		mockCoord := &mockCoordinatorService{
			dispatchFunc: func(_ context.Context, _ *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
				return nil, status.Error(codes.FailedPrecondition, (&exec.StaleQueueDispatchError{
					Reason: "queued attempt was superseded",
				}).Error())
			},
		}

		server, addr := startMockServer(t, mockCoord)
		defer server.Stop()

		host, port := parseHostPort(addr)
		monitor := &mockServiceMonitor{
			members: []exec.HostInfo{
				{ID: "coord-1", Host: host, Port: port, Status: exec.ServiceStatusActive},
			},
		}

		client := coordinator.New(monitor, config)

		err := client.Dispatch(context.Background(), &coordinatorv1.Task{
			DagRunId: "run-123",
			Target:   "test-dag",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, backoff.ErrPermanent)

		var staleErr *exec.StaleQueueDispatchError
		require.ErrorAs(t, err, &staleErr)
		require.Equal(t, "queued attempt was superseded", staleErr.Reason)
	})
}

func TestClientPoll(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	expectedTask := &coordinatorv1.Task{
		DagRunId:  "test-dag-run",
		Target:    "test.yaml",
		Operation: coordinatorv1.Operation_OPERATION_START,
	}

	mockCoord := &mockCoordinatorService{
		pollFunc: func(_ context.Context, req *coordinatorv1.PollRequest) (*coordinatorv1.PollResponse, error) {
			assert.Equal(t, "test-worker", req.WorkerId)
			return &coordinatorv1.PollResponse{Task: expectedTask}, nil
		},
	}

	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{{Host: host, Port: port, Status: exec.ServiceStatusActive}},
	}

	client := coordinator.New(monitor, config)

	req := &coordinatorv1.PollRequest{
		WorkerId: "test-worker",
		Labels:   map[string]string{"type": "test"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	policy := backoff.NewConstantBackoffPolicy(10 * time.Millisecond)
	task, err := client.Poll(ctx, policy, req)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, expectedTask.DagRunId, task.DagRunId)
}

func TestClientGetWorkers(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	expectedWorkers := []*coordinatorv1.WorkerInfo{
		{
			WorkerId:     "worker-1",
			TotalPollers: 5,
		},
		{
			WorkerId:     "worker-2",
			TotalPollers: 3,
		},
	}

	mockCoord := &mockCoordinatorService{
		getWorkersFunc: func(_ context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return &coordinatorv1.GetWorkersResponse{Workers: expectedWorkers}, nil
		},
	}

	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{{Host: host, Port: port, Status: exec.ServiceStatusActive}},
	}

	client := coordinator.New(monitor, config)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	workers, err := client.GetWorkers(ctx)
	require.NoError(t, err)
	assert.Len(t, workers, 2)
	assert.Equal(t, "worker-1", workers[0].WorkerId)
	assert.Equal(t, "worker-2", workers[1].WorkerId)
}

func TestClientGetWorkers_DeduplicatesAndSorts(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	olderHeartbeat := time.Now().Add(-2 * time.Minute).Unix()
	newerHeartbeat := time.Now().Unix()

	oldTask := &coordinatorv1.RunningTask{DagRunId: "old-run", DagName: "old-dag"}
	newTask := &coordinatorv1.RunningTask{DagRunId: "new-run", DagName: "new-dag"}

	coord1 := &mockCoordinatorService{
		getWorkersFunc: func(_ context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return &coordinatorv1.GetWorkersResponse{
				Workers: []*coordinatorv1.WorkerInfo{
					{
						WorkerId:        "worker-b",
						Labels:          map[string]string{"source": "old"},
						TotalPollers:    2,
						BusyPollers:     1,
						RunningTasks:    []*coordinatorv1.RunningTask{oldTask},
						LastHeartbeatAt: olderHeartbeat,
						HealthStatus:    coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_WARNING,
					},
				},
			}, nil
		},
	}
	server1, addr1 := startMockServer(t, coord1)
	defer server1.Stop()

	coord2 := &mockCoordinatorService{
		getWorkersFunc: func(_ context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return &coordinatorv1.GetWorkersResponse{
				Workers: []*coordinatorv1.WorkerInfo{
					{
						WorkerId:        "worker-a",
						Labels:          map[string]string{"role": "gpu"},
						TotalPollers:    4,
						BusyPollers:     0,
						LastHeartbeatAt: newerHeartbeat,
						HealthStatus:    coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_HEALTHY,
					},
					{
						WorkerId:        "worker-b",
						Labels:          map[string]string{"source": "new"},
						TotalPollers:    5,
						BusyPollers:     3,
						RunningTasks:    []*coordinatorv1.RunningTask{newTask},
						LastHeartbeatAt: newerHeartbeat,
						HealthStatus:    coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_HEALTHY,
					},
				},
			}, nil
		},
	}
	server2, addr2 := startMockServer(t, coord2)
	defer server2.Stop()

	host1, port1 := parseHostPort(addr1)
	host2, port2 := parseHostPort(addr2)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-2", Host: host2, Port: port2, Status: exec.ServiceStatusActive},
			{ID: "coord-1", Host: host1, Port: port1, Status: exec.ServiceStatusActive},
		},
	}

	client := coordinator.New(monitor, config)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	workers, err := client.GetWorkers(ctx)
	require.NoError(t, err)
	require.Len(t, workers, 2)

	assert.Equal(t, "worker-a", workers[0].WorkerId)
	assert.Equal(t, "worker-b", workers[1].WorkerId)

	assert.Equal(t, newerHeartbeat, workers[1].LastHeartbeatAt)
	assert.Equal(t, map[string]string{"source": "new"}, workers[1].Labels)
	assert.Equal(t, int32(5), workers[1].TotalPollers)
	assert.Equal(t, int32(3), workers[1].BusyPollers)
	assert.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_HEALTHY, workers[1].HealthStatus)
	require.Len(t, workers[1].RunningTasks, 1)
	assert.Equal(t, "new-run", workers[1].RunningTasks[0].DagRunId)
}

func TestClientGetWorkers_TieBreaksByStableCoordinatorOrder(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	sameHeartbeat := time.Now().Unix()

	coordA := &mockCoordinatorService{
		getWorkersFunc: func(_ context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return &coordinatorv1.GetWorkersResponse{
				Workers: []*coordinatorv1.WorkerInfo{
					{
						WorkerId:        "worker-1",
						Labels:          map[string]string{"source": "coord-a"},
						BusyPollers:     1,
						LastHeartbeatAt: sameHeartbeat,
					},
				},
			}, nil
		},
	}
	serverA, addrA := startMockServer(t, coordA)
	defer serverA.Stop()

	coordB := &mockCoordinatorService{
		getWorkersFunc: func(_ context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return &coordinatorv1.GetWorkersResponse{
				Workers: []*coordinatorv1.WorkerInfo{
					{
						WorkerId:        "worker-1",
						Labels:          map[string]string{"source": "coord-b"},
						BusyPollers:     2,
						LastHeartbeatAt: sameHeartbeat,
					},
				},
			}, nil
		},
	}
	serverB, addrB := startMockServer(t, coordB)
	defer serverB.Stop()

	hostA, portA := parseHostPort(addrA)
	hostB, portB := parseHostPort(addrB)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-b", Host: hostB, Port: portB, Status: exec.ServiceStatusActive},
			{ID: "coord-a", Host: hostA, Port: portA, Status: exec.ServiceStatusActive},
		},
	}

	client := coordinator.New(monitor, config)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	workers, err := client.GetWorkers(ctx)
	require.NoError(t, err)
	require.Len(t, workers, 1)

	assert.Equal(t, map[string]string{"source": "coord-a"}, workers[0].Labels)
	assert.Equal(t, int32(1), workers[0].BusyPollers)
}

func TestClientGetWorkers_PartialFailureStillReturnsWorkers(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	failingCoord := &mockCoordinatorService{
		getWorkersFunc: func(_ context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return nil, status.Error(codes.Unavailable, "coordinator unavailable")
		},
	}
	failingServer, failingAddr := startMockServer(t, failingCoord)
	defer failingServer.Stop()

	successCoord := &mockCoordinatorService{
		getWorkersFunc: func(_ context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return &coordinatorv1.GetWorkersResponse{
				Workers: []*coordinatorv1.WorkerInfo{
					{
						WorkerId:        "worker-1",
						LastHeartbeatAt: time.Now().Unix(),
					},
				},
			}, nil
		},
	}
	successServer, successAddr := startMockServer(t, successCoord)
	defer successServer.Stop()

	failingHost, failingPort := parseHostPort(failingAddr)
	successHost, successPort := parseHostPort(successAddr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-fail", Host: failingHost, Port: failingPort, Status: exec.ServiceStatusActive},
			{ID: "coord-ok", Host: successHost, Port: successPort, Status: exec.ServiceStatusActive},
		},
	}

	client := coordinator.New(monitor, config)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	workers, err := client.GetWorkers(ctx)
	require.Error(t, err)
	require.Len(t, workers, 1)
	assert.Equal(t, "worker-1", workers[0].WorkerId)
	assert.ErrorContains(t, err, "partial failure getting workers")
	assert.ErrorContains(t, err, "coordinator unavailable")
}

func TestClientStateMutationDoesNotRetryAfterRPCError(t *testing.T) {
	t.Parallel()

	t.Run("PutState", func(t *testing.T) {
		t.Parallel()

		config := coordinator.DefaultConfig()
		config.RequestTimeout = 100 * time.Millisecond

		var firstCalls atomic.Int32
		firstCoord := &mockCoordinatorService{
			putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
				firstCalls.Add(1)
				return nil, status.Error(codes.Unavailable, "ambiguous write")
			},
		}
		firstServer, firstAddr := startMockServer(t, firstCoord)
		defer firstServer.Stop()

		var secondCalls atomic.Int32
		secondCoord := &mockCoordinatorService{
			putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
				secondCalls.Add(1)
				return nil, status.Error(codes.Unavailable, "ambiguous write")
			},
		}
		secondServer, secondAddr := startMockServer(t, secondCoord)
		defer secondServer.Stop()

		firstHost, firstPort := parseHostPort(firstAddr)
		secondHost, secondPort := parseHostPort(secondAddr)
		monitor := &mockServiceMonitor{
			members: []exec.HostInfo{
				{ID: "coord-b", Host: secondHost, Port: secondPort, Status: exec.ServiceStatusActive},
				{ID: "coord-a", Host: firstHost, Port: firstPort, Status: exec.ServiceStatusActive},
			},
		}

		stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
		require.True(t, ok)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{
			Ref:   &coordinatorv1.StateRef{Scope: "dag", Namespace: "daily-agent", Key: "cursor"},
			Value: []byte(`1`),
		})
		require.Error(t, err)
		assert.Equal(t, int32(1), firstCalls.Load()+secondCalls.Load())
	})

	t.Run("DeleteState", func(t *testing.T) {
		t.Parallel()

		config := coordinator.DefaultConfig()
		config.RequestTimeout = 100 * time.Millisecond

		var firstCalls atomic.Int32
		firstCoord := &mockCoordinatorService{
			deleteStateFunc: func(context.Context, *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
				firstCalls.Add(1)
				return nil, status.Error(codes.Unavailable, "ambiguous delete")
			},
		}
		firstServer, firstAddr := startMockServer(t, firstCoord)
		defer firstServer.Stop()

		var secondCalls atomic.Int32
		secondCoord := &mockCoordinatorService{
			deleteStateFunc: func(context.Context, *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
				secondCalls.Add(1)
				return nil, status.Error(codes.Unavailable, "ambiguous delete")
			},
		}
		secondServer, secondAddr := startMockServer(t, secondCoord)
		defer secondServer.Stop()

		firstHost, firstPort := parseHostPort(firstAddr)
		secondHost, secondPort := parseHostPort(secondAddr)
		monitor := &mockServiceMonitor{
			members: []exec.HostInfo{
				{ID: "coord-b", Host: secondHost, Port: secondPort, Status: exec.ServiceStatusActive},
				{ID: "coord-a", Host: firstHost, Port: firstPort, Status: exec.ServiceStatusActive},
			},
		}

		stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
		require.True(t, ok)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		_, err := stateClient.DeleteState(ctx, &coordinatorv1.DeleteStateRequest{
			Ref: &coordinatorv1.StateRef{Scope: "dag", Namespace: "daily-agent", Key: "cursor"},
		})
		require.Error(t, err)
		assert.Equal(t, int32(1), firstCalls.Load()+secondCalls.Load())
	})
}

func TestClientStateOperationsUsePinnedCoordinator(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	ref := &coordinatorv1.StateRef{Scope: "dag", Namespace: "daily-agent", Key: "cursor"}
	entry := &coordinatorv1.StateEntry{Ref: ref, Value: []byte(`1`), Version: 1}

	var firstGets atomic.Int32
	var firstPuts atomic.Int32
	var firstDeletes atomic.Int32
	var firstLists atomic.Int32
	firstCoord := &mockCoordinatorService{
		getStateFunc: func(context.Context, *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error) {
			firstGets.Add(1)
			return &coordinatorv1.GetStateResponse{Found: true, Entry: entry}, nil
		},
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			firstPuts.Add(1)
			return &coordinatorv1.PutStateResponse{Entry: entry}, nil
		},
		deleteStateFunc: func(context.Context, *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
			firstDeletes.Add(1)
			return &coordinatorv1.DeleteStateResponse{Deleted: true}, nil
		},
		listStateFunc: func(context.Context, *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
			firstLists.Add(1)
			return &coordinatorv1.ListStateResponse{Entries: []*coordinatorv1.StateEntry{entry}}, nil
		},
	}
	firstServer, firstAddr := startMockServer(t, firstCoord)
	defer firstServer.Stop()

	var secondCalls atomic.Int32
	secondCoord := &mockCoordinatorService{
		getStateFunc: func(context.Context, *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error) {
			secondCalls.Add(1)
			return &coordinatorv1.GetStateResponse{Found: true, Entry: entry}, nil
		},
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			secondCalls.Add(1)
			return &coordinatorv1.PutStateResponse{Entry: entry}, nil
		},
		deleteStateFunc: func(context.Context, *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
			secondCalls.Add(1)
			return &coordinatorv1.DeleteStateResponse{Deleted: true}, nil
		},
		listStateFunc: func(context.Context, *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
			secondCalls.Add(1)
			return &coordinatorv1.ListStateResponse{Entries: []*coordinatorv1.StateEntry{entry}}, nil
		},
	}
	secondServer, secondAddr := startMockServer(t, secondCoord)
	defer secondServer.Stop()

	firstHost, firstPort := parseHostPort(firstAddr)
	secondHost, secondPort := parseHostPort(secondAddr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: exec.ServiceStatusActive},
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: exec.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)
	_, err = stateClient.GetState(ctx, &coordinatorv1.GetStateRequest{Ref: ref})
	require.NoError(t, err)
	_, err = stateClient.ListState(ctx, &coordinatorv1.ListStateRequest{Scope: "dag", Namespace: "daily-agent"})
	require.NoError(t, err)
	_, err = stateClient.DeleteState(ctx, &coordinatorv1.DeleteStateRequest{Ref: ref})
	require.NoError(t, err)

	firstCalls := firstPuts.Load() + firstGets.Load() + firstLists.Load() + firstDeletes.Load()
	secondCallCount := secondCalls.Load()
	assert.True(t, firstCalls == 4 || secondCallCount == 4)
	assert.True(t, firstCalls == 0 || secondCallCount == 0)
}

func TestClientStateCoordinatorPinIsDeterministicByNamespaceAcrossClients(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	ref := &coordinatorv1.StateRef{Scope: "dag", Namespace: "daily-agent", Key: "cursor"}
	entry := &coordinatorv1.StateEntry{Ref: ref, Value: []byte(`1`), Version: 1}

	var firstPuts atomic.Int32
	firstCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			firstPuts.Add(1)
			return &coordinatorv1.PutStateResponse{Entry: entry}, nil
		},
	}
	firstServer, firstAddr := startMockServer(t, firstCoord)
	defer firstServer.Stop()

	var secondPuts atomic.Int32
	secondCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			secondPuts.Add(1)
			return &coordinatorv1.PutStateResponse{Entry: entry}, nil
		},
	}
	secondServer, secondAddr := startMockServer(t, secondCoord)
	defer secondServer.Stop()

	firstHost, firstPort := parseHostPort(firstAddr)
	secondHost, secondPort := parseHostPort(secondAddr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: exec.ServiceStatusActive},
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: exec.ServiceStatusActive},
		},
	}

	firstClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)
	secondClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := firstClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)
	_, err = secondClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`2`)})
	require.NoError(t, err)

	firstCallCount := firstPuts.Load()
	secondCallCount := secondPuts.Load()
	assert.Equal(t, int32(2), firstCallCount+secondCallCount)
	assert.True(t, firstCallCount == 2 || secondCallCount == 2)
}

func TestClientStatePinnedCoordinatorDoesNotFailOverAfterUnavailable(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	ref := &coordinatorv1.StateRef{Scope: "dag", Namespace: "daily-agent", Key: "cursor"}
	entry := &coordinatorv1.StateEntry{Ref: ref, Value: []byte(`1`), Version: 1}

	var firstPuts atomic.Int32
	firstCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			if firstPuts.Add(1) == 1 {
				return &coordinatorv1.PutStateResponse{Entry: entry}, nil
			}
			return nil, status.Error(codes.Unavailable, "ambiguous write")
		},
	}
	firstServer, firstAddr := startMockServer(t, firstCoord)
	defer firstServer.Stop()

	var secondPuts atomic.Int32
	secondCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			if secondPuts.Add(1) == 1 {
				return &coordinatorv1.PutStateResponse{Entry: entry}, nil
			}
			return nil, status.Error(codes.Unavailable, "ambiguous write")
		},
	}
	secondServer, secondAddr := startMockServer(t, secondCoord)
	defer secondServer.Stop()

	firstHost, firstPort := parseHostPort(firstAddr)
	secondHost, secondPort := parseHostPort(secondAddr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: exec.ServiceStatusActive},
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: exec.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)

	_, err = stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`2`)})
	require.Error(t, err)

	firstCallCount := firstPuts.Load()
	secondCallCount := secondPuts.Load()
	assert.True(t, firstCallCount == 2 || secondCallCount == 2)
	assert.True(t, firstCallCount == 0 || secondCallCount == 0)
}

func TestClientStatePinnedCoordinatorRefreshesSameCoordinatorEndpoint(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	ref := &coordinatorv1.StateRef{Scope: "dag", Namespace: "daily-agent", Key: "cursor"}
	entry := &coordinatorv1.StateEntry{Ref: ref, Value: []byte(`1`), Version: 1}

	oldCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			return &coordinatorv1.PutStateResponse{Entry: entry}, nil
		},
	}
	oldServer, oldAddr := startMockServer(t, oldCoord)
	oldServerStopped := false
	defer func() {
		if !oldServerStopped {
			oldServer.Stop()
		}
	}()

	var newPuts atomic.Int32
	newCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			newPuts.Add(1)
			return &coordinatorv1.PutStateResponse{Entry: entry}, nil
		},
	}
	newServer, newAddr := startMockServer(t, newCoord)
	defer newServer.Stop()

	oldHost, oldPort := parseHostPort(oldAddr)
	newHost, newPort := parseHostPort(newAddr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-a", Host: oldHost, Port: oldPort, Status: exec.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)

	monitor.members = []exec.HostInfo{
		{ID: "coord-a", Host: newHost, Port: newPort, Status: exec.ServiceStatusActive},
	}
	oldServer.Stop()
	oldServerStopped = true

	_, err = stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`2`)})
	require.Error(t, err)

	_, err = stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`3`)})
	require.NoError(t, err)
	assert.Equal(t, int32(1), newPuts.Load())
}

func TestClientStatePinnedCoordinatorRefreshesSameCoordinatorEndpointAfterDeadlineExceeded(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	ref := &coordinatorv1.StateRef{Scope: "dag", Namespace: "daily-agent", Key: "cursor"}
	entry := &coordinatorv1.StateEntry{Ref: ref, Value: []byte(`1`), Version: 1}

	var oldPuts atomic.Int32
	oldCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			if oldPuts.Add(1) == 1 {
				return &coordinatorv1.PutStateResponse{Entry: entry}, nil
			}
			return nil, status.Error(codes.DeadlineExceeded, "stale endpoint")
		},
	}
	oldServer, oldAddr := startMockServer(t, oldCoord)
	defer oldServer.Stop()

	var newPuts atomic.Int32
	newCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			newPuts.Add(1)
			return &coordinatorv1.PutStateResponse{Entry: entry}, nil
		},
	}
	newServer, newAddr := startMockServer(t, newCoord)
	defer newServer.Stop()

	oldHost, oldPort := parseHostPort(oldAddr)
	newHost, newPort := parseHostPort(newAddr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-a", Host: oldHost, Port: oldPort, Status: exec.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)

	monitor.members = []exec.HostInfo{
		{ID: "coord-a", Host: newHost, Port: newPort, Status: exec.ServiceStatusActive},
	}

	_, err = stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`2`)})
	require.Error(t, err)

	_, err = stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`3`)})
	require.NoError(t, err)
	assert.Equal(t, int32(2), oldPuts.Load())
	assert.Equal(t, int32(1), newPuts.Load())
}

func TestClientStatePinnedCoordinatorReselectsWhenCoordinatorIDDisappears(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	ref := &coordinatorv1.StateRef{Scope: "dag", Namespace: "daily-agent", Key: "cursor"}
	entry := &coordinatorv1.StateEntry{Ref: ref, Value: []byte(`1`), Version: 1}

	var oldPuts atomic.Int32
	oldCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			if oldPuts.Add(1) == 1 {
				return &coordinatorv1.PutStateResponse{Entry: entry}, nil
			}
			return nil, status.Error(codes.Unavailable, "stale coordinator")
		},
	}
	oldServer, oldAddr := startMockServer(t, oldCoord)
	defer oldServer.Stop()

	var newPuts atomic.Int32
	newCoord := &mockCoordinatorService{
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			newPuts.Add(1)
			return &coordinatorv1.PutStateResponse{Entry: entry}, nil
		},
	}
	newServer, newAddr := startMockServer(t, newCoord)
	defer newServer.Stop()

	oldHost, oldPort := parseHostPort(oldAddr)
	newHost, newPort := parseHostPort(newAddr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{
			{ID: "coord-a", Host: oldHost, Port: oldPort, Status: exec.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)

	monitor.members = []exec.HostInfo{
		{ID: "coord-b", Host: newHost, Port: newPort, Status: exec.ServiceStatusActive},
	}

	_, err = stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`2`)})
	require.Error(t, err)

	_, err = stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`3`)})
	require.NoError(t, err)
	assert.Equal(t, int32(2), oldPuts.Load())
	assert.Equal(t, int32(1), newPuts.Load())
}

func TestClientHeartbeat(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	var receivedReq *coordinatorv1.HeartbeatRequest
	mockCoord := &mockCoordinatorService{
		heartbeatFunc: func(_ context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			receivedReq = req
			return &coordinatorv1.HeartbeatResponse{}, nil
		},
	}

	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{{Host: host, Port: port, Status: exec.ServiceStatusActive}},
	}

	client := coordinator.New(monitor, config)

	req := &coordinatorv1.HeartbeatRequest{
		WorkerId: "test-worker",
		Stats: &coordinatorv1.WorkerStats{
			TotalPollers: 5,
			BusyPollers:  2,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	resp, err := client.Heartbeat(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp, "HeartbeatResponse should not be nil")
	require.NotNil(t, receivedReq)
	assert.Equal(t, "test-worker", receivedReq.WorkerId)
	assert.NotNil(t, receivedReq.Stats)
	assert.Equal(t, int32(5), receivedReq.Stats.TotalPollers)
	assert.Equal(t, int32(2), receivedReq.Stats.BusyPollers)
}

func TestClientReportStatus(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		config := coordinator.DefaultConfig()
		config.RequestTimeout = 100 * time.Millisecond

		var receivedReq *coordinatorv1.ReportStatusRequest
		mockCoord := &mockCoordinatorService{
			reportStatusFunc: func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				receivedReq = req
				return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
			},
		}

		server, addr := startMockServer(t, mockCoord)
		defer server.Stop()

		host, port := parseHostPort(addr)
		monitor := &mockServiceMonitor{
			members: []exec.HostInfo{{Host: host, Port: port, Status: exec.ServiceStatusActive}},
		}

		client := coordinator.New(monitor, config)

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			DAGRunID:  "test-run-123",
			Status:    1, // Running status
			StartedAt: "2024-01-01T00:00:00Z",
		})
		require.NoError(t, convErr)

		req := &coordinatorv1.ReportStatusRequest{
			WorkerId: "test-worker",
			Status:   protoStatus,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		resp, err := client.ReportStatus(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.Accepted)

		require.NotNil(t, receivedReq)
		assert.Equal(t, "test-worker", receivedReq.WorkerId)
		require.NotNil(t, receivedReq.Status)
		// Verify via JSON conversion
		s, convErr := convert.ProtoToDAGRunStatus(receivedReq.Status)
		require.NoError(t, convErr)
		require.NotNil(t, s)
		assert.Equal(t, "test-run-123", s.DAGRunID)
	})

	t.Run("NotAccepted", func(t *testing.T) {
		t.Parallel()

		config := coordinator.DefaultConfig()
		config.RequestTimeout = 100 * time.Millisecond

		mockCoord := &mockCoordinatorService{
			reportStatusFunc: func(_ context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				return &coordinatorv1.ReportStatusResponse{Accepted: false}, nil
			},
		}

		server, addr := startMockServer(t, mockCoord)
		defer server.Stop()

		host, port := parseHostPort(addr)
		monitor := &mockServiceMonitor{
			members: []exec.HostInfo{{Host: host, Port: port, Status: exec.ServiceStatusActive}},
		}

		client := coordinator.New(monitor, config)

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			DAGRunID: "test-run-456",
			Status:   2, // Success status
		})
		require.NoError(t, convErr)

		req := &coordinatorv1.ReportStatusRequest{
			WorkerId: "test-worker",
			Status:   protoStatus,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		resp, err := client.ReportStatus(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.False(t, resp.Accepted)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()

		config := coordinator.DefaultConfig()
		config.MaxRetries = 0
		config.RequestTimeout = 100 * time.Millisecond

		mockCoord := &mockCoordinatorService{
			reportStatusFunc: func(_ context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				return nil, status.Error(codes.Internal, "internal error")
			},
		}

		server, addr := startMockServer(t, mockCoord)
		defer server.Stop()

		host, port := parseHostPort(addr)
		monitor := &mockServiceMonitor{
			members: []exec.HostInfo{{Host: host, Port: port, Status: exec.ServiceStatusActive}},
		}

		client := coordinator.New(monitor, config)

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			DAGRunID: "test-run-789",
			Status:   3, // Failed status
		})
		require.NoError(t, convErr)

		req := &coordinatorv1.ReportStatusRequest{
			WorkerId: "test-worker",
			Status:   protoStatus,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		resp, err := client.ReportStatus(ctx, req)
		require.Error(t, err)
		assert.Nil(t, resp)
	})
}

func TestClientMetrics(t *testing.T) {
	t.Parallel()

	// Test metrics tracking during failures
	config := coordinator.DefaultConfig()
	config.MaxRetries = 0 // No retries
	config.RequestTimeout = 500 * time.Millisecond

	// Create a failing coordinator
	mockCoord := &mockCoordinatorService{
		dispatchFunc: func(_ context.Context, _ *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}

	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{{Host: host, Port: port, Status: exec.ServiceStatusActive}},
	}

	client := coordinator.New(monitor, config)

	// Initial state
	metrics := client.Metrics()
	assert.True(t, metrics.IsConnected)
	assert.Equal(t, 0, metrics.ConsecutiveFails)

	task := &coordinatorv1.Task{DagRunId: "test"}

	// Attempt dispatch - should fail
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := client.Dispatch(ctx, task)
	require.Error(t, err)

	// Check failure metrics
	metrics = client.Metrics()
	assert.False(t, metrics.IsConnected)
	assert.Greater(t, metrics.ConsecutiveFails, 0)
	assert.Greater(t, metrics.FailCount, 0)
}

func TestClientCleanup(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = 100 * time.Millisecond

	mockCoord := &mockCoordinatorService{
		dispatchFunc: func(_ context.Context, _ *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
			return &coordinatorv1.DispatchResponse{}, nil
		},
	}

	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []exec.HostInfo{{Host: host, Port: port, Status: exec.ServiceStatusActive}},
	}

	client := coordinator.New(monitor, config)

	// Make a call to establish connection
	task := &coordinatorv1.Task{DagRunId: "test"}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := client.Dispatch(ctx, task)
	require.NoError(t, err)

	// Cleanup should close all connections
	err = client.Cleanup(ctx)
	require.NoError(t, err)

	// Future calls should still work (will create new connections)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel2()

	err = client.Dispatch(ctx2, task)
	require.NoError(t, err)
}

func TestClientDispatch_NoCoordinators(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	monitor := &mockServiceMonitor{}
	client := coordinator.New(monitor, config)

	task := &coordinatorv1.Task{
		DagRunId: "test-dag-run",
		Target:   "test.yaml",
	}

	// Should fail gracefully with no coordinators
	monitor.members = []exec.HostInfo{}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := client.Dispatch(ctx, task)
	require.Error(t, err)
	// Could be either error depending on timing
	assert.True(t, strings.Contains(err.Error(), "no coordinators available") ||
		strings.Contains(err.Error(), "context deadline exceeded"))
}

// Mock implementations

var _ exec.ServiceRegistry = (*mockServiceMonitor)(nil)

type mockServiceMonitor struct {
	members   []exec.HostInfo
	err       error
	onMembers func()
}

func (m *mockServiceMonitor) Register(_ context.Context, _ exec.ServiceName, _ exec.HostInfo) error {
	return nil
}

func (m *mockServiceMonitor) GetServiceMembers(_ context.Context, _ exec.ServiceName) ([]exec.HostInfo, error) {
	if m.onMembers != nil {
		m.onMembers()
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.members, nil
}

func (m *mockServiceMonitor) Unregister(_ context.Context) {
	// No-op
}

func (m *mockServiceMonitor) UpdateStatus(_ context.Context, _ exec.ServiceName, _ exec.ServiceStatus) error {
	return nil
}

var _ coordinatorv1.CoordinatorServiceServer = (*mockCoordinatorService)(nil)

type mockCoordinatorService struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer

	dispatchFunc     func(context.Context, *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error)
	pollFunc         func(context.Context, *coordinatorv1.PollRequest) (*coordinatorv1.PollResponse, error)
	getWorkersFunc   func(context.Context, *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error)
	heartbeatFunc    func(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error)
	reportStatusFunc func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)
	getStateFunc     func(context.Context, *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error)
	putStateFunc     func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error)
	deleteStateFunc  func(context.Context, *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error)
	listStateFunc    func(context.Context, *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error)
}

func (m *mockCoordinatorService) Dispatch(ctx context.Context, req *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
	if m.dispatchFunc != nil {
		return m.dispatchFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) Poll(ctx context.Context, req *coordinatorv1.PollRequest) (*coordinatorv1.PollResponse, error) {
	if m.pollFunc != nil {
		return m.pollFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) GetWorkers(ctx context.Context, req *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
	if m.getWorkersFunc != nil {
		return m.getWorkersFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) Heartbeat(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	if m.heartbeatFunc != nil {
		return m.heartbeatFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	if m.reportStatusFunc != nil {
		return m.reportStatusFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) GetState(ctx context.Context, req *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error) {
	if m.getStateFunc != nil {
		return m.getStateFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) PutState(ctx context.Context, req *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
	if m.putStateFunc != nil {
		return m.putStateFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) DeleteState(ctx context.Context, req *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
	if m.deleteStateFunc != nil {
		return m.deleteStateFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) ListState(ctx context.Context, req *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
	if m.listStateFunc != nil {
		return m.listStateFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// Helper to start a mock gRPC server
func startMockServer(t *testing.T, service coordinatorv1.CoordinatorServiceServer) (*grpc.Server, string) {
	t.Helper()

	server := grpc.NewServer()
	coordinatorv1.RegisterCoordinatorServiceServer(server, service)

	// Register health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Start server on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = server.Serve(listener)
	}()

	return server, listener.Addr().String()
}
