// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const h2cReasonKey = "dagu-peer-h2c-reason"

// The coordinator records each worker's connection from the connection that
// carries its heartbeat, and GetWorkers returns it (Spec 108 §Workers List).
func TestHeartbeatConnection(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	key, cert := server.enroll(t, created.Invite)
	h2c, _ := server.dial(t, insecure.NewCredentials())

	for _, tc := range []struct {
		name       string
		client     coordinatorv1.CoordinatorServiceClient
		reason     string
		want       coordinatorv1.WorkerConnection
		wantReason string
	}{
		{name: "Certificate", client: server.workerClient(t, cert.Raw, key), want: coordinatorv1.WorkerConnection_WORKER_CONNECTION_CERTIFICATE},
		{name: "BuiltinTLS", client: server.tlsClient(t), want: coordinatorv1.WorkerConnection_WORKER_CONNECTION_TLS},
		// A reason over TLS is not a plaintext reason.
		{name: "TLSWithReason", client: server.tlsClient(t), reason: "peer.insecure", want: coordinatorv1.WorkerConnection_WORKER_CONNECTION_TLS},
		{name: "Insecure", client: h2c, reason: "peer.insecure", want: coordinatorv1.WorkerConnection_WORKER_CONNECTION_PLAINTEXT, wantReason: "peer.insecure"},
		{name: "NoTLSSource", client: h2c, reason: "no TLS source", want: coordinatorv1.WorkerConnection_WORKER_CONNECTION_PLAINTEXT, wantReason: "no TLS source"},
		{name: "NetworkPath", client: h2c, reason: "tls failed on the network path", want: coordinatorv1.WorkerConnection_WORKER_CONNECTION_PLAINTEXT, wantReason: "tls failed on the network path"},
		{name: "OlderVersion", client: h2c, want: coordinatorv1.WorkerConnection_WORKER_CONNECTION_PLAINTEXT, wantReason: "older version"},
		{name: "UnknownReason", client: h2c, reason: "something else", want: coordinatorv1.WorkerConnection_WORKER_CONNECTION_PLAINTEXT, wantReason: "older version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := callContext(t)
			if tc.reason != "" {
				ctx = metadata.AppendToOutgoingContext(ctx, h2cReasonKey, tc.reason)
			}
			_, err := tc.client.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{WorkerId: tc.name})
			require.NoError(t, err)

			worker := findWorker(callContext(t), t, server.tlsClient(t), tc.name)
			assert.Equal(t, tc.want, worker.Connection)
			assert.Equal(t, tc.wantReason, worker.PlaintextReason)
		})
	}
}

// A coordinator that shares worker records with others keeps the connection
// in the record, so each coordinator lists it.
func TestHeartbeatConnectionShared(t *testing.T) {
	t.Parallel()
	heartbeats := store.NewWorkerHeartbeatStore(testutil.NewMemoryBackend().Collection(persis.CollectionWorkerHeartbeats))
	receiver := coordinator.NewHandler(coordinator.HandlerConfig{WorkerHeartbeatStore: heartbeats})
	other := coordinator.NewHandler(coordinator.HandlerConfig{WorkerHeartbeatStore: heartbeats})

	ctx := peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{}})
	_, err := receiver.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{WorkerId: "tls-worker"})
	require.NoError(t, err)
	ctx = metadata.NewIncomingContext(t.Context(), metadata.Pairs(h2cReasonKey, "no TLS source"))
	_, err = receiver.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{WorkerId: "h2c-worker"})
	require.NoError(t, err)

	resp, err := other.GetWorkers(t.Context(), &coordinatorv1.GetWorkersRequest{})
	require.NoError(t, err)
	got := map[string]string{}
	for _, worker := range resp.Workers {
		got[worker.WorkerId] = worker.Connection.String() + " " + worker.PlaintextReason
	}
	assert.Equal(t, map[string]string{
		"tls-worker": "WORKER_CONNECTION_TLS ",
		"h2c-worker": "WORKER_CONNECTION_PLAINTEXT no TLS source",
	}, got)
}

// GetWorkers returns the environment a worker's heartbeat reports, from the
// in-memory list and from a shared record (Spec 108 §Workers List).
func TestHeartbeatEnvironment(t *testing.T) {
	t.Parallel()
	heartbeats := store.NewWorkerHeartbeatStore(testutil.NewMemoryBackend().Collection(persis.CollectionWorkerHeartbeats))
	for name, handler := range map[string]*coordinator.Handler{
		"InMemory": coordinator.NewHandler(coordinator.HandlerConfig{}),
		"Shared":   coordinator.NewHandler(coordinator.HandlerConfig{WorkerHeartbeatStore: heartbeats}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for id, environment := range map[string]coordinatorv1.HostEnvironment{
				"kubernetes": coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES,
				"container":  coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER,
				"host":       coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST,
				"older":      coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_UNSPECIFIED,
			} {
				_, err := handler.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: name + "-" + id, Environment: environment})
				require.NoError(t, err)
			}
			resp, err := handler.GetWorkers(t.Context(), &coordinatorv1.GetWorkersRequest{})
			require.NoError(t, err)
			got := map[string]coordinatorv1.HostEnvironment{}
			for _, worker := range resp.Workers {
				got[worker.WorkerId] = worker.Environment
			}
			assert.Equal(t, map[string]coordinatorv1.HostEnvironment{
				name + "-kubernetes": coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES,
				name + "-container":  coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER,
				name + "-host":       coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST,
				name + "-older":      coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_UNSPECIFIED,
			}, got)
		})
	}
}

// findWorker returns the worker with id that client lists.
func findWorker(ctx context.Context, t *testing.T, client coordinatorv1.CoordinatorServiceClient, id string) *coordinatorv1.WorkerInfo {
	t.Helper()
	resp, err := client.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
	require.NoError(t, err)
	for _, worker := range resp.Workers {
		if worker.WorkerId == id {
			return worker
		}
	}
	require.Failf(t, "worker not listed", "%s", id)
	return nil
}
