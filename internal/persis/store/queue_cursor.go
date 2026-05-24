// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import "github.com/dagucloud/dagu/internal/core/exec"

type queueReadCursor struct {
	Version     int    `json:"version"`
	Queue       string `json:"queue"`
	Offset      int    `json:"offset"`
	AfterItemID string `json:"afterItemId"`
}

func encodeQueueCursor(name string, offset int, itemID string) string {
	if itemID == "" {
		return ""
	}
	return exec.EncodeSearchCursor(queueReadCursor{
		Version:     queueCursorVersion,
		Queue:       name,
		Offset:      offset,
		AfterItemID: itemID,
	})
}

func decodeQueueCursor(name, raw string) (queueReadCursor, error) {
	if raw == "" {
		return queueReadCursor{Version: queueCursorVersion, Queue: name}, nil
	}
	var cursor queueReadCursor
	if err := exec.DecodeSearchCursor(raw, &cursor); err != nil {
		return queueReadCursor{}, err
	}
	if cursor.Version != queueCursorVersion || cursor.Queue != name {
		return queueReadCursor{}, exec.ErrInvalidCursor
	}
	return cursor, nil
}

func resolveQueueCursorStart(items []*queueItem, cursor queueReadCursor) (int, error) {
	if cursor.Offset < 0 {
		return 0, exec.ErrInvalidCursor
	}
	if cursor.AfterItemID == "" {
		if cursor.Offset != 0 {
			return 0, exec.ErrInvalidCursor
		}
		return 0, nil
	}
	if cursor.Offset > 0 && cursor.Offset <= len(items) && items[cursor.Offset-1].ID() == cursor.AfterItemID {
		return cursor.Offset, nil
	}
	for i, item := range items {
		if item.ID() == cursor.AfterItemID {
			return i + 1, nil
		}
	}
	return 0, exec.ErrInvalidCursor
}
