// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proc"
)

func (s *ProcStore) latestCollectionHeartbeat(
	ctx context.Context,
	groupName string,
	dagRun ir.DAGRunRef,
) (*proc.ProcHeartbeat, error) {
	recs, err := s.listCollectionRecords(ctx, procGroupPrefix(groupName))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var latest *proc.ProcHeartbeat
	for _, rec := range recs {
		entry, err := s.entryFromRecord(rec, now)
		if err != nil {
			// Heartbeat observation is best-effort; ListEntries and Validate
			// still surface corrupt collection records.
			continue
		}
		if entry.GroupName != groupName || entry.Meta.Name != dagRun.Name || entry.Meta.DAGRunID != dagRun.ID {
			continue
		}
		heartbeat := entry.Heartbeat(rec.UpdatedAt)
		if latest == nil || heartbeat.PreferredTo(*latest) {
			latest = &heartbeat
		}
	}
	return latest, nil
}
