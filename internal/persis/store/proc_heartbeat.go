// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
)

func procHeartbeatFromEntry(entry exec.ProcEntry, observedAt time.Time) exec.ProcHeartbeat {
	return exec.ProcHeartbeat{
		GroupName:       entry.GroupName,
		DAGRun:          entry.Meta.DAGRun(),
		AttemptID:       entry.Meta.AttemptID,
		StartedAt:       entry.Meta.StartedAt,
		LastHeartbeatAt: entry.LastHeartbeatAt,
		ObservedAt:      observedAt,
		Fresh:           entry.Fresh,
	}
}

func (s *ProcStore) latestCollectionHeartbeat(
	ctx context.Context,
	groupName string,
	dagRun exec.DAGRunRef,
) (*exec.ProcHeartbeat, error) {
	recs, err := s.listCollectionRecords(ctx, procGroupPrefix(groupName))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var latest *exec.ProcHeartbeat
	for _, rec := range recs {
		entry, err := s.entryFromRecord(rec, now)
		if err != nil {
			return nil, err
		}
		if entry.GroupName != groupName || entry.Meta.Name != dagRun.Name || entry.Meta.DAGRunID != dagRun.ID {
			continue
		}
		heartbeat := procHeartbeatFromEntry(entry, rec.UpdatedAt)
		if latest == nil || procHeartbeatPreferred(heartbeat, *latest) {
			latest = &heartbeat
		}
	}
	return latest, nil
}

func procHeartbeatPreferred(candidate, existing exec.ProcHeartbeat) bool {
	if candidate.Fresh != existing.Fresh {
		return candidate.Fresh
	}
	if candidate.StartedAt != existing.StartedAt {
		return candidate.StartedAt > existing.StartedAt
	}
	if candidate.LastHeartbeatAt != existing.LastHeartbeatAt {
		return candidate.LastHeartbeatAt > existing.LastHeartbeatAt
	}
	if !candidate.ObservedAt.Equal(existing.ObservedAt) {
		return candidate.ObservedAt.After(existing.ObservedAt)
	}
	return candidate.AttemptID < existing.AttemptID
}
