// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// Service collects system resource metrics at regular intervals.
type Service struct {
	config *config.Config
	store  Store
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	latest atomic.Pointer[Sample]
}

// Sample is one reading of the resources of the machine the process runs
// on. A part whose reading failed is nil, and CPUs is zero when unknown.
type Sample struct {
	At time.Time
	// CPUs counts the logical CPUs.
	CPUs int
	// CPUPercent is how busy all CPUs were, from 0 to 100.
	CPUPercent *float64
	Memory     *Usage
	// Disk is the disk that holds the data directory.
	Disk *Usage
	Load *LoadAverage
}

// Usage is how much of a memory or a disk is in use.
type Usage struct {
	UsedPercent float64
	UsedBytes   uint64
	TotalBytes  uint64
}

// LoadAverage is the system load averaged over 1, 5, and 15 minutes.
type LoadAverage struct {
	Load1  float64
	Load5  float64
	Load15 float64
}

const defaultMonitoringInterval = 10 * time.Second

// NewService creates a new resource Service configured with cfg.
// It panics if cfg is nil. If cfg.Monitoring.Interval is not greater than zero,
// it is set to defaultMonitoringInterval. The returned Service uses an in-memory
// store with retention taken from cfg.Monitoring.Retention and has its done
// channel initialized; the cancel function is not set.
func NewService(cfg *config.Config) *Service {
	if cfg == nil {
		panic("config must not be nil")
	}
	// Guard against invalid interval to prevent ticker panic
	if cfg.Monitoring.Interval <= 0 {
		cfg.Monitoring.Interval = defaultMonitoringInterval
	}
	return &Service{
		config: cfg,
		store:  NewMemoryStore(cfg.Monitoring.Retention, cfg.Monitoring.Interval),
		done:   make(chan struct{}),
	}
}

// Start begins collecting metrics. Safe to call multiple times.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return nil // Already started
	}

	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	go s.loop(ctx)

	logger.Info(ctx, "Resource monitoring service started")
	return nil
}

// Stop halts metric collection and waits for the loop to exit.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.mu.Unlock()
		<-s.done
	} else {
		s.mu.Unlock()
	}
	logger.Info(ctx, "Resource monitoring service stopped")
	return nil
}

// GetHistory returns metrics collected within the given duration.
func (s *Service) GetHistory(duration time.Duration) *ResourceHistory {
	return s.store.GetHistory(duration)
}

// Latest returns the latest sample, and false before one is taken.
func (s *Service) Latest() (Sample, bool) {
	sample := s.latest.Load()
	if sample == nil {
		return Sample{}, false
	}
	return *sample, true
}

// DiskUsedPercent returns how full the data directory's disk was at the
// latest sample, and false before a sample succeeds.
func (s *Service) DiskUsedPercent() (float64, bool) {
	return s.store.DiskUsedPercent()
}

func (s *Service) loop(ctx context.Context) {
	defer close(s.done)

	ticker := time.NewTicker(s.config.Monitoring.Interval)
	defer ticker.Stop()

	// Collect initial metrics immediately
	s.collect(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.collect(ctx)
		}
	}
}

func (s *Service) collect(ctx context.Context) {
	var cpuVal, memVal, diskVal, loadVal float64
	var memTotal, memUsed, diskTotal, diskUsed uint64
	sample := Sample{At: time.Now()}

	// CPU Usage
	if cpuPercent, err := cpu.PercentWithContext(ctx, 100*time.Millisecond, false); err != nil {
		logger.Error(ctx, "Failed to get CPU usage", tag.Error(err))
	} else if len(cpuPercent) > 0 {
		cpuVal = cpuPercent[0]
		sample.CPUPercent = &cpuVal
	}
	if cpus, err := cpu.CountsWithContext(ctx, true); err == nil {
		sample.CPUs = cpus
	}

	// Memory Usage
	if memStat, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		logger.Error(ctx, "Failed to get memory usage", tag.Error(err))
	} else {
		memVal = memStat.UsedPercent
		memTotal = memStat.Total
		memUsed = memStat.Used
		sample.Memory = &Usage{UsedPercent: memVal, UsedBytes: memUsed, TotalBytes: memTotal}
	}

	// Disk Usage (for data directory)
	if diskStat, err := disk.UsageWithContext(ctx, s.config.Paths.DataDir); err != nil {
		logger.Error(ctx, "Failed to get disk usage", tag.Error(err), tag.Path(s.config.Paths.DataDir))
	} else {
		diskVal = diskStat.UsedPercent
		diskTotal = diskStat.Total
		diskUsed = diskStat.Used
		sample.Disk = &Usage{UsedPercent: diskVal, UsedBytes: diskUsed, TotalBytes: diskTotal}
	}

	// Load Average
	if loadStat, err := load.AvgWithContext(ctx); err != nil {
		logger.Error(ctx, "Failed to get load average", tag.Error(err))
	} else {
		loadVal = loadStat.Load1
		sample.Load = &LoadAverage{Load1: loadStat.Load1, Load5: loadStat.Load5, Load15: loadStat.Load15}
	}

	s.store.Add(cpuVal, memVal, diskVal, loadVal, memTotal, memUsed, diskTotal, diskUsed)
	s.latest.Store(&sample)
}
