// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/dagucloud/dagu/internal/service/eventstore"
)

const queryCursorVersion = 1

type queryCursorPayload struct {
	Version    int    `json:"v"`
	FilterHash string `json:"f"`
	File       string `json:"file"`
	Offset     int64  `json:"offset"`
}

func encodeQueryCursor(filter eventstore.QueryFilter, file string, offset int64) (string, error) {
	payload := queryCursorPayload{
		Version:    queryCursorVersion,
		FilterHash: queryFilterHash(filter),
		File:       file,
		Offset:     offset,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("fileeventstore: marshal query cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeQueryCursor(cursor string, filter eventstore.QueryFilter) (queryCursorPayload, error) {
	if cursor == "" {
		return queryCursorPayload{}, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return queryCursorPayload{}, invalidQueryCursor("decode cursor")
	}

	var payload queryCursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return queryCursorPayload{}, invalidQueryCursor("parse cursor")
	}
	if payload.Version != queryCursorVersion {
		return queryCursorPayload{}, invalidQueryCursor("unsupported cursor version")
	}
	if payload.FilterHash == "" || payload.File == "" || payload.Offset < 0 {
		return queryCursorPayload{}, invalidQueryCursor("cursor is incomplete")
	}
	if payload.FilterHash != queryFilterHash(filter) {
		return queryCursorPayload{}, invalidQueryCursor("cursor does not match the current filters")
	}

	return payload, nil
}

func queryFilterHash(filter eventstore.QueryFilter) string {
	normalized := struct {
		Kind      string `json:"kind,omitempty"`
		Type      string `json:"type,omitempty"`
		DAGName   string `json:"dag_name,omitempty"`
		DAGRunID  string `json:"dag_run_id,omitempty"`
		AttemptID string `json:"attempt_id,omitempty"`
		SessionID string `json:"session_id,omitempty"`
		UserID    string `json:"user_id,omitempty"`
		Model     string `json:"model,omitempty"`
		Status    string `json:"status,omitempty"`
		StartTime string `json:"start_time,omitempty"`
		EndTime   string `json:"end_time,omitempty"`
	}{
		Kind:      string(filter.Kind),
		Type:      string(filter.Type),
		DAGName:   filter.DAGName,
		DAGRunID:  filter.DAGRunID,
		AttemptID: filter.AttemptID,
		SessionID: filter.SessionID,
		UserID:    filter.UserID,
		Model:     filter.Model,
		Status:    filter.Status,
	}
	if !filter.StartTime.IsZero() {
		normalized.StartTime = filter.StartTime.UTC().Format(jsonTimeLayout)
	}
	if !filter.EndTime.IsZero() {
		normalized.EndTime = filter.EndTime.UTC().Format(jsonTimeLayout)
	}

	data, _ := json.Marshal(normalized)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func invalidQueryCursor(reason string) error {
	return fmt.Errorf("%w: %s", eventstore.ErrInvalidQueryCursor, reason)
}

const jsonTimeLayout = "2006-01-02T15:04:05.999999999Z07:00"
