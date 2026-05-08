// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
	"github.com/dagucloud/dagu/internal/service/audit"
)

var _ audit.Store = (*auditStore)(nil)

type auditStore struct{ store *Store }

// Audit returns the audit store.
func (s *Store) Audit() audit.Store {
	return &auditStore{store: s}
}

func (s *auditStore) Append(ctx context.Context, entry *audit.Entry) error {
	if entry == nil {
		return fmt.Errorf("postgres audit store: entry cannot be nil")
	}
	idString, id, err := ensureUUIDv7String(entry.ID)
	if err != nil {
		return err
	}
	entry.ID = idString
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}
	return s.store.queries.AppendAuditEntry(ctx, db.AppendAuditEntryParams{
		ID:         id,
		OccurredAt: timestamptz(entry.Timestamp),
		Category:   string(entry.Category),
		Action:     entry.Action,
		UserID:     entry.UserID,
		Username:   entry.Username,
		IpAddress:  entry.IPAddress,
		Data:       data,
	})
}

func (s *auditStore) Query(ctx context.Context, filter audit.QueryFilter) (*audit.QueryResult, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := max(filter.Offset, 0)
	params := auditQueryParams(filter, limit, offset)
	rows, err := s.store.queries.QueryAuditEntries(ctx, params)
	if err != nil {
		return nil, err
	}
	total, err := s.store.queries.CountAuditEntries(ctx, db.CountAuditEntriesParams{
		HasCategory:  params.HasCategory,
		Category:     params.Category,
		HasUserID:    params.HasUserID,
		UserID:       params.UserID,
		HasStartTime: params.HasStartTime,
		StartTime:    params.StartTime,
		HasEndTime:   params.HasEndTime,
		EndTime:      params.EndTime,
	})
	if err != nil {
		return nil, err
	}

	entries := make([]*audit.Entry, 0, len(rows))
	for _, row := range rows {
		entry, err := auditEntryFromRow(row)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return &audit.QueryResult{Entries: entries, Total: int(total)}, nil
}

func auditQueryParams(filter audit.QueryFilter, limit, offset int) db.QueryAuditEntriesParams {
	return db.QueryAuditEntriesParams{
		HasCategory:  filter.Category != "",
		Category:     string(filter.Category),
		HasUserID:    filter.UserID != "",
		UserID:       filter.UserID,
		HasStartTime: !filter.StartTime.IsZero(),
		StartTime:    timestamptz(filter.StartTime),
		HasEndTime:   !filter.EndTime.IsZero(),
		EndTime:      timestamptz(filter.EndTime),
		RowLimit:     clampInt32(limit),
		RowOffset:    clampInt32(offset),
	}
}

func clampInt32(value int) int32 {
	const maxInt32 = int64(1<<31 - 1)
	if value <= 0 {
		return 0
	}
	if int64(value) > maxInt32 {
		return int32(maxInt32)
	}
	return int32(value) //nolint:gosec
}

func auditEntryFromRow(row db.DaguAuditEntry) (*audit.Entry, error) {
	var entry audit.Entry
	if err := json.Unmarshal(row.Data, &entry); err != nil {
		return nil, fmt.Errorf("unmarshal audit entry: %w", err)
	}
	entry.ID = row.ID.String()
	entry.Timestamp = timeFromTimestamptz(row.OccurredAt)
	entry.Category = audit.Category(row.Category)
	entry.Action = row.Action
	entry.UserID = row.UserID
	entry.Username = row.Username
	entry.IPAddress = row.IpAddress.String
	return &entry, nil
}
