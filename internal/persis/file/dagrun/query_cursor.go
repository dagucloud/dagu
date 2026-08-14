// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

// queryCursorVersion 3 binds cursors to workspace visibility filters.
const queryCursorVersion = 3

type queryCursorPayload struct {
	Version    int    `json:"v"`
	FilterHash string `json:"f"`
	Timestamp  string `json:"ts"`
	Name       string `json:"n"`
	DAGRunID   string `json:"r"`
}

func encodeQueryCursor(opts persis.DAGRunStatusQuery, key dagRunListKey) (string, error) {
	payload := queryCursorPayload{
		Version:    queryCursorVersion,
		FilterHash: queryFilterHash(opts),
		Timestamp:  key.Timestamp.UTC().Format(time.RFC3339Nano),
		Name:       key.Name,
		DAGRunID:   key.DAGRunID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("dagrun: marshal query cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeQueryCursor(cursor string, opts persis.DAGRunStatusQuery) (dagRunListKey, error) {
	if cursor == "" {
		return dagRunListKey{}, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return dagRunListKey{}, invalidQueryCursor("decode cursor")
	}

	var payload queryCursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return dagRunListKey{}, invalidQueryCursor("parse cursor")
	}
	if payload.Version != queryCursorVersion {
		return dagRunListKey{}, invalidQueryCursor("unsupported cursor version")
	}
	if payload.FilterHash == "" || payload.Timestamp == "" || payload.Name == "" || payload.DAGRunID == "" {
		return dagRunListKey{}, invalidQueryCursor("cursor is incomplete")
	}
	if payload.FilterHash != queryFilterHash(opts) {
		return dagRunListKey{}, invalidQueryCursor("cursor does not match the current filters")
	}

	ts, err := time.Parse(time.RFC3339Nano, payload.Timestamp)
	if err != nil {
		return dagRunListKey{}, invalidQueryCursor("invalid cursor timestamp")
	}

	return dagRunListKey{
		Timestamp: ts.UTC(),
		Name:      payload.Name,
		DAGRunID:  payload.DAGRunID,
	}, nil
}

func queryFilterHash(opts persis.DAGRunStatusQuery) string {
	statuses := make([]int, 0, len(opts.Statuses))
	for _, status := range opts.Statuses {
		statuses = append(statuses, int(status))
	}
	sort.Ints(statuses)

	labels := append([]string(nil), opts.Labels...)
	sort.Strings(labels)
	var workspaces []string
	var workspaceEnabled bool
	var includeUnlabelled bool
	if opts.WorkspaceFilter != nil && opts.WorkspaceFilter.Enabled {
		workspaceEnabled = true
		workspaces = append([]string(nil), opts.WorkspaceFilter.Workspaces...)
		sort.Strings(workspaces)
		includeUnlabelled = opts.WorkspaceFilter.IncludeUnlabelled
	}

	normalized := struct {
		DAGRunID          string   `json:"dag_run_id,omitempty"`
		Name              string   `json:"name,omitempty"`
		ExactName         string   `json:"exact_name,omitempty"`
		From              string   `json:"from,omitempty"`
		To                string   `json:"to,omitempty"`
		Statuses          []int    `json:"statuses,omitempty"`
		Labels            []string `json:"labels,omitempty"`
		WorkspaceEnabled  bool     `json:"workspace_enabled,omitempty"`
		Workspaces        []string `json:"workspaces,omitempty"`
		IncludeUnlabelled bool     `json:"include_unlabelled,omitempty"`
	}{
		DAGRunID:          opts.DAGRunID,
		Name:              opts.Name,
		ExactName:         opts.ExactName,
		Statuses:          statuses,
		Labels:            labels,
		WorkspaceEnabled:  workspaceEnabled,
		Workspaces:        workspaces,
		IncludeUnlabelled: includeUnlabelled,
	}
	if !opts.From.IsZero() {
		normalized.From = opts.From.UTC().Format(time.RFC3339Nano)
	}
	if !opts.To.IsZero() {
		normalized.To = opts.To.UTC().Format(time.RFC3339Nano)
	}

	data, _ := json.Marshal(normalized)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func invalidQueryCursor(reason string) error {
	return fmt.Errorf("%w: %s", persis.ErrInvalidDAGRunQueryCursor, reason)
}
