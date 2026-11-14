// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"sync"
	"time"
)

// MetricPoint represents a single data point for a metric
type MetricPoint struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// ResourceHistory holds the history of resource usage
type ResourceHistory struct {
	CPU              []MetricPoint `json:"cpu"`
	Memory           []MetricPoint `json:"memory"`
	Disk             []MetricPoint `json:"disk"`
	Load             []MetricPoint `json:"load"`
	MemoryTotalBytes uint64        `json:"memoryTotalBytes"`
	MemoryUsedBytes  uint64        `json:"memoryUsedBytes"`
	DiskTotalBytes   uint64        `json:"diskTotalBytes"`
	DiskUsedBytes    uint64        `json:"diskUsedBytes"`
}

// Store defines the interface for storing resource metrics
type Store interface {
	Add(cpu, memory, disk, load float64, memTotal, memUsed, diskTotal, diskUsed uint64)
	GetHistory(duration time.Duration) *ResourceHistory
	// DiskUsedPercent returns the latest disk usage in percent, and false
	// before any disk usage has been recorded.
	DiskUsedPercent() (float64, bool)
}

// dataPoint stores all metrics for a single timestamp
type dataPoint struct {
	Timestamp int64
	CPU       float64
	Memory    float64
	Disk      float64
	Load      float64
}

// MemoryStore implements Store using a generic ring buffer
type MemoryStore struct {
	mu               sync.RWMutex
	buffer           *RingBuffer[dataPoint]
	memoryTotalBytes uint64
	memoryUsedBytes  uint64
	diskTotalBytes   uint64
	diskUsedBytes    uint64
	diskUsedPercent  float64
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore creates a MemoryStore configured for the given retention and sampling interval.
// If interval is less than or equal to zero, it defaults to 10 seconds.
// The ring buffer capacity is computed as int(retention/interval) + 10.
func NewMemoryStore(retention, interval time.Duration) *MemoryStore {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	capacity := int(retention/interval) + 10
	return &MemoryStore{
		buffer: NewRingBuffer[dataPoint](capacity),
	}
}

// Add writes a new data point. Zero allocations, O(1).
func (s *MemoryStore) Add(cpu, memory, disk, load float64, memTotal, memUsed, diskTotal, diskUsed uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.buffer.Push(dataPoint{
		Timestamp: time.Now().Unix(),
		CPU:       cpu,
		Memory:    memory,
		Disk:      disk,
		Load:      load,
	})
	s.memoryTotalBytes = memTotal
	s.memoryUsedBytes = memUsed
	s.diskTotalBytes = diskTotal
	s.diskUsedBytes = diskUsed
	s.diskUsedPercent = disk
}

// DiskUsedPercent implements Store.
func (s *MemoryStore) DiskUsedPercent() (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// A failed disk read records a zero total.
	return s.diskUsedPercent, s.diskTotalBytes > 0
}

// GetHistory returns metrics for the specified duration
func (s *MemoryStore) GetHistory(duration time.Duration) *ResourceHistory {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.buffer.Len() == 0 {
		return &ResourceHistory{}
	}

	cutoff := time.Now().Add(-duration).Unix()

	// Count matching points for pre-allocation
	n := 0
	s.buffer.ForEach(func(p *dataPoint) bool {
		if p.Timestamp >= cutoff {
			n++
		}
		return true
	})

	if n == 0 {
		return &ResourceHistory{
			MemoryTotalBytes: s.memoryTotalBytes,
			MemoryUsedBytes:  s.memoryUsedBytes,
			DiskTotalBytes:   s.diskTotalBytes,
			DiskUsedBytes:    s.diskUsedBytes,
		}
	}

	cpu := make([]MetricPoint, 0, n)
	memory := make([]MetricPoint, 0, n)
	disk := make([]MetricPoint, 0, n)
	load := make([]MetricPoint, 0, n)

	s.buffer.ForEach(func(p *dataPoint) bool {
		if p.Timestamp >= cutoff {
			cpu = append(cpu, MetricPoint{Timestamp: p.Timestamp, Value: p.CPU})
			memory = append(memory, MetricPoint{Timestamp: p.Timestamp, Value: p.Memory})
			disk = append(disk, MetricPoint{Timestamp: p.Timestamp, Value: p.Disk})
			load = append(load, MetricPoint{Timestamp: p.Timestamp, Value: p.Load})
		}
		return true
	})

	return &ResourceHistory{
		CPU:              cpu,
		Memory:           memory,
		Disk:             disk,
		Load:             load,
		MemoryTotalBytes: s.memoryTotalBytes,
		MemoryUsedBytes:  s.memoryUsedBytes,
		DiskTotalBytes:   s.diskTotalBytes,
		DiskUsedBytes:    s.diskUsedBytes,
	}
}
