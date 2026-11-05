// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// Limits on the workers one report lists; workers_total counts them all.
const (
	maxWorkers = 256
	// maxWorkersBytes bounds the encoded workers, whose IDs and labels have
	// no length limit. With the budgets of finished runs, runs in progress,
	// schedules, and queues, the report stays under the 1 MiB that Dagu
	// Console accepts.
	maxWorkersBytes = 64 << 10
)

// Workers lists the presence records the coordinator keeps from distributed
// workers' heartbeats.
type Workers interface {
	List(ctx context.Context) ([]dispatch.WorkerHeartbeatRecord, error)
}

// workerHealth is a distributed worker as its latest heartbeat describes it.
type workerHealth struct {
	ID              string            `json:"id"`
	Host            string            `json:"host,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	TotalPollers    int32             `json:"total_pollers"`
	BusyPollers     int32             `json:"busy_pollers"`
	LastHeartbeatAt string            `json:"last_heartbeat_at"`
	Resources       *resources        `json:"resources,omitempty"`
}

// readWorkers returns the workers whose last heartbeat is fresh, by ID and at
// most maxWorkers and maxWorkersBytes of them when list is set, and how many
// there are. Both are
// nil while the workers are unknown: without a worker store, without a
// registered coordinator, which keeps the store current, or when either
// cannot be read.
func (r *Reporter) readWorkers(ctx context.Context, now time.Time, services []service, list bool) ([]workerHealth, *int) {
	if r.workers == nil || !hasCoordinator(services) {
		return nil, nil
	}
	records, err := r.workers.List(ctx)
	if err != nil {
		logger.Debug(ctx, "Failed to read workers for the server report", tag.Error(err))
		return nil, nil
	}
	cutoff := now.Add(-dispatch.DefaultStaleWorkerHeartbeatThreshold)
	live := slices.DeleteFunc(records, func(rec dispatch.WorkerHeartbeatRecord) bool {
		return rec.LastHeartbeatTime().Before(cutoff)
	})
	total := len(live)
	if !list {
		return nil, &total
	}
	slices.SortFunc(live, func(a, b dispatch.WorkerHeartbeatRecord) int { return cmp.Compare(a.WorkerID, b.WorkerID) })
	workers := make([]workerHealth, 0, min(total, maxWorkers))
	size := 0
	for _, rec := range live[:min(total, maxWorkers)] {
		w := workerHealth{ID: rec.WorkerID, Host: rec.Host, Labels: rec.Labels,
			LastHeartbeatAt: formatTime(rec.LastHeartbeatTime()), Resources: workerResourcesOf(rec.Machine)}
		if s := rec.Stats; s != nil {
			w.TotalPollers, w.BusyPollers = s.TotalPollers, s.BusyPollers
		}
		encoded, err := json.Marshal(w)
		if err != nil || size+len(encoded)+1 > maxWorkersBytes {
			break
		}
		size += len(encoded) + 1
		workers = append(workers, w)
	}
	return workers, &total
}

// hasCoordinator reports whether services count a registered coordinator.
func hasCoordinator(services []service) bool {
	return slices.ContainsFunc(services, func(s service) bool {
		return s.Name == string(serviceregistry.ServiceNameCoordinator) && s.Instances > 0
	})
}

// workerResourcesOf is the report's form of a worker's machine sample.
func workerResourcesOf(m *dispatch.MachineSample) *resources {
	if m == nil {
		return nil
	}
	r := &resources{CPUs: m.CPUs, CPULimit: m.CPULimit, CPUPercent: m.CPUPercent, CPUPeakPercent: m.CPUPeakPercent,
		Memory: workerUsageOf(m.Memory), Disk: workerUsageOf(m.Disk), LogDisk: workerUsageOf(m.LogDisk)}
	if l := m.Load; l != nil {
		r.Load = &loadAverage{Load1: l.Load1, Load5: l.Load5, Load15: l.Load15}
	}
	return r
}

func workerUsageOf(u *dispatch.MachineUsage) *usage {
	if u == nil {
		return nil
	}
	return &usage{UsedPercent: u.UsedPercent, UsedBytes: u.UsedBytes, TotalBytes: u.TotalBytes, Limited: u.Limited,
		InodesUsedPercent: u.InodesUsedPercent}
}
