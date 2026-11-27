// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	appconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// The coordinator does not require the peer token for other calls, so the
// refusal without it comes from join readiness alone.
func TestClientGetJoinReadiness(t *testing.T) {
	t.Parallel()

	const token = "join-readiness-token"
	auth := coordinator.NewPeerTokenAuth(token, true, false)
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		JoinReadiness: coordinator.JoinReadinessConfig{
			ListenHost: "127.0.0.1",
			Port:       50055,
			ConfigFile: "/etc/dagu/config.yaml",
			HostSource: appconfig.ValueSource{Kind: appconfig.SourceKindFile, Name: "/etc/dagu/config.yaml"},
			Advertise:  "dagu.example.com",
		},
	})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	server, addr := startMockServerWithHealth(t, handler, healthServer,
		grpc.ChainUnaryInterceptor(auth.UnaryInterceptor()),
	)
	t.Cleanup(server.Stop)

	host, port := parseHostPort(addr)
	member := serviceregistry.HostInfo{ID: "coordinator-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}
	monitor := &mockServiceMonitor{members: []serviceregistry.HostInfo{member}}

	read := func(t *testing.T, peerToken string) coordinator.JoinReadiness {
		t.Helper()
		config := coordinator.DefaultConfig()
		config.Token = peerToken
		client, ok := coordinator.New(monitor, config).(coordinator.JoinReadinessClient)
		require.True(t, ok)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		reports, err := client.GetJoinReadiness(ctx)
		require.NoError(t, err)
		require.Len(t, reports, 1)
		assert.Equal(t, member.ID, reports[0].Coordinator.ID)
		return reports[0]
	}

	t.Run("with the peer token", func(t *testing.T) {
		t.Parallel()
		got := read(t, token)
		require.NoError(t, got.Err)
		report := got.Report
		assert.Equal(t, "127.0.0.1", report.ListenHost)
		assert.Equal(t, int32(50055), report.Port)
		assert.Equal(t, "/etc/dagu/config.yaml", report.ConfigFile)
		assert.True(t, report.LoopbackOnly)
		assert.Equal(t, coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FILE, report.HostSource.Kind)
		assert.Equal(t, "/etc/dagu/config.yaml", report.HostSource.Name)
		assert.Equal(t, "dagu.example.com", report.Advertise)
		assert.NotEqual(t, coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_UNSPECIFIED, report.Environment)
		assert.False(t, report.BuiltinTls)
		assert.False(t, report.Ready)
	})

	t.Run("without the peer token", func(t *testing.T) {
		t.Parallel()
		got := read(t, "")
		assert.Nil(t, got.Report)
		assert.Equal(t, codes.Unauthenticated, status.Code(got.Err))
	})
}

func TestClientGetJoinReadinessNoCoordinators(t *testing.T) {
	t.Parallel()

	client, ok := coordinator.New(&mockServiceMonitor{}, coordinator.DefaultConfig()).(coordinator.JoinReadinessClient)
	require.True(t, ok)
	_, err := client.GetJoinReadiness(context.Background())
	assert.Error(t, err)
}

// barrierCoordinator answers join readiness only once every coordinator of
// its barrier has been asked.
type barrierCoordinator struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	barrier *sync.WaitGroup
}

func (c *barrierCoordinator) GetJoinReadiness(ctx context.Context, _ *coordinatorv1.GetJoinReadinessRequest) (*coordinatorv1.GetJoinReadinessResponse, error) {
	c.barrier.Done()
	waited := make(chan struct{})
	go func() {
		c.barrier.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		return &coordinatorv1.GetJoinReadinessResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Each coordinator answers only after the other was asked, so asking them
// one after the other would time out.
func TestClientGetJoinReadinessAsksConcurrently(t *testing.T) {
	t.Parallel()

	var barrier sync.WaitGroup
	barrier.Add(2)
	var members []serviceregistry.HostInfo
	for i := range 2 {
		server, addr := startMockServer(t, &barrierCoordinator{barrier: &barrier})
		t.Cleanup(server.Stop)
		host, port := parseHostPort(addr)
		members = append(members, serviceregistry.HostInfo{ID: fmt.Sprintf("coordinator-%d", i), Host: host, Port: port})
	}

	client, ok := coordinator.New(&mockServiceMonitor{members: members}, coordinator.DefaultConfig()).(coordinator.JoinReadinessClient)
	require.True(t, ok)
	reports, err := client.GetJoinReadiness(context.Background())
	require.NoError(t, err)
	require.Len(t, reports, 2)
	for _, report := range reports {
		assert.NoError(t, report.Err)
	}
}
