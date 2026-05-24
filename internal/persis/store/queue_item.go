// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

var (
	queueItemIDPattern = regexp.MustCompile(`^item_(high|low)_(\d{8}_\d{6})_(\d{9})Z_(.*)$`)
)

type queueItemPayload struct {
	FileName string         `json:"fileName"`
	DAGRun   exec.DAGRunRef `json:"dagRun"`
	QueuedAt time.Time      `json:"queuedAt"`
}

type queueItem struct {
	id       string
	queue    string
	priority exec.QueuePriority
	queuedAt time.Time
	dagRun   exec.DAGRunRef
	recordID string
	dataErr  error
}

var _ exec.QueuedItemData = (*queueItem)(nil)

func (i *queueItem) ID() string {
	if i == nil {
		return ""
	}
	return i.id
}

func (i *queueItem) Data() (*exec.DAGRunRef, error) {
	if i == nil {
		return nil, fmt.Errorf("queue item is nil")
	}
	if i.dataErr != nil {
		return nil, i.dataErr
	}
	ref := i.dagRun
	return &ref, nil
}

func queueItemFromRecord(rec *persis.Record) (*queueItem, error) {
	if rec == nil {
		return nil, fmt.Errorf("queue store: nil record")
	}
	queueName, fallbackItemID, ok := splitQueueRecordID(rec.ID)
	if !ok {
		return nil, fmt.Errorf("queue store: invalid record ID %q", rec.ID)
	}

	var payload queueItemPayload
	if err := persis.Decode(rec, &payload); err != nil {
		return invalidQueueItem(queueName, fallbackItemID, rec.ID, rec.CreatedAt, fmt.Errorf("queue store: decode %q: %w", rec.ID, err)), nil
	}

	itemID := normalizeQueueItemID(payload.FileName)
	if itemID == "" {
		itemID = fallbackItemID
	}
	priority, queuedAt := queueItemMetadata(itemID, rec.CreatedAt)
	if !payload.QueuedAt.IsZero() {
		queuedAt = payload.QueuedAt.UTC()
	}
	if payload.DAGRun.Name == "" || payload.DAGRun.ID == "" {
		return &queueItem{
			id:       itemID,
			queue:    queueName,
			priority: priority,
			queuedAt: queuedAt,
			recordID: rec.ID,
			dataErr:  fmt.Errorf("queue store: invalid dag-run in %q", rec.ID),
		}, nil
	}

	return &queueItem{
		id:       itemID,
		queue:    queueName,
		priority: priority,
		queuedAt: queuedAt,
		dagRun:   payload.DAGRun,
		recordID: rec.ID,
	}, nil
}

func invalidQueueItem(queueName, itemID, recordID string, fallback time.Time, err error) *queueItem {
	priority, queuedAt := queueItemMetadata(itemID, fallback)
	return &queueItem{
		id:       itemID,
		queue:    queueName,
		priority: priority,
		queuedAt: queuedAt,
		recordID: recordID,
		dataErr:  err,
	}
}

func invalidQueueItemFromRecordID(recordID string, err error) (*queueItem, error) {
	queueName, itemID, ok := splitQueueRecordID(recordID)
	if !ok {
		return nil, fmt.Errorf("queue store: invalid record ID %q", recordID)
	}
	return invalidQueueItem(queueName, itemID, recordID, time.Time{}, err), nil
}

func queueItemsAsData(items []*queueItem) []exec.QueuedItemData {
	out := make([]exec.QueuedItemData, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func sortQueueItems(items []*queueItem) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.priority != right.priority {
			return left.priority < right.priority
		}
		if left.queue != right.queue {
			return left.queue < right.queue
		}
		if !left.queuedAt.Equal(right.queuedAt) {
			return left.queuedAt.Before(right.queuedAt)
		}
		return left.id < right.id
	})
}

func queuePrefix(name string) string {
	if name == "" {
		return ""
	}
	return name + "/"
}

func queueItemPrefix(name string) string {
	return queuePrefix(name) + "item_"
}

func queueNameFromItemRecordID(id string) (string, bool) {
	queueName, itemID, ok := splitQueueRecordID(id)
	if !ok || !strings.HasPrefix(itemID, "item_") {
		return "", false
	}
	return queueName, true
}

func isQueueItemRecordID(id string) bool {
	_, ok := queueNameFromItemRecordID(id)
	return ok
}

func queueRecordID(name, itemID string) string {
	return queuePrefix(name) + normalizeQueueItemID(itemID)
}

func splitQueueRecordID(id string) (queueName, itemID string, ok bool) {
	idx := strings.LastIndex(id, "/")
	if idx < 0 || idx == len(id)-1 {
		return "", "", false
	}
	return id[:idx], normalizeQueueItemID(id[idx+1:]), true
}

func normalizeQueueItemID(itemID string) string {
	itemID = strings.TrimSpace(itemID)
	itemID = filepath.Base(itemID)
	itemID = strings.TrimSuffix(itemID, ".json")
	if itemID == "." || itemID == string(filepath.Separator) {
		return ""
	}
	return itemID
}

func newQueueItemID(priority exec.QueuePriority, dagRunID string, t time.Time) string {
	label := "low"
	if priority == exec.QueuePriorityHigh {
		label = "high"
	}
	t = t.UTC()
	return fmt.Sprintf("item_%s_%s_%09dZ_%s_%s",
		label,
		t.Format(queueDateTimeUTC),
		t.Nanosecond(),
		dagRunID,
		uuid.NewString(),
	)
}

func queueItemMetadata(itemID string, fallback time.Time) (exec.QueuePriority, time.Time) {
	priority := exec.QueuePriorityLow
	queuedAt := fallback.UTC()
	matches := queueItemIDPattern.FindStringSubmatch(itemID)
	if len(matches) != 5 {
		return priority, queuedAt
	}
	if matches[1] == "high" {
		priority = exec.QueuePriorityHigh
	}
	parsed, err := time.Parse(queueDateTimeUTC, matches[2])
	if err != nil {
		return priority, queuedAt
	}
	nanos, err := time.ParseDuration(matches[3] + "ns")
	if err != nil {
		return priority, queuedAt
	}
	return priority, parsed.Add(nanos).UTC()
}
