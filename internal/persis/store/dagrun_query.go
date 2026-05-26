// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

type dagRunListItem struct {
	key    dagRunListKey
	status *exec.DAGRunStatus
}

type dagRunListKey struct {
	Timestamp time.Time
	Name      string
	DAGRunID  string
}

func (s *DAGRunStore) listStatuses(ctx context.Context, opts exec.ListDAGRunStatusesOptions, limit int, returnCursor bool) ([]*exec.DAGRunStatus, string, error) {
	cursorKey, err := decodeDAGRunQueryCursor(opts.Cursor, opts)
	if err != nil {
		return nil, "", err
	}
	attempts, err := s.latestRootAttempts(ctx, opts.ExactName)
	if err != nil {
		return nil, "", err
	}

	labelFilters := make([]core.LabelFilter, 0, len(opts.Labels))
	for _, label := range opts.Labels {
		if strings.TrimSpace(label) != "" {
			labelFilters = append(labelFilters, core.ParseLabelFilter(label))
		}
	}
	statusFilter := make(map[core.Status]struct{}, len(opts.Statuses))
	for _, st := range opts.Statuses {
		statusFilter[st] = struct{}{}
	}

	start, end := effectiveDAGRunTimeRange(opts.From, opts.To)
	items := make([]dagRunListItem, 0, len(attempts))
	for _, item := range attempts {
		if item.payload.Status == nil {
			continue
		}
		runTime := time.Unix(0, item.payload.RunCreatedAt).UTC()
		if !inDAGRunTimeRange(runTime, start, end, opts.From.IsZero(), opts.To.IsZero()) {
			continue
		}
		status := cloneDAGRunStatus(item.payload.Status)
		if opts.DAGRunID != "" && !strings.Contains(status.DAGRunID, opts.DAGRunID) {
			continue
		}
		if opts.Name != "" && !containsFold(status.Name, opts.Name) {
			continue
		}
		if len(statusFilter) > 0 {
			if _, ok := statusFilter[status.Status]; !ok {
				continue
			}
		}
		labels := core.NewLabels(status.Labels)
		if len(labelFilters) > 0 && !labels.MatchesFilters(labelFilters) {
			continue
		}
		if !opts.WorkspaceFilter.MatchesLabels(labels) {
			continue
		}
		items = append(items, dagRunListItem{
			key: dagRunListKey{
				Timestamp: runTime,
				Name:      status.Name,
				DAGRunID:  status.DAGRunID,
			},
			status: status,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return compareDagRunListKeys(items[i].key, items[j].key) < 0
	})

	if opts.Cursor != "" {
		filtered := items[:0]
		for _, item := range items {
			if compareDagRunListKeys(item.key, cursorKey) > 0 {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	target := limit
	if target <= 0 {
		target = len(items)
	}
	if returnCursor && limit > 0 && len(items) > limit {
		nextCursor, err := encodeDAGRunQueryCursor(opts, items[limit-1].key)
		if err != nil {
			return nil, "", err
		}
		return statusesFromItems(items[:limit]), nextCursor, nil
	}
	if target > len(items) {
		target = len(items)
	}
	return statusesFromItems(items[:target]), "", nil
}

func statusesFromItems(items []dagRunListItem) []*exec.DAGRunStatus {
	out := make([]*exec.DAGRunStatus, 0, len(items))
	for _, item := range items {
		out = append(out, item.status)
	}
	return out
}

func prepareDAGRunListOptions(location *time.Location, opts []exec.ListDAGRunStatusesOption) (exec.ListDAGRunStatusesOptions, error) {
	var options exec.ListDAGRunStatusesOptions
	for _, opt := range opts {
		opt(&options)
	}
	if !options.AllHistory && options.From.IsZero() && options.To.IsZero() {
		options.From = exec.NewUTC(startOfToday(location))
	}
	if !options.Unlimited {
		const maxLimit = 1000
		if options.Limit == 0 || options.Limit > maxLimit {
			options.Limit = maxLimit
		}
	}
	return options, nil
}

func effectiveDAGRunTimeRange(from, to exec.TimeInUTC) (time.Time, time.Time) {
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Now().UTC()
	if !from.IsZero() {
		start = from.Time
	}
	if !to.IsZero() {
		end = to.Time
	}
	return start, end
}

func inDAGRunTimeRange(t, start, end time.Time, fromZero, toZero bool) bool {
	if !fromZero && t.Before(start) {
		return false
	}
	if !toZero && !t.Before(end) {
		return false
	}
	return true
}

func compareDagRunListKeys(a, b dagRunListKey) int {
	switch {
	case a.Timestamp.After(b.Timestamp):
		return -1
	case a.Timestamp.Before(b.Timestamp):
		return 1
	case a.Name < b.Name:
		return -1
	case a.Name > b.Name:
		return 1
	case a.DAGRunID < b.DAGRunID:
		return -1
	case a.DAGRunID > b.DAGRunID:
		return 1
	default:
		return 0
	}
}

func compareRunPayload(a, b dagRunPayload) int {
	return compareDagRunListKeys(
		dagRunListKey{Timestamp: time.Unix(0, a.RunCreatedAt).UTC(), Name: a.Name, DAGRunID: a.DAGRunID},
		dagRunListKey{Timestamp: time.Unix(0, b.RunCreatedAt).UTC(), Name: b.Name, DAGRunID: b.DAGRunID},
	)
}

func compareAttemptPayload(a, b dagRunPayload) int {
	switch {
	case a.AttemptCreatedAt > b.AttemptCreatedAt:
		return 1
	case a.AttemptCreatedAt < b.AttemptCreatedAt:
		return -1
	case a.AttemptID > b.AttemptID:
		return 1
	case a.AttemptID < b.AttemptID:
		return -1
	default:
		return 0
	}
}

func dagRunCreatedAtFromPayloads(items []dagRunRecordItem) time.Time {
	var minTime time.Time
	for _, item := range items {
		t := time.Unix(0, item.payload.RunCreatedAt).UTC()
		if minTime.IsZero() || t.Before(minTime) {
			minTime = t
		}
	}
	if minTime.IsZero() {
		return time.Now().UTC()
	}
	return minTime
}

func dagRunAttemptActivity(item dagRunRecordItem) time.Time {
	if !item.record.UpdatedAt.IsZero() {
		return item.record.UpdatedAt.UTC()
	}
	if item.payload.AttemptCreatedAt != 0 {
		return time.Unix(0, item.payload.AttemptCreatedAt).UTC()
	}
	return time.Unix(0, item.payload.RunCreatedAt).UTC()
}

func startOfToday(location *time.Location) time.Time {
	if location == nil {
		location = time.Local
	}
	now := time.Now().In(location)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).UTC()
}

func rootRunPrefix(name, dagRunID string) string {
	return dagRunRootPrefix + escapeDAGRunComponent(name) + "/" + escapeDAGRunComponent(dagRunID) + "/"
}

func subRunPrefix(root exec.DAGRunRef, subDAGRunID string) string {
	return rootRunPrefix(root.Name, root.ID) + dagRunSubPrefix + escapeDAGRunComponent(subDAGRunID) + "/"
}

func rootAttemptRecordID(name, dagRunID, attemptID string) string {
	return rootRunPrefix(name, dagRunID) + dagRunAttemptPart + escapeDAGRunComponent(attemptID)
}

func subAttemptRecordID(root exec.DAGRunRef, subDAGRunID, attemptID string) string {
	return subRunPrefix(root, subDAGRunID) + dagRunAttemptPart + escapeDAGRunComponent(attemptID)
}

func renameDAGRunPayload(payload dagRunPayload, oldName, newName string) (dagRunPayload, string) {
	next := payload
	next.DAG = cloneDAG(payload.DAG)
	next.Status = cloneDAGRunStatus(payload.Status)
	next.Outputs = cloneDAGRunOutputs(payload.Outputs)
	next.StepMessages = copyStepMessagesMap(payload.StepMessages)

	if next.Outputs != nil && next.Outputs.Metadata.DAGName == oldName {
		next.Outputs.Metadata.DAGName = newName
	}

	if payload.Parent.Zero() {
		next.Name = newName
		if next.DAG != nil {
			next.DAG.Name = newName
		}
		if next.Status != nil {
			next.Status.Name = newName
		}
		return next, rootAttemptRecordID(newName, next.DAGRunID, next.AttemptID)
	}

	if next.Root.Name == oldName {
		next.Root.Name = newName
	}
	if next.Parent.Name == oldName {
		next.Parent.Name = newName
	}
	if next.Status != nil {
		if next.Status.Root.Name == oldName {
			next.Status.Root.Name = newName
		}
		if next.Status.Parent.Name == oldName {
			next.Status.Parent.Name = newName
		}
	}
	return next, subAttemptRecordID(next.Root, next.DAGRunID, next.AttemptID)
}

func dagRunLockKey(name, dagRunID string) string {
	return rootRunPrefix(name, dagRunID) + "lock"
}

func escapeDAGRunComponent(s string) string {
	return url.PathEscape(s)
}

func normalizeOrGenerateAttemptID(attemptID string) (string, error) {
	if attemptID != "" {
		return attemptID, nil
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate attempt ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func cloneDAG(dag *core.DAG) *core.DAG {
	if dag == nil {
		return nil
	}
	data, err := json.Marshal(dag)
	if err != nil {
		return dag.Clone()
	}
	var cloned core.DAG
	if err := json.Unmarshal(data, &cloned); err != nil {
		return dag.Clone()
	}
	return &cloned
}

func cloneDAGRunStatus(st *exec.DAGRunStatus) *exec.DAGRunStatus {
	if st == nil {
		return nil
	}
	data, err := json.Marshal(st)
	if err != nil {
		cp := *st
		return &cp
	}
	var cloned exec.DAGRunStatus
	if err := json.Unmarshal(data, &cloned); err != nil {
		cp := *st
		return &cp
	}
	return &cloned
}

func cloneDAGRunOutputs(outputs *exec.DAGRunOutputs) *exec.DAGRunOutputs {
	if outputs == nil {
		return nil
	}
	copied := *outputs
	copied.Outputs = copyStringMap(outputs.Outputs)
	return &copied
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func copyStepMessagesMap(in map[string][]exec.LLMMessage) map[string][]exec.LLMMessage {
	if in == nil {
		return nil
	}
	out := make(map[string][]exec.LLMMessage, len(in))
	for step, messages := range in {
		out[step] = append([]exec.LLMMessage(nil), messages...)
	}
	return out
}

func stepMessagesFromPayloads(items []dagRunRecordItem) map[string][]exec.LLMMessage {
	sorted := append([]dagRunRecordItem(nil), items...)
	// Merge from oldest to newest so retry attempts inherit the latest message
	// set for each step while preserving earlier steps that were not rewritten.
	sort.Slice(sorted, func(i, j int) bool {
		return compareAttemptPayload(sorted[i].payload, sorted[j].payload) < 0
	})
	var out map[string][]exec.LLMMessage
	for _, item := range sorted {
		for step, messages := range item.payload.StepMessages {
			if len(messages) == 0 {
				continue
			}
			if out == nil {
				out = make(map[string][]exec.LLMMessage)
			}
			out[step] = append([]exec.LLMMessage(nil), messages...)
		}
	}
	return out
}

func latestStepMessagesFromPayloads(items []dagRunRecordItem, stepName string) []exec.LLMMessage {
	sorted := append([]dagRunRecordItem(nil), items...)
	// Read from newest to oldest because direct reads should return the most
	// recent message set for the requested step.
	sort.Slice(sorted, func(i, j int) bool {
		return compareAttemptPayload(sorted[i].payload, sorted[j].payload) > 0
	})
	for _, item := range sorted {
		if len(item.payload.StepMessages[stepName]) > 0 {
			return append([]exec.LLMMessage(nil), item.payload.StepMessages[stepName]...)
		}
	}
	return nil
}

func containsFold(value, query string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

// Cursor versions are backend-scoped; file-backed cursors intentionally do not
// round-trip through this collection-backed store.
const dagRunQueryCursorVersion = 1

type dagRunQueryCursorPayload struct {
	Version    int    `json:"v"`
	FilterHash string `json:"f"`
	Timestamp  string `json:"ts"`
	Name       string `json:"n"`
	DAGRunID   string `json:"r"`
}

func encodeDAGRunQueryCursor(opts exec.ListDAGRunStatusesOptions, key dagRunListKey) (string, error) {
	payload := dagRunQueryCursorPayload{
		Version:    dagRunQueryCursorVersion,
		FilterHash: dagRunQueryFilterHash(opts),
		Timestamp:  key.Timestamp.UTC().Format(time.RFC3339Nano),
		Name:       key.Name,
		DAGRunID:   key.DAGRunID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("dag-run store: marshal query cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeDAGRunQueryCursor(cursor string, opts exec.ListDAGRunStatusesOptions) (dagRunListKey, error) {
	if cursor == "" {
		return dagRunListKey{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return dagRunListKey{}, invalidDAGRunQueryCursor("decode cursor")
	}
	var payload dagRunQueryCursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return dagRunListKey{}, invalidDAGRunQueryCursor("parse cursor")
	}
	if payload.Version != dagRunQueryCursorVersion {
		return dagRunListKey{}, invalidDAGRunQueryCursor("unsupported cursor version")
	}
	if payload.FilterHash == "" || payload.Timestamp == "" || payload.Name == "" || payload.DAGRunID == "" {
		return dagRunListKey{}, invalidDAGRunQueryCursor("cursor is incomplete")
	}
	if payload.FilterHash != dagRunQueryFilterHash(opts) {
		return dagRunListKey{}, invalidDAGRunQueryCursor("cursor does not match the current filters")
	}
	ts, err := time.Parse(time.RFC3339Nano, payload.Timestamp)
	if err != nil {
		return dagRunListKey{}, invalidDAGRunQueryCursor("invalid cursor timestamp")
	}
	return dagRunListKey{Timestamp: ts.UTC(), Name: payload.Name, DAGRunID: payload.DAGRunID}, nil
}

func dagRunQueryFilterHash(opts exec.ListDAGRunStatusesOptions) string {
	statuses := make([]int, 0, len(opts.Statuses))
	for _, status := range opts.Statuses {
		statuses = append(statuses, int(status))
	}
	sort.Ints(statuses)
	labels := append([]string(nil), opts.Labels...)
	sort.Strings(labels)
	workspace := normalizedWorkspaceFilter(opts.WorkspaceFilter)
	normalized := struct {
		DAGRunID        string                    `json:"dag_run_id,omitempty"`
		Name            string                    `json:"name,omitempty"`
		ExactName       string                    `json:"exact_name,omitempty"`
		From            string                    `json:"from,omitempty"`
		To              string                    `json:"to,omitempty"`
		Statuses        []int                     `json:"statuses,omitempty"`
		Labels          []string                  `json:"labels,omitempty"`
		WorkspaceFilter *dagRunWorkspaceFilterKey `json:"workspace_filter,omitempty"`
		AllHistory      bool                      `json:"all_history,omitempty"`
	}{
		DAGRunID:        opts.DAGRunID,
		Name:            opts.Name,
		ExactName:       opts.ExactName,
		Statuses:        statuses,
		Labels:          labels,
		WorkspaceFilter: workspace,
		AllHistory:      opts.AllHistory,
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

type dagRunWorkspaceFilterKey struct {
	Workspaces        []string `json:"workspaces,omitempty"`
	IncludeUnlabelled bool     `json:"include_unlabelled,omitempty"`
}

func normalizedWorkspaceFilter(filter *exec.WorkspaceFilter) *dagRunWorkspaceFilterKey {
	if filter == nil || !filter.Enabled {
		return nil
	}
	workspaces := append([]string(nil), filter.Workspaces...)
	sort.Strings(workspaces)
	return &dagRunWorkspaceFilterKey{
		Workspaces:        workspaces,
		IncludeUnlabelled: filter.IncludeUnlabelled,
	}
}

func invalidDAGRunQueryCursor(reason string) error {
	return fmt.Errorf("%w: %s", exec.ErrInvalidDAGRunQueryCursor, reason)
}
