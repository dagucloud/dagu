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

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

const queueIndexVersion = 1

type queueReadIndex struct {
	Version  int      `json:"version"`
	Revision int64    `json:"revision"`
	High     []string `json:"high,omitempty"`
	Low      []string `json:"low,omitempty"`
}

type queueReadIndexCache struct {
	index         *queueReadIndex
	recordVersion string
}

type recordVersionCollection interface {
	RecordVersion(ctx context.Context, id string) (string, error)
}

func newQueueReadIndex() *queueReadIndex {
	return &queueReadIndex{
		Version:  queueIndexVersion,
		Revision: time.Now().UTC().UnixNano(),
		High:     []string{},
		Low:      []string{},
	}
}

func (idx *queueReadIndex) ensureDefaults() {
	if idx.Version == 0 {
		idx.Version = queueIndexVersion
	}
	if idx.Revision == 0 {
		idx.Revision = time.Now().UTC().UnixNano()
	}
	if idx.High == nil {
		idx.High = []string{}
	}
	if idx.Low == nil {
		idx.Low = []string{}
	}
	idx.High = normalizeQueueIndexEntries(idx.High)
	idx.Low = normalizeQueueIndexEntries(idx.Low)
}

func (idx *queueReadIndex) total() int {
	if idx == nil {
		return 0
	}
	return len(idx.High) + len(idx.Low)
}

func (idx *queueReadIndex) touch() {
	now := time.Now().UTC().UnixNano()
	if now <= idx.Revision {
		now = idx.Revision + 1
	}
	idx.Revision = now
}

func (idx *queueReadIndex) append(priority exec.QueuePriority, itemID string) bool {
	itemID = normalizeQueueItemID(itemID)
	if itemID == "" || idx.findItemOffset(itemID) >= 0 {
		return false
	}
	entry := queueIndexEntryName(itemID)
	switch priority {
	case exec.QueuePriorityHigh:
		idx.High = append(idx.High, entry)
		sort.Strings(idx.High)
	case exec.QueuePriorityLow:
		idx.Low = append(idx.Low, entry)
		sort.Strings(idx.Low)
	default:
		return false
	}
	idx.touch()
	return true
}

func (idx *queueReadIndex) removeItemID(itemID string) bool {
	itemID = normalizeQueueItemID(itemID)
	if itemID == "" {
		return false
	}
	removed := removeQueueItemID(&idx.High, itemID)
	if removeQueueItemID(&idx.Low, itemID) {
		removed = true
	}
	if removed {
		idx.touch()
	}
	return removed
}

func (idx *queueReadIndex) itemIDAt(offset int) (string, bool) {
	if offset < 0 {
		return "", false
	}
	if offset < len(idx.High) {
		return queueIndexItemID(idx.High[offset]), true
	}
	offset -= len(idx.High)
	if offset < len(idx.Low) {
		return queueIndexItemID(idx.Low[offset]), true
	}
	return "", false
}

func (idx *queueReadIndex) resolveStart(cursor queueReadCursor) (int, error) {
	if cursor.Offset < 0 {
		return 0, exec.ErrInvalidCursor
	}
	if cursor.AfterItemID == "" {
		if cursor.Offset != 0 {
			return 0, exec.ErrInvalidCursor
		}
		return 0, nil
	}

	if cursor.Offset > 0 {
		if itemID, ok := idx.itemIDAt(cursor.Offset - 1); ok && itemID == cursor.AfterItemID {
			return cursor.Offset, nil
		}
	}
	if offset := idx.findItemOffset(cursor.AfterItemID); offset >= 0 {
		return offset + 1, nil
	}
	return 0, exec.ErrInvalidCursor
}

func (idx *queueReadIndex) slice(start, limit int) []string {
	if start < 0 {
		start = 0
	}
	if limit <= 0 || start >= idx.total() {
		return nil
	}
	end := min(start+limit, idx.total())
	ret := make([]string, 0, end-start)
	for pos := start; pos < end; pos++ {
		itemID, ok := idx.itemIDAt(pos)
		if !ok {
			break
		}
		ret = append(ret, itemID)
	}
	return ret
}

func (idx *queueReadIndex) findItemOffset(itemID string) int {
	itemID = normalizeQueueItemID(itemID)
	for pos, current := range idx.High {
		if queueIndexItemID(current) == itemID {
			return pos
		}
	}
	for pos, current := range idx.Low {
		if queueIndexItemID(current) == itemID {
			return len(idx.High) + pos
		}
	}
	return -1
}

func removeQueueItemID(target *[]string, itemID string) bool {
	itemID = normalizeQueueItemID(itemID)
	for i, current := range *target {
		if queueIndexItemID(current) != itemID {
			continue
		}
		copy((*target)[i:], (*target)[i+1:])
		*target = (*target)[:len(*target)-1]
		return true
	}
	return false
}

func normalizeQueueIndexEntries(entries []string) []string {
	if len(entries) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		fileName := queueIndexEntryName(entry)
		if fileName == "" {
			continue
		}
		if _, ok := seen[fileName]; ok {
			continue
		}
		seen[fileName] = struct{}{}
		out = append(out, fileName)
	}
	sort.Strings(out)
	return out
}

func queueIndexEntryName(itemID string) string {
	itemID = normalizeQueueItemID(itemID)
	if itemID == "" {
		return ""
	}
	return itemID + ".json"
}

func queueIndexItemID(entry string) string {
	return normalizeQueueItemID(entry)
}

func queueIndexRecordID(name string) string {
	return queuePrefix(name) + ".queue-index"
}

func queuePriorityFromItemID(itemID string) exec.QueuePriority {
	itemID = normalizeQueueItemID(itemID)
	if strings.HasPrefix(itemID, "item_high_") {
		return exec.QueuePriorityHigh
	}
	return exec.QueuePriorityLow
}

func (s *QueueStore) loadOrRebuildQueueIndexLocked(ctx context.Context, name string) (*queueReadIndex, error) {
	cached, ok, err := s.cachedQueueIndexLocked(ctx, name)
	if err != nil {
		return nil, err
	}
	if ok {
		return cached, nil
	}

	rec, err := s.col.Get(ctx, queueIndexRecordID(name))
	if errors.Is(err, persis.ErrNotFound) {
		return s.rebuildQueueIndexLocked(ctx, name)
	}
	if err != nil {
		return nil, err
	}

	var loaded queueReadIndex
	if err := persis.Decode(rec, &loaded); err != nil {
		return s.rebuildQueueIndexLocked(ctx, name)
	}
	if loaded.Version != queueIndexVersion {
		return s.rebuildQueueIndexLocked(ctx, name)
	}
	loaded.ensureDefaults()
	s.cacheQueueIndexLocked(ctx, name, &loaded)
	return &loaded, nil
}

func (s *QueueStore) rebuildQueueIndexLocked(ctx context.Context, name string) (*queueReadIndex, error) {
	ids, err := s.queueRecordIDs(ctx, queueItemPrefix(name))
	if err != nil {
		return nil, err
	}

	idx := newQueueReadIndex()
	for _, id := range ids {
		_, itemID, ok := splitQueueRecordID(id)
		if !ok || itemID == "" {
			continue
		}
		idx.append(queuePriorityFromItemID(itemID), itemID)
	}
	idx.touch()

	if err := s.saveQueueIndexLocked(ctx, name, idx); err != nil {
		return nil, err
	}
	return idx, nil
}

func (s *QueueStore) saveQueueIndexLocked(ctx context.Context, name string, idx *queueReadIndex) error {
	if idx == nil {
		return nil
	}

	recordID := queueIndexRecordID(name)
	if idx.total() == 0 {
		delete(s.indices, name)
		return s.col.Delete(ctx, recordID)
	}

	idx.ensureDefaults()
	data, err := persis.Encode(idx)
	if err != nil {
		return fmt.Errorf("queue store: encode index: %w", err)
	}
	now := time.Now().UTC()
	if err := s.col.Put(ctx, &persis.Record{
		ID:        recordID,
		Data:      data,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return err
	}
	s.cacheQueueIndexLocked(ctx, name, idx)
	return nil
}

func (s *QueueStore) invalidateQueueIndexLocked(ctx context.Context, name string) {
	delete(s.indices, name)
	_ = s.col.Delete(ctx, queueIndexRecordID(name))
}

func (s *QueueStore) cachedQueueIndexLocked(ctx context.Context, name string) (*queueReadIndex, bool, error) {
	cached, ok := s.indices[name]
	if !ok || cached == nil || cached.index == nil {
		return nil, false, nil
	}
	version, ok, err := s.queueIndexRecordVersion(ctx, name)
	if !ok {
		return nil, false, nil
	}
	if errors.Is(err, persis.ErrNotFound) {
		delete(s.indices, name)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if version != cached.recordVersion {
		delete(s.indices, name)
		return nil, false, nil
	}
	return cached.index, true, nil
}

func (s *QueueStore) cacheQueueIndexLocked(ctx context.Context, name string, idx *queueReadIndex) {
	version, ok, err := s.queueIndexRecordVersion(ctx, name)
	if !ok || err != nil {
		delete(s.indices, name)
		return
	}
	s.indices[name] = &queueReadIndexCache{
		index:         idx,
		recordVersion: version,
	}
}

func (s *QueueStore) queueIndexRecordVersion(ctx context.Context, name string) (string, bool, error) {
	col, ok := s.col.(recordVersionCollection)
	if !ok {
		return "", false, nil
	}
	version, err := col.RecordVersion(ctx, queueIndexRecordID(name))
	return version, true, err
}

func (s *QueueStore) addQueueIndexItemLocked(ctx context.Context, name string, priority exec.QueuePriority, itemID string) {
	idx, err := s.loadOrRebuildQueueIndexLocked(ctx, name)
	if err != nil {
		s.invalidateQueueIndexLocked(ctx, name)
		return
	}
	if !idx.append(priority, itemID) {
		return
	}
	if err := s.saveQueueIndexLocked(ctx, name, idx); err != nil {
		s.invalidateQueueIndexLocked(ctx, name)
	}
}

func (s *QueueStore) removeQueueIndexItemsLocked(ctx context.Context, name string, itemIDs ...string) {
	if len(itemIDs) == 0 {
		return
	}
	idx, err := s.loadOrRebuildQueueIndexLocked(ctx, name)
	if err != nil {
		s.invalidateQueueIndexLocked(ctx, name)
		return
	}

	changed := false
	for _, itemID := range itemIDs {
		if idx.removeItemID(itemID) {
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := s.saveQueueIndexLocked(ctx, name, idx); err != nil {
		s.invalidateQueueIndexLocked(ctx, name)
	}
}

func (s *QueueStore) listCursorLocked(ctx context.Context, name string, cursor queueReadCursor, limit int) (exec.CursorResult[exec.QueuedItemData], error) {
	idx, err := s.loadOrRebuildQueueIndexLocked(ctx, name)
	if err != nil {
		return exec.CursorResult[exec.QueuedItemData]{}, err
	}

	for attempt := range 2 {
		start, err := idx.resolveStart(cursor)
		if err != nil {
			return exec.CursorResult[exec.QueuedItemData]{}, err
		}

		itemIDs := idx.slice(start, limit)
		items, missing, err := s.queueItemsByID(ctx, name, itemIDs)
		if err != nil {
			return exec.CursorResult[exec.QueuedItemData]{}, err
		}
		if missing && attempt == 0 {
			idx, err = s.rebuildQueueIndexLocked(ctx, name)
			if err != nil {
				return exec.CursorResult[exec.QueuedItemData]{}, err
			}
			continue
		}

		hasMore := start+len(itemIDs) < idx.total()
		nextCursor := ""
		if hasMore && len(itemIDs) > 0 {
			nextCursor = encodeQueueCursor(name, start+len(itemIDs), itemIDs[len(itemIDs)-1])
		}
		return exec.CursorResult[exec.QueuedItemData]{
			Items:      items,
			HasMore:    hasMore,
			NextCursor: nextCursor,
		}, nil
	}

	return exec.CursorResult[exec.QueuedItemData]{Items: []exec.QueuedItemData{}}, nil
}

func (s *QueueStore) queueItemsByID(ctx context.Context, name string, itemIDs []string) ([]exec.QueuedItemData, bool, error) {
	items := make([]exec.QueuedItemData, 0, len(itemIDs))
	missing := false
	for _, itemID := range itemIDs {
		recordID := queueRecordID(name, itemID)
		rec, err := s.col.Get(ctx, recordID)
		if errors.Is(err, persis.ErrNotFound) {
			missing = true
			continue
		}
		if err != nil {
			item, invalidErr := invalidQueueItemFromRecordID(recordID, err)
			if invalidErr != nil {
				return nil, false, invalidErr
			}
			items = append(items, item)
			continue
		}
		item, err := queueItemFromRecord(rec)
		if err != nil {
			return nil, false, err
		}
		items = append(items, item)
	}
	return items, missing, nil
}
