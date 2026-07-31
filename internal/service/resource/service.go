// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"context"
	"sync"
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

	// CPU Usage
	if cpuPercent, err := cpu.PercentWithContext(ctx, 100*time.Millisecond, false); err != nil {
		logger.Error(ctx, "Failed to get CPU usage", tag.Error(err))
	} else if len(cpuPercent) > 0 {
		cpuVal = cpuPercent[0]
	}

	// Memory Usage
	if memStat, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		logger.Error(ctx, "Failed to get memory usage", tag.Error(err))
	} else {
		memVal = memStat.UsedPercent
		memTotal = memStat.Total
		memUsed = memStat.Used
	}

	// Disk Usage (for data directory)
	if diskStat, err := disk.UsageWithContext(ctx, s.config.Paths.DataDir); err != nil {
		logger.Error(ctx, "Failed to get disk usage", tag.Error(err), tag.Path(s.config.Paths.DataDir))
	} else {
		diskVal = diskStat.UsedPercent
		diskTotal = diskStat.Total
		diskUsed = diskStat.Used
	}

	// Load Average
	if loadStat, err := load.AvgWithContext(ctx); err != nil {
		logger.Error(ctx, "Failed to get load average", tag.Error(err))
	} else {
		loadVal = loadStat.Load1
	}

	s.store.Add(cpuVal, memVal, diskVal, loadVal, memTotal, memUsed, diskTotal, diskUsed)
}
