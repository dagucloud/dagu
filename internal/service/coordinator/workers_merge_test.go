// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"net"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// sortedListeners returns n loopback listeners in the order the client
// tries coordinators: the order of their addresses.
func sortedListeners(t *testing.T, n int) []net.Listener {
	t.Helper()
	listeners := make([]net.Listener, n)
	for i := range listeners {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		listeners[i] = listener
	}
	sort.Slice(listeners, func(i, j int) bool { return listeners[i].Addr().String() < listeners[j].Addr().String() })
	return listeners
}

// workersFrom returns a coordinator that lists worker.
func workersFrom(worker *coordinatorv1.WorkerInfo) memberKind {
	return serving(&mockCoordinatorService{
		getWorkersFunc: func(context.Context, *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
			return &coordinatorv1.GetWorkersResponse{Workers: []*coordinatorv1.WorkerInfo{worker}}, nil
		},
	})
}

// A coordinator of an earlier release lists the same heartbeat without the
// fields it predates. The list keeps them from the coordinator that has
// them, whichever is asked first.
func TestClientGetWorkers_KeepsNewerFields(t *testing.T) {
	t.Parallel()

	heartbeat := time.Now().Unix()
	older := workersFrom(&coordinatorv1.WorkerInfo{WorkerId: "worker-1", LastHeartbeatAt: heartbeat})
	newer := workersFrom(&coordinatorv1.WorkerInfo{
		WorkerId:        "worker-1",
		LastHeartbeatAt: heartbeat,
		Version:         "2.27.0",
		Connection:      coordinatorv1.WorkerConnection_WORKER_CONNECTION_PLAINTEXT,
		PlaintextReason: "peer.insecure",
	})
	for name, kinds := range map[string][]memberKind{"older first": {older, newer}, "newer first": {newer, older}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			members := make([]serviceregistry.HostInfo, len(kinds))
			for i, listener := range sortedListeners(t, len(kinds)) {
				members[i] = kinds[i](t, listener)
			}
			client := coordinator.New(&mockServiceMonitor{members: members}, coordinator.DefaultConfig())
			t.Cleanup(func() { _ = client.Cleanup(context.Background()) })
			workers, err := client.GetWorkers(t.Context())
			require.NoError(t, err)
			require.Len(t, workers, 1)
			assert.Equal(t, "2.27.0", workers[0].Version)
			assert.Equal(t, coordinatorv1.WorkerConnection_WORKER_CONNECTION_PLAINTEXT, workers[0].Connection)
			assert.Equal(t, "peer.insecure", workers[0].PlaintextReason)
		})
	}
}
