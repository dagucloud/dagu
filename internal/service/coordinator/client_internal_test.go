// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type stubServiceRegistry struct{}

func (stubServiceRegistry) Register(context.Context, serviceregistry.ServiceName, serviceregistry.HostInfo) error {
	return nil
}
func (stubServiceRegistry) Unregister(context.Context) {}
func (stubServiceRegistry) GetServiceMembers(context.Context, serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	return nil, nil
}
func (stubServiceRegistry) UpdateStatus(context.Context, serviceregistry.ServiceName, serviceregistry.ServiceStatus) error {
	return nil
}

type callbackLogger struct {
	info func()
}

func (l *callbackLogger) Debug(string, ...slog.Attr) {}

func (l *callbackLogger) Info(string, ...slog.Attr) {
	if l.info != nil {
		l.info()
	}
}

func (l *callbackLogger) Warn(string, ...slog.Attr)  {}
func (l *callbackLogger) Error(string, ...slog.Attr) {}
func (l *callbackLogger) Fatal(string, ...slog.Attr) {}

func (l *callbackLogger) Debugf(string, ...any) {}
func (l *callbackLogger) Infof(string, ...any)  {}
func (l *callbackLogger) Warnf(string, ...any)  {}
func (l *callbackLogger) Errorf(string, ...any) {}
func (l *callbackLogger) Fatalf(string, ...any) {}

func (l *callbackLogger) With(...slog.Attr) logger.Logger {
	return l
}

func (l *callbackLogger) WithGroup(string) logger.Logger {
	return l
}

func (l *callbackLogger) Write(string) {}

func TestClientRecordSuccess_LogsRecoveryWithoutHoldingStateLock(t *testing.T) {
	t.Parallel()

	cli := New(stubServiceRegistry{}, DefaultConfig()).(*clientImpl)
	cli.state.IsConnected = false
	cli.state.ConsecutiveFails = 3

	ctx := logger.WithFixedLogger(context.Background(), &callbackLogger{
		info: func() {
			_ = cli.Metrics()
		},
	})

	done := make(chan struct{})
	go func() {
		cli.recordSuccess(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("recordSuccess deadlocked while logging recovery")
	}

	metrics := cli.Metrics()
	require.True(t, metrics.IsConnected)
	require.Zero(t, metrics.ConsecutiveFails)
	require.Nil(t, metrics.LastError)
}

func TestClientCacheUsesDerivedKeyForEmptyCoordinatorIDs(t *testing.T) {
	t.Parallel()

	cli := &clientImpl{
		config:  DefaultConfig(),
		clients: make(map[string]*client),
	}

	member1 := serviceregistry.HostInfo{Host: "127.0.0.1", Port: 1234}
	member2 := serviceregistry.HostInfo{Host: "127.0.0.1", Port: 5678}

	client1, err := cli.getOrCreateClient(member1)
	require.NoError(t, err)

	client2, err := cli.getOrCreateClient(member2)
	require.NoError(t, err)

	require.NotSame(t, client1, client2)
	require.Len(t, cli.clients, 2)
	require.Contains(t, cli.clients, coordinatorMemberKey(member1))
	require.Contains(t, cli.clients, coordinatorMemberKey(member2))
	require.NoError(t, cli.Cleanup(t.Context()))
}

func TestClientCacheUsesAdvertisedEndpointAcrossCoordinatorProcesses(t *testing.T) {
	t.Parallel()

	cli := &clientImpl{
		config:  DefaultConfig(),
		clients: make(map[string]*client),
	}
	endpoint := serviceregistry.HostInfo{ID: "coord-a", Host: "coordinator", Port: 50055}
	replacement := endpoint
	replacement.ID = "coord-b"

	first, err := cli.getOrCreateClient(endpoint)
	require.NoError(t, err)
	second, err := cli.getOrCreateClient(replacement)
	require.NoError(t, err)

	require.Same(t, first, second)
	require.Len(t, cli.clients, 1)
	require.NoError(t, cli.Cleanup(t.Context()))
}

// interruptingRegistry finds no coordinator, and stops the attempts of the
// call during its first lookup, as an interrupt during the first attempt does.
type interruptingRegistry struct {
	stubServiceRegistry
	stop    func()
	lookups int
}

func (r *interruptingRegistry) GetServiceMembers(context.Context, serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	r.lookups++
	r.stop()
	return nil, nil
}

// A dispatch whose attempts stop makes no retry once its attempt in flight
// ends, and returns that attempt's error at once. The retry interval is an
// hour, so a retry would hold the call.
func TestDispatchStopsRetryingWhenAttemptsStop(t *testing.T) {
	t.Parallel()

	stop := make(chan struct{})
	registry := &interruptingRegistry{stop: sync.OnceFunc(func() { close(stop) })}
	cfg := DefaultConfig()
	cfg.RetryInterval = time.Hour
	cli := New(registry, cfg)

	err := cli.Dispatch(WithAttemptStop(context.Background(), stop), dispatch.DispatchRequest{
		Task: &dispatch.DispatchTask{Operation: dispatch.DispatchOperationStart, Target: "deploy", DAGRunID: "run"},
	})
	require.ErrorContains(t, err, "no coordinators available")
	require.Equal(t, 1, registry.lookups)
}

// stalledHealthClient is the health service of a coordinator that accepts a
// check and never answers it. The check stops the call's attempts, as an
// interrupt during the check does.
type stalledHealthClient struct {
	grpc_health_v1.HealthClient
	stop func()
}

func (c stalledHealthClient) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthCheckResponse, error) {
	c.stop()
	<-ctx.Done()
	return nil, ctx.Err()
}

// The health check before a dispatch changes nothing, so it ends as soon as
// the call's attempts stop, and the call returns without a dispatch.
func TestDispatchHealthCheckEndsWhenAttemptsStop(t *testing.T) {
	t.Parallel()

	stop := make(chan struct{})
	member := serviceregistry.HostInfo{ID: "coord-1", Host: "127.0.0.1", Port: 1}
	cli := New(staticStateRegistry{members: []serviceregistry.HostInfo{member}}, DefaultConfig()).(*clientImpl)
	cli.clients[coordinatorMemberKey(member)] = &client{
		address:      coordinatorAddress(member),
		healthClient: stalledHealthClient{stop: sync.OnceFunc(func() { close(stop) })},
	}

	err := cli.Dispatch(WithAttemptStop(context.Background(), stop), dispatch.DispatchRequest{
		Task: &dispatch.DispatchTask{Operation: dispatch.DispatchOperationStart, Target: "deploy", DAGRunID: "run"},
	})
	require.ErrorContains(t, err, "health check failed")
}
