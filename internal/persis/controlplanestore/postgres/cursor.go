// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
)

const queryCursorVersion = 2

type listKey struct {
	Timestamp time.Time
	Name      string
	DAGRunID  string
}

type queryCursorPayload struct {
	Version    int    `json:"v"`
	FilterHash string `json:"f"`
	Timestamp  string `json:"ts"`
	Name       string `json:"n"`
	DAGRunID   string `json:"r"`
}

func encodeQueryCursor(opts exec.ListDAGRunStatusesOptions, key listKey) (string, error) {
	payload := queryCursorPayload{
		Version:    queryCursorVersion,
		FilterHash: queryFilterHash(opts),
		Timestamp:  key.Timestamp.UTC().Format(time.RFC3339Nano),
		Name:       key.Name,
		DAGRunID:   key.DAGRunID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal query cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeQueryCursor(cursor string, opts exec.ListDAGRunStatusesOptions) (listKey, error) {
	if cursor == "" {
		return listKey{}, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return listKey{}, invalidQueryCursor("decode cursor")
	}

	var payload queryCursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return listKey{}, invalidQueryCursor("parse cursor")
	}
	if payload.Version != queryCursorVersion {
		return listKey{}, invalidQueryCursor("unsupported cursor version")
	}
	if payload.FilterHash == "" || payload.Timestamp == "" || payload.Name == "" || payload.DAGRunID == "" {
		return listKey{}, invalidQueryCursor("cursor is incomplete")
	}
	if payload.FilterHash != queryFilterHash(opts) {
		return listKey{}, invalidQueryCursor("cursor does not match the current filters")
	}

	ts, err := time.Parse(time.RFC3339Nano, payload.Timestamp)
	if err != nil {
		return listKey{}, invalidQueryCursor("invalid cursor timestamp")
	}

	return listKey{
		Timestamp: ts.UTC(),
		Name:      payload.Name,
		DAGRunID:  payload.DAGRunID,
	}, nil
}

func queryFilterHash(opts exec.ListDAGRunStatusesOptions) string {
	statuses := make([]int, 0, len(opts.Statuses))
	for _, status := range opts.Statuses {
		statuses = append(statuses, int(status))
	}
	sort.Ints(statuses)

	labels := append([]string(nil), opts.Labels...)
	sort.Strings(labels)

	type workspaceFilterFingerprint struct {
		Enabled           bool     `json:"enabled,omitempty"`
		Workspaces        []string `json:"workspaces,omitempty"`
		IncludeUnlabelled bool     `json:"include_unlabelled,omitempty"`
	}
	workspaceFilter := workspaceFilterFingerprint{Workspaces: []string{}}
	if opts.WorkspaceFilter != nil {
		workspaceFilter.Enabled = opts.WorkspaceFilter.Enabled
		if opts.WorkspaceFilter.Enabled {
			workspaceFilter.Workspaces = append([]string(nil), opts.WorkspaceFilter.Workspaces...)
			sort.Strings(workspaceFilter.Workspaces)
			workspaceFilter.IncludeUnlabelled = opts.WorkspaceFilter.IncludeUnlabelled
		}
	}

	normalized := struct {
		DAGRunID        string                     `json:"dag_run_id,omitempty"`
		Name            string                     `json:"name,omitempty"`
		ExactName       string                     `json:"exact_name,omitempty"`
		From            string                     `json:"from,omitempty"`
		To              string                     `json:"to,omitempty"`
		Statuses        []int                      `json:"statuses,omitempty"`
		Labels          []string                   `json:"labels,omitempty"`
		WorkspaceFilter workspaceFilterFingerprint `json:"workspace_filter"`
		AllHistory      bool                       `json:"all_history,omitempty"`
	}{
		DAGRunID:        opts.DAGRunID,
		Name:            opts.Name,
		ExactName:       opts.ExactName,
		Statuses:        statuses,
		Labels:          labels,
		WorkspaceFilter: workspaceFilter,
		AllHistory:      opts.AllHistory,
	}
	if !opts.From.IsZero() {
		normalized.From = opts.From.UTC().Format(time.RFC3339Nano)
	}
	if !opts.To.IsZero() {
		normalized.To = opts.To.UTC().Format(time.RFC3339Nano)
	}

	data, err := json.Marshal(normalized)
	if err != nil {
		panic(fmt.Errorf("marshal query cursor filter: %w", err))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func invalidQueryCursor(reason string) error {
	return fmt.Errorf("%w: %s", exec.ErrInvalidQueryCursor, reason)
}
