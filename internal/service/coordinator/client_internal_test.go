// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/stretchr/testify/require"
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
