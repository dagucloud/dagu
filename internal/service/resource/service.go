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
	// cgroup reads the limits of the process's container, if it has any.
	cgroup cgroup
	// prevCPU is the cgroup's CPU time at the previous sample, which the
	// collect loop owns.
	prevCPU cpuTime

	// cpuMu guards cpuHistory, the CPU use of the samples of the last
	// cpuWindow, for Since.
	cpuMu      sync.Mutex
	cpuHistory *RingBuffer[cpuPoint]
}

// Sample is one reading of the resources of the machine the process runs
// on, or of the container it runs in when the container has limits. A part
// whose reading failed is nil, and CPUs is zero when unknown.
type Sample struct {
	At time.Time
	// CPUs counts the machine's logical CPUs, and CPULimit the CPUs the
	// container may use, zero without a limit.
	CPUs     int
	CPULimit float64
	// CPUPercent is how busy the CPUs were since the sample before, from 0
	// to 100, of CPULimit when there is one. In a sample from Since, it is
	// the average over the samples since then, and CPUPeak their highest.
	CPUPercent *float64
	CPUPeak    *float64
	Memory     *Usage
	// Disk is the disk that holds the data directory, and LogDisk the one
	// that holds the log directory when that is another disk.
	Disk    *Usage
	LogDisk *Usage
	Load    *LoadAverage
}

// Usage is how much of a memory or a disk is in use.
type Usage struct {
	UsedPercent float64
	UsedBytes   uint64
	TotalBytes  uint64
	// Limited is set for the memory of a container with a memory limit:
	// TotalBytes is the limit and UsedBytes the memory counted toward it.
	Limited bool
	// InodesUsedPercent is how many of a disk's inodes are in use, nil
	// where the filesystem does not say.
	InodesUsedPercent *float64
}

// cpuWindow is how far back Since can average CPU use: the longest time
// between two reports to Dagu Console.
const cpuWindow = time.Hour

// cpuPoint is the CPU use of one sample.
type cpuPoint struct {
	at      time.Time
	percent float64
}

// cpuTime is a cgroup's CPU time as of a sample.
type cpuTime struct {
	at   time.Time
	used time.Duration
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
		config:     cfg,
		store:      NewMemoryStore(cfg.Monitoring.Retention, cfg.Monitoring.Interval),
		done:       make(chan struct{}),
		cgroup:     cgroup{root: "/"},
		cpuHistory: NewRingBuffer[cpuPoint](int(cpuWindow/cfg.Monitoring.Interval) + 1),
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

// Since returns the latest sample with its CPU use averaged over the samples
// taken after since, and the highest of them as CPUPeak; the latest
// sample's own when none was. It reports false before a sample is taken.
func (s *Service) Since(since time.Time) (Sample, bool) {
	sample, ok := s.Latest()
	if !ok {
		return Sample{}, false
	}
	s.cpuMu.Lock()
	defer s.cpuMu.Unlock()
	var sum, peak float64
	n := 0
	s.cpuHistory.ForEach(func(p *cpuPoint) bool {
		if p.at.After(since) {
			sum += p.percent
			peak = max(peak, p.percent)
			n++
		}
		return true
	})
	if n > 0 {
		average := sum / float64(n)
		sample.CPUPercent, sample.CPUPeak = &average, &peak
	}
	return sample, true
}

// recordCPU keeps a sample's CPU use for Since.
func (s *Service) recordCPU(at time.Time, percent float64) {
	s.cpuMu.Lock()
	defer s.cpuMu.Unlock()
	s.cpuHistory.Push(cpuPoint{at: at, percent: percent})
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
	limits, limited := s.cgroup.read()

	if percent, ok := s.cpuPercent(ctx, sample.At, limits, limited); ok {
		cpuVal = percent
		sample.CPUPercent = &cpuVal
		s.recordCPU(sample.At, percent)
	}
	if cpus, err := cpu.CountsWithContext(ctx, true); err == nil {
		sample.CPUs = cpus
	}
	sample.CPULimit = limits.CPULimit

	// Memory: the container's, when its limit is below the machine's.
	if memStat, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		logger.Error(ctx, "Failed to get memory usage", tag.Error(err))
	} else {
		usage := Usage{UsedPercent: memStat.UsedPercent, UsedBytes: memStat.Used, TotalBytes: memStat.Total}
		if limited && limits.MemoryLimit > 0 && limits.MemoryLimit < memStat.Total {
			usage = Usage{UsedPercent: percentOf(limits.MemoryUsed, limits.MemoryLimit), UsedBytes: limits.MemoryUsed,
				TotalBytes: limits.MemoryLimit, Limited: true}
		}
		memVal, memTotal, memUsed = usage.UsedPercent, usage.TotalBytes, usage.UsedBytes
		sample.Memory = &usage
	}

	dataDir := s.config.Paths.DataDir
	if usage, err := diskUsage(ctx, dataDir); err != nil {
		logger.Error(ctx, "Failed to get disk usage", tag.Error(err), tag.Path(dataDir))
	} else {
		diskVal, diskTotal, diskUsed = usage.UsedPercent, usage.TotalBytes, usage.UsedBytes
		sample.Disk = usage
	}
	if logDir := s.config.Paths.LogDir; logDir != "" && !sameDisk(dataDir, logDir) {
		if usage, err := diskUsage(ctx, logDir); err != nil {
			logger.Error(ctx, "Failed to get disk usage", tag.Error(err), tag.Path(logDir))
		} else {
			sample.LogDisk = usage
		}
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

// cpuPercent returns how busy the CPUs were since the previous sample: of
// the container's CPU quota when it has one, else of all the machine's
// CPUs. A container's first sample has nothing to compare with.
func (s *Service) cpuPercent(ctx context.Context, now time.Time, limits cgroupUsage, limited bool) (float64, bool) {
	if limited && limits.CPULimit > 0 && limits.CPUTime > 0 {
		prev := s.prevCPU
		s.prevCPU = cpuTime{at: now, used: limits.CPUTime}
		elapsed := now.Sub(prev.at)
		if prev.at.IsZero() || elapsed <= 0 || limits.CPUTime < prev.used {
			return 0, false
		}
		busy := float64(limits.CPUTime-prev.used) / (float64(elapsed) * limits.CPULimit) * 100
		return min(busy, 100), true
	}
	// An interval of zero measures since the previous call, which only this
	// service makes.
	percents, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		logger.Error(ctx, "Failed to get CPU usage", tag.Error(err))
		return 0, false
	}
	if len(percents) == 0 {
		return 0, false
	}
	return percents[0], true
}

// diskUsage reads how full the disk that holds path is.
func diskUsage(ctx context.Context, path string) (*Usage, error) {
	stat, err := disk.UsageWithContext(ctx, path)
	if err != nil {
		return nil, err
	}
	usage := &Usage{UsedPercent: stat.UsedPercent, UsedBytes: stat.Used, TotalBytes: stat.Total}
	if stat.InodesTotal > 0 {
		inodes := stat.InodesUsedPercent
		usage.InodesUsedPercent = &inodes
	}
	return usage, nil
}

func percentOf(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
