// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
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

func TestClientRepairsCorruptWorkspaceBundle(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := []byte("valid workspace bundle")
	desc := workspacebundle.Descriptor{Digest: workspacebundle.Digest(data), Size: int64(len(data))}
	dir := t.TempDir()
	store := workspacebundle.NewStore(dir, workspacebundle.DefaultLimits())
	require.NoError(t, store.Put(ctx, desc, data))
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	require.NoError(t, os.WriteFile(paths[0], []byte("corrupt"), 0o600))

	server, addr := startMockServer(t, coordinator.NewHandler(coordinator.HandlerConfig{WorkspaceBundleDir: dir}))
	t.Cleanup(server.Stop)
	host, port := parseHostPort(addr)
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{{
		ID: "coord-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive,
	}}}, coordinator.DefaultConfig())
	bundleClient, ok := client.(workspacebundle.Client)
	require.True(t, ok)

	require.NoError(t, bundleClient.PutWorkspaceBundle(ctx, desc, data))
	actual, err := bundleClient.GetWorkspaceBundle(ctx, desc.Digest)
	require.NoError(t, err)
	assert.Equal(t, data, actual)
}

func TestClientDispatch(t *testing.T) {
	t.Parallel()

	t.Run("UploadsDeclaredFileDependencies", func(t *testing.T) {
		t.Parallel()

		dagRoot := t.TempDir()
		dependencyRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dependencyRoot, "input.txt"), []byte("input"), 0o644))
		dagFile := filepath.Join(dagRoot, "dag.yaml")
		definition := fmt.Sprintf("name: test\nparams:\n  WORKSPACE: %q\nworking_dir: ${params.WORKSPACE}\nsteps:\n  - name: consume\n    run: cat input.txt\n    dependencies: input.txt\n", dagRoot)
		workspaceBundleDir := filepath.Join(t.TempDir(), "workspace-bundles")
		stagingDir := filepath.Join(workspaceBundleDir, "staging")
		dest := filepath.Join(t.TempDir(), "workspace")

		var uploaded []byte
		mockCoord := &mockCoordinatorService{
			hasWorkspaceBundleFunc: func(_ context.Context, _ *coordinatorv1.HasWorkspaceBundleRequest) (*coordinatorv1.HasWorkspaceBundleResponse, error) {
				return &coordinatorv1.HasWorkspaceBundleResponse{}, nil
			},
			putWorkspaceBundleFunc: func(stream coordinatorv1.CoordinatorService_PutWorkspaceBundleServer) error {
				entries, err := os.ReadDir(stagingDir)
				if err != nil {
					return fmt.Errorf("read workspace bundle staging directory: %w", err)
				}
				if len(entries) != 1 {
					return fmt.Errorf("workspace bundle staging directory contains %d entries, want 1", len(entries))
				}
				for {
					chunk, err := stream.Recv()
					if err == io.EOF {
						return stream.SendAndClose(&coordinatorv1.PutWorkspaceBundleResponse{Accepted: true})
					}
					if err != nil {
						return fmt.Errorf("receive workspace bundle chunk: %w", err)
					}
					uploaded = append(uploaded, chunk.Data...)
				}
			},
			dispatchFunc: func(_ context.Context, req *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
				if req.Task.WorkspaceBundleDigest == "" {
					return nil, fmt.Errorf("workspace bundle digest is empty")
				}
				if req.Task.WorkspaceBundleSize != int64(len(uploaded)) {
					return nil, fmt.Errorf("workspace bundle size is %d, want %d", req.Task.WorkspaceBundleSize, len(uploaded))
				}
				if req.Task.WorkspaceBundleDagPath == "" {
					return nil, fmt.Errorf("workspace bundle DAG path is empty")
				}
				if err := workspacebundle.Extract(uploaded, dest, workspacebundle.Descriptor{
					Digest:  req.Task.WorkspaceBundleDigest,
					Size:    req.Task.WorkspaceBundleSize,
					DAGPath: req.Task.WorkspaceBundleDagPath,
				}, workspacebundle.DefaultLimits()); err != nil {
					return nil, fmt.Errorf("extract uploaded workspace bundle: %w", err)
				}
				actualDAG, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(req.Task.WorkspaceBundleDagPath)))
				if err != nil {
					return nil, fmt.Errorf("read uploaded DAG: %w", err)
				}
				if string(actualDAG) != definition {
					return nil, fmt.Errorf("uploaded DAG does not match its definition")
				}
				return &coordinatorv1.DispatchResponse{}, nil
			},
		}

		server, addr := startMockServer(t, mockCoord)
		defer server.Stop()
		host, port := parseHostPort(addr)
		config := coordinator.DefaultConfig()
		config.MaxRetries = 0
		config.WorkspaceBundleDir = workspaceBundleDir
		client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{{
			ID: "coord-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive,
		}}}, config)

		err := client.Dispatch(context.Background(), dispatch.DispatchRequest{Task: &dispatch.DispatchTask{
			Operation:  dispatch.DispatchOperationRetry,
			DAGRunID:   "run-1",
			Target:     "test",
			Definition: definition,
			SourceFile: dagFile,
			PreviousStatus: &ir.DAGRunStatus{
				ParamsList: []string{"WORKSPACE=" + dependencyRoot},
			},
		}})
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(dest, "input.txt"))
		entries, err := os.ReadDir(stagingDir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

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
			members: []serviceregistry.HostInfo{
				{ID: "coord-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive},
			},
		}

		client := coordinator.New(monitor, config)

		task := &dispatch.DispatchTask{
			DAGRunID: "test-dag-run",
			Target:   "test.yaml",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		err := client.Dispatch(ctx, dispatch.DispatchRequest{Task: task})
		require.NoError(t, err)
	})

	t.Run("SendsAdmissionReservationToken", func(t *testing.T) {
		t.Parallel()
		config := coordinator.DefaultConfig()
		config.MaxRetries = 0
		config.RequestTimeout = 100 * time.Millisecond

		mockCoord := &mockCoordinatorService{
			dispatchFunc: func(_ context.Context, req *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
				assert.Equal(t, "reservation-token-a", req.AdmissionReservationToken)
				return &coordinatorv1.DispatchResponse{}, nil
			},
		}

		server, addr := startMockServer(t, mockCoord)
		defer server.Stop()

		host, port := parseHostPort(addr)
		monitor := &mockServiceMonitor{
			members: []serviceregistry.HostInfo{
				{ID: "coord-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive},
			},
		}

		client := coordinator.New(monitor, config)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		err := client.Dispatch(ctx, dispatch.DispatchRequest{
			Task: &dispatch.DispatchTask{
				DAGRunID: "test-dag-run",
				Target:   "test.yaml",
			},
			AdmissionReservationToken: "reservation-token-a",
		})
		require.NoError(t, err)
	})

	t.Run("NoCoordinators", func(t *testing.T) {
		t.Parallel()

		config := coordinator.DefaultConfig()
		config.MaxRetries = 0
		config.RequestTimeout = 100 * time.Millisecond

		monitor := &mockServiceMonitor{
			members: []serviceregistry.HostInfo{}, // No coordinators
		}

		client := coordinator.New(monitor, config)

		task := &dispatch.DispatchTask{
			DAGRunID: "test-dag-run",
			Target:   "test.yaml",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		err := client.Dispatch(ctx, dispatch.DispatchRequest{Task: task})
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
				return nil, status.Error(codes.FailedPrecondition, (&queue.StaleQueueDispatchError{
					Reason: "queued attempt was superseded",
				}).Error())
			},
		}

		server, addr := startMockServer(t, mockCoord)
		defer server.Stop()

		host, port := parseHostPort(addr)
		monitor := &mockServiceMonitor{
			members: []serviceregistry.HostInfo{
				{ID: "coord-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive},
			},
		}

		client := coordinator.New(monitor, config)

		err := client.Dispatch(context.Background(), dispatch.DispatchRequest{
			Task: &dispatch.DispatchTask{
				DAGRunID: "run-123",
				Target:   "test-dag",
			},
		})
		require.Error(t, err)
		require.ErrorIs(t, err, backoff.ErrPermanent)

		var staleErr *queue.StaleQueueDispatchError
		require.ErrorAs(t, err, &staleErr)
		require.Equal(t, "queued attempt was superseded", staleErr.Reason)
	})
}

func TestClientDispatchRejectsFileDependenciesWithoutWorkspaceBundleDir(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "input.txt"), []byte("input"), 0o644))
	dagFile := filepath.Join(root, "dag.yaml")
	definition := "name: test\nsteps:\n  - name: consume\n    run: cat input.txt\n    dependencies: input.txt\n"

	client := coordinator.New(&mockServiceMonitor{}, coordinator.DefaultConfig())
	err := client.Dispatch(context.Background(), dispatch.DispatchRequest{Task: &dispatch.DispatchTask{
		DAGRunID:   "run-1",
		Target:     "test",
		Definition: definition,
		SourceFile: dagFile,
	}})
	require.ErrorContains(t, err, "workspace bundle directory is not configured")
	assert.NoDirExists(t, filepath.Join(root, "staging"))
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
		members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
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
		members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
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
		members: []serviceregistry.HostInfo{
			{ID: "coord-2", Host: host2, Port: port2, Status: serviceregistry.ServiceStatusActive},
			{ID: "coord-1", Host: host1, Port: port1, Status: serviceregistry.ServiceStatusActive},
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

func TestClientGetWorkers_TieBreakIsIndependentOfDiscoveryOrder(t *testing.T) {
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
	memberA := serviceregistry.HostInfo{ID: "coord-a", Host: hostA, Port: portA, Status: serviceregistry.ServiceStatusActive}
	memberB := serviceregistry.HostInfo{ID: "coord-b", Host: hostB, Port: portB, Status: serviceregistry.ServiceStatusActive}

	getWorker := func(members ...serviceregistry.HostInfo) *coordinatorv1.WorkerInfo {
		client := coordinator.New(&mockServiceMonitor{members: members}, config)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		workers, err := client.GetWorkers(ctx)
		require.NoError(t, err)
		require.Len(t, workers, 1)
		return workers[0]
	}

	forward := getWorker(memberA, memberB)
	reverse := getWorker(memberB, memberA)

	assert.Equal(t, forward.Labels, reverse.Labels)
	assert.Equal(t, forward.BusyPollers, reverse.BusyPollers)
	require.True(t,
		(forward.Labels["source"] == "coord-a" && forward.BusyPollers == 1) ||
			(forward.Labels["source"] == "coord-b" && forward.BusyPollers == 2),
		"selected worker fields must come from one coordinator report",
	)
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
		members: []serviceregistry.HostInfo{
			{ID: "coord-fail", Host: failingHost, Port: failingPort, Status: serviceregistry.ServiceStatusActive},
			{ID: "coord-ok", Host: successHost, Port: successPort, Status: serviceregistry.ServiceStatusActive},
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
			members: []serviceregistry.HostInfo{
				{ID: "coord-b", Host: secondHost, Port: secondPort, Status: serviceregistry.ServiceStatusActive},
				{ID: "coord-a", Host: firstHost, Port: firstPort, Status: serviceregistry.ServiceStatusActive},
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
			members: []serviceregistry.HostInfo{
				{ID: "coord-b", Host: secondHost, Port: secondPort, Status: serviceregistry.ServiceStatusActive},
				{ID: "coord-a", Host: firstHost, Port: firstPort, Status: serviceregistry.ServiceStatusActive},
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
	config.RequestTimeout = time.Second

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
		members: []serviceregistry.HostInfo{
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: serviceregistry.ServiceStatusActive},
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: serviceregistry.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx := t.Context()

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
		members: []serviceregistry.HostInfo{
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: serviceregistry.ServiceStatusActive},
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: serviceregistry.ServiceStatusActive},
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
		members: []serviceregistry.HostInfo{
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: serviceregistry.ServiceStatusActive},
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: serviceregistry.ServiceStatusActive},
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
	config.RequestTimeout = time.Second

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
		members: []serviceregistry.HostInfo{
			{ID: "coord-a", Host: oldHost, Port: oldPort, Status: serviceregistry.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)

	monitor.members = []serviceregistry.HostInfo{
		{ID: "coord-a", Host: newHost, Port: newPort, Status: serviceregistry.ServiceStatusActive},
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
	config.RequestTimeout = time.Second

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
		members: []serviceregistry.HostInfo{
			{ID: "coord-a", Host: oldHost, Port: oldPort, Status: serviceregistry.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)

	monitor.members = []serviceregistry.HostInfo{
		{ID: "coord-a", Host: newHost, Port: newPort, Status: serviceregistry.ServiceStatusActive},
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
	config.RequestTimeout = time.Second

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
		members: []serviceregistry.HostInfo{
			{ID: "coord-a", Host: oldHost, Port: oldPort, Status: serviceregistry.ServiceStatusActive},
		},
	}

	stateClient, ok := coordinator.New(monitor, config).(coordinator.StateClient)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := stateClient.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)

	monitor.members = []serviceregistry.HostInfo{
		{ID: "coord-b", Host: newHost, Port: newPort, Status: serviceregistry.ServiceStatusActive},
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
		members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
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

func TestClientHeartbeatWithSkipTLSVerify(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.Insecure = false
	config.SkipTLSVerify = true
	config.MaxRetries = 0
	require.NoError(t, config.Validate())

	mockCoord := &mockCoordinatorService{
		heartbeatFunc: func(_ context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			return &coordinatorv1.HeartbeatResponse{}, nil
		},
	}

	server, addr := startMockTLSServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := coordinator.New(monitor, config)
	defer func() {
		require.NoError(t, client.Cleanup(ctx))
	}()

	_, err := client.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"})
	require.NoError(t, err)
}

func TestClientTreatsDiscoveredEndpointsAsDistinctOwners(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.MaxRetries = 0

	var oldHeartbeats atomic.Int32
	var oldReports atomic.Int32
	var oldPuts atomic.Int32
	oldCoord := &mockCoordinatorService{
		heartbeatFunc: func(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			oldHeartbeats.Add(1)
			return &coordinatorv1.HeartbeatResponse{}, nil
		},
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			oldReports.Add(1)
			return &coordinatorv1.ReportStatusResponse{}, nil
		},
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			oldPuts.Add(1)
			return &coordinatorv1.PutStateResponse{}, nil
		},
	}
	oldServer, oldAddr := startMockServer(t, oldCoord)
	defer oldServer.Stop()

	var newHeartbeats atomic.Int32
	var newReports atomic.Int32
	var newPuts atomic.Int32
	newCoord := &mockCoordinatorService{
		heartbeatFunc: func(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			newHeartbeats.Add(1)
			return &coordinatorv1.HeartbeatResponse{}, nil
		},
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			newReports.Add(1)
			return &coordinatorv1.ReportStatusResponse{}, nil
		},
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			newPuts.Add(1)
			return &coordinatorv1.PutStateResponse{}, nil
		},
	}
	newServer, newAddr := startMockServer(t, newCoord)
	defer newServer.Stop()

	oldHost, oldPort := parseHostPort(oldAddr)
	newHost, newPort := parseHostPort(newAddr)
	oldStartedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldMember := serviceregistry.HostInfo{ID: "coord-a", Host: oldHost, Port: oldPort, Status: serviceregistry.ServiceStatusActive, StartedAt: oldStartedAt}
	newMember := serviceregistry.HostInfo{ID: "coord-a", Host: newHost, Port: newPort, Status: serviceregistry.ServiceStatusActive, StartedAt: oldStartedAt.Add(time.Minute)}
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{oldMember},
	}
	client := coordinator.New(monitor, config)

	request := &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"}
	_, err := client.Heartbeat(context.Background(), request)
	require.NoError(t, err)

	stateClient, ok := client.(coordinator.StateClient)
	require.True(t, ok)
	_, err = stateClient.PutState(context.Background(), &coordinatorv1.PutStateRequest{
		Ref: &coordinatorv1.StateRef{Scope: "dag", Namespace: "test", Key: "state"},
	})
	require.NoError(t, err)

	monitor.members = []serviceregistry.HostInfo{newMember}
	_, err = stateClient.PutState(context.Background(), &coordinatorv1.PutStateRequest{
		Ref: &coordinatorv1.StateRef{Scope: "dag", Namespace: "new", Key: "state"},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), oldPuts.Load())
	assert.Equal(t, int32(1), newPuts.Load())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	_, err = client.Heartbeat(ctx, request)
	cancel()
	require.NoError(t, err)
	assert.Equal(t, int32(1), newHeartbeats.Load())

	_, err = stateClient.PutState(context.Background(), &coordinatorv1.PutStateRequest{
		Ref: &coordinatorv1.StateRef{Scope: "dag", Namespace: "test", Key: "state"},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), oldPuts.Load())
	assert.Equal(t, int32(1), newPuts.Load())

	monitor.members = []serviceregistry.HostInfo{oldMember}
	_, err = client.Heartbeat(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, int32(2), oldHeartbeats.Load())
	assert.Equal(t, int32(1), newHeartbeats.Load())

	ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
	_, err = client.ReportStatusTo(ctx, oldMember, &coordinatorv1.ReportStatusRequest{})
	cancel()
	require.NoError(t, err)
	assert.Equal(t, int32(1), oldReports.Load())
	assert.Zero(t, newReports.Load())
}

func TestClientHeartbeatFailsOverWithinConfiguredTimeout(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.MaxRetries = 0
	config.HeartbeatTimeout = time.Second

	var heartbeatCalls atomic.Int32
	heartbeatFunc := func(ctx context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
		if heartbeatCalls.Add(1) == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &coordinatorv1.HeartbeatResponse{}, nil
	}

	firstServer, firstAddr := startMockServer(t, &mockCoordinatorService{
		heartbeatFunc: heartbeatFunc,
	})
	defer firstServer.Stop()
	secondServer, secondAddr := startMockServer(t, &mockCoordinatorService{
		heartbeatFunc: heartbeatFunc,
	})
	defer secondServer.Stop()

	firstHost, firstPort := parseHostPort(firstAddr)
	secondHost, secondPort := parseHostPort(secondAddr)
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: serviceregistry.ServiceStatusActive},
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: serviceregistry.ServiceStatusActive},
		},
	}
	client := coordinator.New(monitor, config)

	resp, err := client.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(2), heartbeatCalls.Load())
}

func TestClientOwnerCallFailsOverAndCachesSuccessfulRoute(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.RequestTimeout = time.Second

	var ownerCalls atomic.Int32
	ownerServer, ownerAddr := startMockServer(t, &mockCoordinatorService{
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			ownerCalls.Add(1)
			return nil, status.Error(codes.Unavailable, "owner unavailable")
		},
	})
	defer ownerServer.Stop()

	var replacementCalls atomic.Int32
	replacementServer, replacementAddr := startMockServer(t, &mockCoordinatorService{
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			replacementCalls.Add(1)
			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		},
	})
	defer replacementServer.Stop()

	ownerHost, ownerPort := parseHostPort(ownerAddr)
	replacementHost, replacementPort := parseHostPort(replacementAddr)
	owner := serviceregistry.HostInfo{ID: "coord-a", Host: ownerHost, Port: ownerPort, Status: serviceregistry.ServiceStatusActive}
	replacement := serviceregistry.HostInfo{ID: "coord-b", Host: replacementHost, Port: replacementPort, Status: serviceregistry.ServiceStatusActive}
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{owner, replacement}}, config)

	for range 2 {
		resp, err := client.ReportStatusTo(t.Context(), owner, &coordinatorv1.ReportStatusRequest{})
		require.NoError(t, err)
		require.True(t, resp.Accepted)
	}

	assert.Equal(t, int32(1), ownerCalls.Load())
	assert.Equal(t, int32(2), replacementCalls.Load())
}

func TestClientOwnerCallDoesNotMaskApplicationRejection(t *testing.T) {
	t.Parallel()

	var replacementCalls atomic.Int32
	ownerServer, ownerAddr := startMockServer(t, &mockCoordinatorService{
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "stale attempt")
		},
	})
	defer ownerServer.Stop()
	replacementServer, replacementAddr := startMockServer(t, &mockCoordinatorService{
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			replacementCalls.Add(1)
			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		},
	})
	defer replacementServer.Stop()

	ownerHost, ownerPort := parseHostPort(ownerAddr)
	replacementHost, replacementPort := parseHostPort(replacementAddr)
	owner := serviceregistry.HostInfo{ID: "coord-a", Host: ownerHost, Port: ownerPort, Status: serviceregistry.ServiceStatusActive}
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{
		owner,
		{ID: "coord-b", Host: replacementHost, Port: replacementPort, Status: serviceregistry.ServiceStatusActive},
	}}, coordinator.DefaultConfig())

	_, err := client.ReportStatusTo(t.Context(), owner, &coordinatorv1.ReportStatusRequest{})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Zero(t, replacementCalls.Load())
}

func TestClientOwnerCallRetriesLegacyNonOwnerRejection(t *testing.T) {
	t.Parallel()

	ownerServer, ownerAddr := startMockServer(t, &mockCoordinatorService{
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "status update sent to non-owner coordinator")
		},
	})
	defer ownerServer.Stop()
	replacementServer, replacementAddr := startMockServer(t, &mockCoordinatorService{
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		},
	})
	defer replacementServer.Stop()

	ownerHost, ownerPort := parseHostPort(ownerAddr)
	replacementHost, replacementPort := parseHostPort(replacementAddr)
	owner := serviceregistry.HostInfo{ID: "coord-a", Host: ownerHost, Port: ownerPort, Status: serviceregistry.ServiceStatusActive}
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{
		owner,
		{ID: "coord-b", Host: replacementHost, Port: replacementPort, Status: serviceregistry.ServiceStatusActive},
	}}, coordinator.DefaultConfig())

	resp, err := client.ReportStatusTo(t.Context(), owner, &coordinatorv1.ReportStatusRequest{})
	require.NoError(t, err)
	require.True(t, resp.Accepted)
}

func TestClientOwnerCallRetriesLegacyClaimEndpointRejection(t *testing.T) {
	t.Parallel()

	ownerServer, ownerAddr := startMockServer(t, &mockCoordinatorService{
		ackTaskClaimFunc: func(context.Context, *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
			return &coordinatorv1.AckTaskClaimResponse{
				Error: "claim belongs to a different coordinator endpoint",
			}, nil
		},
	})
	defer ownerServer.Stop()
	replacementServer, replacementAddr := startMockServer(t, &mockCoordinatorService{
		ackTaskClaimFunc: func(context.Context, *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
			return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
		},
	})
	defer replacementServer.Stop()

	ownerHost, ownerPort := parseHostPort(ownerAddr)
	replacementHost, replacementPort := parseHostPort(replacementAddr)
	owner := serviceregistry.HostInfo{ID: "coord-a", Host: ownerHost, Port: ownerPort, Status: serviceregistry.ServiceStatusActive}
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{
		owner,
		{ID: "coord-b", Host: replacementHost, Port: replacementPort, Status: serviceregistry.ServiceStatusActive},
	}}, coordinator.DefaultConfig())

	resp, err := client.AckTaskClaimTo(t.Context(), owner, &coordinatorv1.AckTaskClaimRequest{})
	require.NoError(t, err)
	require.True(t, resp.Accepted)
}

func TestClientOwnerStreamMovesAfterLegacyNonOwnerRejection(t *testing.T) {
	t.Parallel()

	ownerServer, ownerAddr := startMockServer(t, &mockCoordinatorService{
		streamLogsFunc: func(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
			_, err := stream.Recv()
			if err != nil {
				return err
			}
			return status.Error(codes.FailedPrecondition, "log chunk sent to non-owner coordinator")
		},
	})
	defer ownerServer.Stop()

	received := make(chan string, 1)
	replacementServer, replacementAddr := startMockServer(t, &mockCoordinatorService{
		streamLogsFunc: func(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
			chunk, err := stream.Recv()
			if err != nil {
				return err
			}
			received <- string(chunk.Data)
			for {
				_, err = stream.Recv()
				if err == io.EOF {
					return stream.SendAndClose(&coordinatorv1.StreamLogsResponse{})
				}
				if err != nil {
					return err
				}
			}
		},
	})
	defer replacementServer.Stop()

	ownerHost, ownerPort := parseHostPort(ownerAddr)
	replacementHost, replacementPort := parseHostPort(replacementAddr)
	owner := serviceregistry.HostInfo{ID: "coord-a", Host: ownerHost, Port: ownerPort, Status: serviceregistry.ServiceStatusActive}
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{
		owner,
		{ID: "coord-b", Host: replacementHost, Port: replacementPort, Status: serviceregistry.ServiceStatusActive},
	}}, coordinator.DefaultConfig())

	stream, err := client.StreamLogsTo(t.Context(), owner)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&coordinatorv1.LogChunk{Data: []byte("old")}))
	_, err = stream.CloseAndRecv()
	require.Equal(t, codes.Unavailable, status.Code(err))

	stream, err = client.StreamLogsTo(t.Context(), owner)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&coordinatorv1.LogChunk{Data: []byte("replacement")}))
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)
	assert.Equal(t, "replacement", <-received)
}

func TestClientOwnerStreamSkipsUnhealthyOwner(t *testing.T) {
	t.Parallel()

	unhealthy := health.NewServer()
	unhealthy.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	ownerServer, ownerAddr := startMockServerWithHealth(t, &mockCoordinatorService{}, unhealthy)
	defer ownerServer.Stop()

	received := make(chan string, 1)
	replacementServer, replacementAddr := startMockServer(t, &mockCoordinatorService{
		streamLogsFunc: func(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
			chunk, err := stream.Recv()
			if err != nil {
				return err
			}
			received <- string(chunk.Data)
			for {
				_, err = stream.Recv()
				if err == io.EOF {
					return stream.SendAndClose(&coordinatorv1.StreamLogsResponse{})
				}
				if err != nil {
					return err
				}
			}
		},
	})
	defer replacementServer.Stop()

	ownerHost, ownerPort := parseHostPort(ownerAddr)
	replacementHost, replacementPort := parseHostPort(replacementAddr)
	owner := serviceregistry.HostInfo{ID: "coord-a", Host: ownerHost, Port: ownerPort, Status: serviceregistry.ServiceStatusActive}
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{
		owner,
		{ID: "coord-b", Host: replacementHost, Port: replacementPort, Status: serviceregistry.ServiceStatusActive},
	}}, coordinator.DefaultConfig())

	stream, err := client.StreamLogsTo(t.Context(), owner)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&coordinatorv1.LogChunk{Data: []byte("replacement")}))
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)
	assert.Equal(t, "replacement", <-received)
}

func TestClientHeartbeatFailsOverAfterHealthCheckStalls(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.MaxRetries = 0
	config.HeartbeatTimeout = time.Second

	healthServer := &stallFirstHealthCheckServer{}
	var heartbeatCalls atomic.Int32
	mockCoord := &mockCoordinatorService{
		heartbeatFunc: func(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			heartbeatCalls.Add(1)
			return &coordinatorv1.HeartbeatResponse{}, nil
		},
	}
	firstServer, firstAddr := startMockServerWithHealth(t, mockCoord, healthServer)
	defer firstServer.Stop()
	secondServer, secondAddr := startMockServerWithHealth(t, mockCoord, healthServer)
	defer secondServer.Stop()

	firstHost, firstPort := parseHostPort(firstAddr)
	secondHost, secondPort := parseHostPort(secondAddr)
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: serviceregistry.ServiceStatusActive},
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: serviceregistry.ServiceStatusActive},
		},
	}
	client := coordinator.New(monitor, config)

	resp, err := client.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(2), healthServer.calls.Load())
	assert.Equal(t, int32(1), heartbeatCalls.Load())
}

func TestClientRPCFailuresPreserveActiveLogStream(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()

	received := make(chan string, 2)
	mockCoord := &mockCoordinatorService{
		heartbeatFunc: func(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			return nil, status.Error(codes.DeadlineExceeded, "heartbeat deadline exceeded")
		},
		reportStatusFunc: func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			return nil, status.Error(codes.Unavailable, "report unavailable")
		},
		getWorkersFunc: func(context.Context, *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return nil, status.Error(codes.Unavailable, "workers unavailable")
		},
		putStateFunc: func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
			return nil, status.Error(codes.DeadlineExceeded, "state deadline exceeded")
		},
		streamLogsFunc: func(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
			var chunksReceived uint64
			for {
				chunk, err := stream.Recv()
				if err == io.EOF {
					return stream.SendAndClose(&coordinatorv1.StreamLogsResponse{
						ChunksReceived: chunksReceived,
					})
				}
				if err != nil {
					return err
				}
				chunksReceived++
				received <- string(chunk.Data)
			}
		},
	}
	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{{ID: "coord-a", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
	}
	client := coordinator.New(monitor, config)

	stream, err := client.StreamLogsTo(t.Context(), monitor.members[0])
	require.NoError(t, err)

	require.NoError(t, stream.Send(&coordinatorv1.LogChunk{Data: []byte("before")}))
	select {
	case chunk := <-received:
		require.Equal(t, "before", chunk)
	case <-time.After(time.Second):
		t.Fatal("coordinator did not receive the first log chunk")
	}

	_, err = client.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))

	_, err = client.ReportStatusTo(
		context.Background(),
		monitor.members[0],
		&coordinatorv1.ReportStatusRequest{},
	)
	require.Equal(t, codes.Unavailable, status.Code(err))

	_, err = client.GetWorkers(context.Background())
	require.Error(t, err)

	stateClient, ok := client.(coordinator.StateClient)
	require.True(t, ok)
	_, err = stateClient.PutState(context.Background(), &coordinatorv1.PutStateRequest{})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))

	require.NoError(t, stream.Send(&coordinatorv1.LogChunk{Data: []byte("after")}))
	select {
	case chunk := <-received:
		require.Equal(t, "after", chunk)
	case <-time.After(time.Second):
		t.Fatal("coordinator did not receive the second log chunk")
	}

	resp, err := stream.CloseAndRecv()
	require.NoError(t, err)
	assert.Equal(t, uint64(2), resp.ChunksReceived)
}

func TestClientHeartbeatUsesConfiguredTimeout(t *testing.T) {
	config := coordinator.DefaultConfig()
	config.HeartbeatTimeout = 200 * time.Millisecond

	mockCoord := &mockCoordinatorService{
		heartbeatFunc: func(ctx context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{{ID: "coord-a", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
	}
	client := coordinator.New(monitor, config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"})
		result <- err
	}()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("Heartbeat did not respect its configured timeout")
	}
}

func TestClientHeartbeatHonorsCallerDeadline(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.HeartbeatTimeout = time.Second

	monitor := &mockServiceMonitor{
		getMembers: func(ctx context.Context) ([]serviceregistry.HostInfo, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	client := coordinator.New(monitor, config)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"})
		result <- err
	}()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Heartbeat did not respect the caller deadline")
	}
}

func TestClientHeartbeatReturnsDeadlineAfterFailoverExhaustion(t *testing.T) {
	config := coordinator.DefaultConfig()
	config.HeartbeatTimeout = 400 * time.Millisecond

	var heartbeatCalls atomic.Int32
	mockCoord := &mockCoordinatorService{
		heartbeatFunc: func(ctx context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			heartbeatCalls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	firstServer, firstAddr := startMockServer(t, mockCoord)
	defer firstServer.Stop()
	secondServer, secondAddr := startMockServer(t, mockCoord)
	defer secondServer.Stop()

	firstHost, firstPort := parseHostPort(firstAddr)
	secondHost, secondPort := parseHostPort(secondAddr)
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{
			{ID: "coord-a", Host: firstHost, Port: firstPort, Status: serviceregistry.ServiceStatusActive},
			{ID: "coord-b", Host: secondHost, Port: secondPort, Status: serviceregistry.ServiceStatusActive},
		},
	}
	client := coordinator.New(monitor, config)

	_, err := client.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, int32(2), heartbeatCalls.Load())
}

func TestClientHeartbeatReturnsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	mockCoord := &mockCoordinatorService{
		heartbeatFunc: func(ctx context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{{ID: "coord-a", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
	}
	client := coordinator.New(monitor, coordinator.DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{WorkerId: "test-worker"})
		result <- err
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("Heartbeat RPC did not start")
	}

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Heartbeat did not respect caller cancellation")
	}
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
			members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
		}

		client := coordinator.New(monitor, config)

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
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
			members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
		}

		client := coordinator.New(monitor, config)

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
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
			members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
		}

		client := coordinator.New(monitor, config)

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
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

func TestClientGetDAGRunStatusReturnsResponseError(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	config.MaxRetries = 0
	config.RequestTimeout = 100 * time.Millisecond

	mockCoord := &mockCoordinatorService{
		getRunStatusFunc: func(_ context.Context, req *coordinatorv1.GetDAGRunStatusRequest) (*coordinatorv1.GetDAGRunStatusResponse, error) {
			assert.Equal(t, "test-dag", req.DagName)
			assert.Equal(t, "run-123", req.DagRunId)
			return &coordinatorv1.GetDAGRunStatusResponse{Error: "failed to read status: storage unavailable"}, nil
		},
	}

	server, addr := startMockServer(t, mockCoord)
	defer server.Stop()

	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{
		members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
	}
	client := coordinator.New(monitor, config)

	_, err := client.GetDAGRunStatus(context.Background(), "test-dag", "run-123", nil)
	require.ErrorContains(t, err, "failed to read status: storage unavailable")
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
		members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
	}

	client := coordinator.New(monitor, config)

	// Initial state
	metrics := client.Metrics()
	assert.True(t, metrics.IsConnected)
	assert.Equal(t, 0, metrics.ConsecutiveFails)

	task := &dispatch.DispatchTask{DAGRunID: "test"}

	// Attempt dispatch - should fail
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := client.Dispatch(ctx, dispatch.DispatchRequest{Task: task})
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
		members: []serviceregistry.HostInfo{{Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}},
	}

	client := coordinator.New(monitor, config)

	// Make a call to establish connection
	task := &dispatch.DispatchTask{DAGRunID: "test"}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := client.Dispatch(ctx, dispatch.DispatchRequest{Task: task})
	require.NoError(t, err)

	// Cleanup should close all connections
	err = client.Cleanup(ctx)
	require.NoError(t, err)

	// Future calls should still work (will create new connections)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel2()

	err = client.Dispatch(ctx2, dispatch.DispatchRequest{Task: task})
	require.NoError(t, err)
}

func TestClientDispatch_NoCoordinators(t *testing.T) {
	t.Parallel()

	config := coordinator.DefaultConfig()
	monitor := &mockServiceMonitor{}
	client := coordinator.New(monitor, config)

	task := &dispatch.DispatchTask{
		DAGRunID: "test-dag-run",
		Target:   "test.yaml",
	}

	// Should fail gracefully with no coordinators
	monitor.members = []serviceregistry.HostInfo{}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := client.Dispatch(ctx, dispatch.DispatchRequest{Task: task})
	require.Error(t, err)
	// Could be either error depending on timing
	assert.True(t, strings.Contains(err.Error(), "no coordinators available") ||
		strings.Contains(err.Error(), "context deadline exceeded"))
}

// Mock implementations

var _ serviceregistry.ServiceRegistry = (*mockServiceMonitor)(nil)

type mockServiceMonitor struct {
	members    []serviceregistry.HostInfo
	err        error
	onMembers  func()
	getMembers func(context.Context) ([]serviceregistry.HostInfo, error)
}

func (m *mockServiceMonitor) Register(_ context.Context, _ serviceregistry.ServiceName, _ serviceregistry.HostInfo) error {
	return nil
}

func (m *mockServiceMonitor) GetServiceMembers(ctx context.Context, _ serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	if m.onMembers != nil {
		m.onMembers()
	}
	if m.getMembers != nil {
		return m.getMembers(ctx)
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.members, nil
}

func (m *mockServiceMonitor) Unregister(_ context.Context) {
	// No-op
}

func (m *mockServiceMonitor) UpdateStatus(_ context.Context, _ serviceregistry.ServiceName, _ serviceregistry.ServiceStatus) error {
	return nil
}

var _ coordinatorv1.CoordinatorServiceServer = (*mockCoordinatorService)(nil)

type mockCoordinatorService struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer

	dispatchFunc           func(context.Context, *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error)
	pollFunc               func(context.Context, *coordinatorv1.PollRequest) (*coordinatorv1.PollResponse, error)
	ackTaskClaimFunc       func(context.Context, *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error)
	getWorkersFunc         func(context.Context, *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error)
	heartbeatFunc          func(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error)
	reportStatusFunc       func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)
	getRunStatusFunc       func(context.Context, *coordinatorv1.GetDAGRunStatusRequest) (*coordinatorv1.GetDAGRunStatusResponse, error)
	streamLogsFunc         func(coordinatorv1.CoordinatorService_StreamLogsServer) error
	getStateFunc           func(context.Context, *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error)
	putStateFunc           func(context.Context, *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error)
	deleteStateFunc        func(context.Context, *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error)
	listStateFunc          func(context.Context, *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error)
	hasWorkspaceBundleFunc func(context.Context, *coordinatorv1.HasWorkspaceBundleRequest) (*coordinatorv1.HasWorkspaceBundleResponse, error)
	putWorkspaceBundleFunc func(coordinatorv1.CoordinatorService_PutWorkspaceBundleServer) error
}

type stallFirstHealthCheckServer struct {
	grpc_health_v1.UnimplementedHealthServer
	calls atomic.Int32
}

func (s *stallFirstHealthCheckServer) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	if s.calls.Add(1) == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
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

func (m *mockCoordinatorService) AckTaskClaim(ctx context.Context, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	if m.ackTaskClaimFunc != nil {
		return m.ackTaskClaimFunc(ctx, req)
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

func (m *mockCoordinatorService) GetDAGRunStatus(ctx context.Context, req *coordinatorv1.GetDAGRunStatusRequest) (*coordinatorv1.GetDAGRunStatusResponse, error) {
	if m.getRunStatusFunc != nil {
		return m.getRunStatusFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) StreamLogs(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
	if m.streamLogsFunc != nil {
		return m.streamLogsFunc(stream)
	}
	return m.UnimplementedCoordinatorServiceServer.StreamLogs(stream)
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

func (m *mockCoordinatorService) HasWorkspaceBundle(ctx context.Context, req *coordinatorv1.HasWorkspaceBundleRequest) (*coordinatorv1.HasWorkspaceBundleResponse, error) {
	if m.hasWorkspaceBundleFunc != nil {
		return m.hasWorkspaceBundleFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCoordinatorService) PutWorkspaceBundle(stream coordinatorv1.CoordinatorService_PutWorkspaceBundleServer) error {
	if m.putWorkspaceBundleFunc != nil {
		return m.putWorkspaceBundleFunc(stream)
	}
	return status.Error(codes.Unimplemented, "not implemented")
}

// Helper to start a mock gRPC server
func startMockServer(t *testing.T, service coordinatorv1.CoordinatorServiceServer) (*grpc.Server, string) {
	t.Helper()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	return startMockServerWithHealth(t, service, healthServer)
}

func startMockServerWithHealth(t *testing.T, service coordinatorv1.CoordinatorServiceServer, healthServer grpc_health_v1.HealthServer, opts ...grpc.ServerOption) (*grpc.Server, string) {
	t.Helper()
	server := grpc.NewServer(opts...)
	coordinatorv1.RegisterCoordinatorServiceServer(server, service)

	grpc_health_v1.RegisterHealthServer(server, healthServer)

	// Start server on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = server.Serve(listener)
	}()

	return server, listener.Addr().String()
}

func startMockTLSServer(t *testing.T, service coordinatorv1.CoordinatorServiceServer) (*grpc.Server, string) {
	t.Helper()

	creds, err := credentials.NewServerTLSFromFile(
		test.TestdataPath(t, "certs/cert.pem"),
		test.TestdataPath(t, "certs/key.pem"),
	)
	require.NoError(t, err)

	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	return startMockServerWithHealth(t, service, healthServer, grpc.Creds(creds))
}
