// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
)

func (s *ProcStore) listCollectionEntries(ctx context.Context, groupName string) ([]proc.ProcEntry, error) {
	prefix := ""
	if groupName != "" {
		prefix = procGroupPrefix(groupName)
	}
	recs, err := s.listCollectionRecords(ctx, prefix)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	entries := make([]proc.ProcEntry, 0, len(recs))
	for _, rec := range recs {
		entry, err := s.entryFromRecord(rec, now)
		if err != nil {
			return nil, err
		}
		if groupName != "" && entry.GroupName != groupName {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *ProcStore) listCollectionRecords(ctx context.Context, prefix string) ([]*persis.Record, error) {
	if col, ok := s.col.(recordIDsCollection); ok {
		ids, err := col.RecordIDs(ctx, prefix)
		if err != nil {
			return nil, err
		}
		recs := make([]*persis.Record, 0, len(ids))
		for _, id := range ids {
			rec, err := s.col.Get(ctx, id)
			if errors.Is(err, persis.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			recs = append(recs, rec)
		}
		return recs, nil
	}
	return listAll(ctx, s.col, persis.ListQuery{Prefix: prefix})
}

func (s *ProcStore) entryFromRecord(rec *persis.Record, now time.Time) (proc.ProcEntry, error) {
	var payload procPayload
	if err := persis.Decode(rec, &payload); err != nil {
		return proc.ProcEntry{}, fmt.Errorf("proc store: decode %q: %w", rec.ID, err)
	}
	if payload.Version != procStoreVersion {
		return proc.ProcEntry{}, fmt.Errorf("proc store: unsupported record version %d for %q", payload.Version, rec.ID)
	}
	if err := payload.Meta.Validate(); err != nil {
		return proc.ProcEntry{}, fmt.Errorf("proc store: invalid metadata in %q: %w", rec.ID, err)
	}
	recordGroupName := procGroupNameFromRecordID(rec.ID)
	if recordGroupName == "" {
		return proc.ProcEntry{}, fmt.Errorf("proc store: invalid record ID %q", rec.ID)
	}
	groupName := payload.GroupName
	if groupName == "" {
		groupName = recordGroupName
	} else if groupName != recordGroupName {
		return proc.ProcEntry{}, fmt.Errorf("proc store: record group mismatch for %q: payload %q, path %q", rec.ID, groupName, recordGroupName)
	}
	heartbeatAt := time.Unix(payload.LastHeartbeatAt, 0).UTC()
	if heartbeatAt.After(now.Add(5 * time.Minute)) {
		return proc.ProcEntry{}, fmt.Errorf("proc store: heartbeat timestamp is in the future for %q", rec.ID)
	}
	fresh := now.Sub(heartbeatAt) < s.staleTime
	if !fresh && now.Sub(rec.UpdatedAt) < s.staleTime {
		fresh = true
	}
	return proc.ProcEntry{
		GroupName:       groupName,
		Identity:        collectionProcEntryID(rec.ID),
		Meta:            payload.Meta,
		LastHeartbeatAt: payload.LastHeartbeatAt,
		Fresh:           fresh,
	}, nil
}

func (s *ProcStore) removeCollectionIfStale(ctx context.Context, entry proc.ProcEntry) error {
	recordID, ok := entry.Identity.StoreValue(procEntryIdentityCollection)
	if !ok {
		return nil
	}
	currentRec, err := s.col.Get(ctx, recordID)
	if errors.Is(err, persis.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := s.entryFromRecord(currentRec, time.Now().UTC())
	if err != nil {
		return err
	}
	if current.Fresh || !current.SameObservation(entry) {
		return nil
	}
	if err := s.col.CompareAndDelete(ctx, currentRec); errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
		return nil
	} else if err != nil {
		return err
	}

	logger.Info(ctx, "Removed stale proc record", tag.Name(recordID))
	return nil
}

func dedupeAndSortProcEntries(entries []proc.ProcEntry) []proc.ProcEntry {
	byKey := make(map[string]proc.ProcEntry)
	for _, entry := range entries {
		key := entry.AttemptKey()
		if key == "" || strings.Count(key, "|") < 4 {
			key = entry.GroupName + "|" + procEntrySortKey(entry)
		}
		existing, ok := byKey[key]
		if !ok || procEntryPreferred(entry, existing) {
			byKey[key] = entry
		}
	}
	out := make([]proc.ProcEntry, 0, len(byKey))
	for _, entry := range byKey {
		out = append(out, entry)
	}
	sortProcEntries(out)
	return out
}

func procEntryPreferred(candidate, existing proc.ProcEntry) bool {
	if candidate.Fresh != existing.Fresh {
		return candidate.Fresh
	}
	if candidate.LastHeartbeatAt != existing.LastHeartbeatAt {
		return candidate.LastHeartbeatAt > existing.LastHeartbeatAt
	}
	return procEntrySortKey(candidate) < procEntrySortKey(existing)
}

func sortProcEntries(entries []proc.ProcEntry) {
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if left.GroupName != right.GroupName {
			return left.GroupName < right.GroupName
		}
		if left.Meta.Name != right.Meta.Name {
			return left.Meta.Name < right.Meta.Name
		}
		if left.Meta.StartedAt != right.Meta.StartedAt {
			return left.Meta.StartedAt < right.Meta.StartedAt
		}
		if left.LastHeartbeatAt != right.LastHeartbeatAt {
			return left.LastHeartbeatAt < right.LastHeartbeatAt
		}
		return procEntrySortKey(left) < procEntrySortKey(right)
	})
}

func procGroupPrefix(groupName string) string {
	if groupName == "" {
		return ""
	}
	return groupName + "/"
}

func procGroupNameFromRecordID(id string) string {
	before, _, ok := strings.Cut(id, "/")
	if !ok {
		return ""
	}
	return before
}
