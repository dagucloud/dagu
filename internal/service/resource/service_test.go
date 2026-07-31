// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewService(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Monitoring: config.MonitoringConfig{
			Retention: time.Hour,
			Interval:  5 * time.Second,
		},
	}

	svc := NewService(cfg)

	require.NotNil(t, svc)
	assert.NotNil(t, svc.store)
	assert.NotNil(t, svc.done)
	assert.Equal(t, cfg, svc.config)
}

func TestNewService_DefaultInterval(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Monitoring: config.MonitoringConfig{
			Retention: time.Hour,
			Interval:  0, // Invalid interval
		},
	}

	svc := NewService(cfg)
	assert.Equal(t, defaultMonitoringInterval, svc.config.Monitoring.Interval)
}

func TestService_Lifecycle(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Monitoring: config.MonitoringConfig{
			Retention: time.Hour,
			Interval:  50 * time.Millisecond,
		},
		Paths: config.PathsConfig{
			DataDir: t.TempDir(),
		},
	}

	svc := NewService(cfg)
	ctx := context.Background()

	// Start
	err := svc.Start(ctx)
	require.NoError(t, err)

	// Double start should be no-op
	err = svc.Start(ctx)
	require.NoError(t, err)

	// Wait for collection using Eventually for CI resilience
	require.Eventually(t, func() bool {
		history := svc.GetHistory(time.Hour)
		return history != nil && len(history.CPU) >= 1
	}, time.Second, 50*time.Millisecond, "should collect at least one data point")

	// Stop
	err = svc.Stop(ctx)
	require.NoError(t, err)
}

func TestService_GetHistoryWithoutStart(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Monitoring: config.MonitoringConfig{
			Retention: time.Hour,
			Interval:  time.Second,
		},
	}

	svc := NewService(cfg)

	history := svc.GetHistory(time.Hour)
	require.NotNil(t, history)
	assert.Empty(t, history.CPU)
}

func TestService_StopWithoutStart(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Monitoring: config.MonitoringConfig{
			Retention: time.Hour,
			Interval:  time.Second,
		},
	}

	svc := NewService(cfg)
	ctx := context.Background()

	// Stop handles nil cancel gracefully (no blocking)
	err := svc.Stop(ctx)
	require.NoError(t, err)
}
