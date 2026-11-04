// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"context"
	"runtime"
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

// Latest has no sample before the service starts, and then the latest one,
// with the disk of the data directory and the memory read.
func TestService_Latest(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Monitoring: config.MonitoringConfig{
			Retention: time.Hour,
			Interval:  time.Hour,
		},
		Paths: config.PathsConfig{
			DataDir: t.TempDir(),
		},
	}
	svc := NewService(cfg)

	_, ok := svc.Latest()
	assert.False(t, ok)

	before := time.Now()
	require.NoError(t, svc.Start(t.Context()))
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	var sample Sample
	require.Eventually(t, func() bool {
		sample, ok = svc.Latest()
		return ok
	}, 5*time.Second, 10*time.Millisecond)
	assert.False(t, sample.At.Before(before))
	require.NotNil(t, sample.Disk)
	assert.Positive(t, sample.Disk.TotalBytes)
	assert.InDelta(t, 50, sample.Disk.UsedPercent, 50)
	require.NotNil(t, sample.Memory)
	assert.Positive(t, sample.Memory.TotalBytes)
}

// Since averages the CPU use of the samples after a time and takes their
// peak; with none after it, the latest sample's own CPU use stands.
func TestService_Since(t *testing.T) {
	t.Parallel()

	svc := NewService(&config.Config{Monitoring: config.MonitoringConfig{Retention: time.Hour, Interval: time.Second}})
	_, ok := svc.Since(time.Time{})
	assert.False(t, ok, "no sample yet")

	start := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	for i, percent := range []float64{90, 10, 30, 50} {
		svc.recordCPU(start.Add(time.Duration(i)*time.Minute), percent)
	}
	latest := 50.0
	svc.latest.Store(&Sample{At: start.Add(3 * time.Minute), CPUPercent: &latest})

	sample, ok := svc.Since(start)
	require.True(t, ok)
	assert.InDelta(t, 30, *sample.CPUPercent, 0.001, "the three samples after the start")
	assert.InDelta(t, 50, *sample.CPUPeak, 0.001)

	sample, ok = svc.Since(start.Add(time.Hour))
	require.True(t, ok)
	assert.InDelta(t, 50, *sample.CPUPercent, 0.001)
	assert.Nil(t, sample.CPUPeak)
}

// In a container with a CPU quota, CPU use is the cgroup's CPU time since
// the sample before, of the quota.
func TestService_ContainerCPU(t *testing.T) {
	t.Parallel()

	svc := NewService(&config.Config{Monitoring: config.MonitoringConfig{Retention: time.Hour, Interval: time.Second}})
	start := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	quota := func(used time.Duration) cgroupUsage { return cgroupUsage{CPULimit: 2, CPUTime: used} }

	_, ok := svc.cpuPercent(t.Context(), start, quota(time.Second), true)
	assert.False(t, ok, "nothing to compare the first sample with")
	percent, ok := svc.cpuPercent(t.Context(), start.Add(10*time.Second), quota(11*time.Second), true)
	require.True(t, ok)
	assert.InDelta(t, 50, percent, 0.001, "10s of CPU over 10s of 2 CPUs")
}

// A container's memory limit below the machine's memory stands for the
// machine's, with the memory counted toward it.
func TestService_ContainerMemory(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Monitoring: config.MonitoringConfig{Retention: time.Hour, Interval: time.Hour},
		Paths:      config.PathsConfig{DataDir: t.TempDir()},
	}
	svc := NewService(cfg)
	svc.cgroup = cgroup{root: fakeRoot(t, map[string]string{
		"proc/self/cgroup":                 "0::/\n",
		"sys/fs/cgroup/cgroup.controllers": "memory\n",
		"sys/fs/cgroup/memory.max":         "1048576\n",
		"sys/fs/cgroup/memory.current":     "524288\n",
	})}

	svc.collect(t.Context())

	sample, ok := svc.Latest()
	require.True(t, ok)
	require.NotNil(t, sample.Memory)
	assert.Equal(t, Usage{UsedPercent: 50, UsedBytes: 524288, TotalBytes: 1048576, Limited: true}, *sample.Memory)
	require.NotNil(t, sample.Disk)
	if runtime.GOOS != "windows" {
		assert.NotNil(t, sample.Disk.InodesUsedPercent, "inodes of the data directory's disk")
	}
	assert.Nil(t, sample.LogDisk, "no log directory on another disk")
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
