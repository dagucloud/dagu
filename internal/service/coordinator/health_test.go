// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/service/healthcheck"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestServiceHealthServerStartsAndStops(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	grpcHealthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, grpcHealthServer)

	srv := NewService(
		grpcServer,
		NewHandler(HandlerConfig{}),
		listener,
		grpcHealthServer,
		healthcheck.NewServerWithAddr("coordinator", "127.0.0.1:0"),
		nil,
		&config.Config{},
		"test-coordinator",
		"127.0.0.1",
	)

	ctx := context.Background()
	require.NoError(t, srv.Start(ctx))

	baseURL := requireHealthServerURL(t, srv.httpHealthServer)
	requireHealthyHealthEndpoint(t, baseURL)
	requireCoordinatorServing(t, listener.Addr().String())

	require.NoError(t, srv.Stop(ctx))
	requireHealthServerStopped(t, baseURL)
}

func TestServiceStartCleansUpHealthServerOnRegistrationFailure(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	grpcHealthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, grpcHealthServer)

	registry := &blockingFailingRegistry{
		started: make(chan struct{}, 1),
		proceed: make(chan struct{}),
	}

	srv := NewService(
		grpcServer,
		NewHandler(HandlerConfig{}),
		listener,
		grpcHealthServer,
		healthcheck.NewServerWithAddr("coordinator", "127.0.0.1:0"),
		registry,
		&config.Config{},
		"test-coordinator",
		"127.0.0.1",
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(context.Background())
	}()

	select {
	case <-registry.started:
	case <-time.After(5 * time.Second):
		t.Fatal("service registry registration did not start")
	}

	baseURL := requireHealthServerURL(t, srv.httpHealthServer)
	close(registry.proceed)

	select {
	case err := <-errCh:
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to register with service registry")
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator start did not return after registration failure")
	}

	requireHealthServerStopped(t, baseURL)
}

type blockingFailingRegistry struct {
	started chan struct{}
	proceed chan struct{}
}

func (r *blockingFailingRegistry) Register(context.Context, exec.ServiceName, exec.HostInfo) error {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-r.proceed
	return fmt.Errorf("register failed")
}

func (*blockingFailingRegistry) Unregister(context.Context) {}

func (*blockingFailingRegistry) GetServiceMembers(context.Context, exec.ServiceName) ([]exec.HostInfo, error) {
	return nil, nil
}

func (*blockingFailingRegistry) UpdateStatus(context.Context, exec.ServiceName, exec.ServiceStatus) error {
	return nil
}

func requireHealthServerURL(t *testing.T, hs *healthcheck.Server) string {
	t.Helper()

	var url string
	require.Eventually(t, func() bool {
		url = hs.URL()
		return url != ""
	}, 5*time.Second, 10*time.Millisecond, "health server did not bind an address")
	return url
}

func requireHealthyHealthEndpoint(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: time.Second}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/health")
		if err != nil {
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode != http.StatusOK {
			return false
		}

		var healthResp healthcheck.Response
		if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
			return false
		}

		return healthResp.Status == "healthy"
	}, 5*time.Second, 10*time.Millisecond, "health endpoint did not become healthy")
}

func requireCoordinatorServing(t *testing.T, addr string) {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() {
		_ = conn.Close()
	}()

	client := grpc_health_v1.NewHealthClient(conn)
	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{
			Service: coordinatorv1.CoordinatorService_ServiceDesc.ServiceName,
		})
		return err == nil && resp.Status == grpc_health_v1.HealthCheckResponse_SERVING
	}, 5*time.Second, 10*time.Millisecond, "coordinator gRPC health did not become serving")
}

func requireHealthServerStopped(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: 200 * time.Millisecond}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			return false
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "health server still responded after stop")
}
