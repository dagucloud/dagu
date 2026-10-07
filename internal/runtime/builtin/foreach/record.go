// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package foreach

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// Item records are observability only: a failure to write one is logged
// and never fails the item or the step.

// writeItems records the expanded items of the step in its foreach directory.
func writeItems(ctx context.Context, stepDir string, items []expandedItem) {
	record := ir.ForeachItems{Total: len(items), Items: make([]ir.ForeachItemRef, len(items))}
	for i, item := range items {
		record.Items[i] = ir.ForeachItemRef{Index: item.index, Key: item.key}
	}
	writeRecord(ctx, filepath.Join(stepDir, logpath.ForeachItemsFile), record)
}

// itemRecorder keeps an item's status file current while its body runs.
type itemRecorder struct {
	dir       string
	item      expandedItem
	plan      *runtime.Plan
	startedAt time.Time
}

func newItemRecorder(dir string, item expandedItem, plan *runtime.Plan) *itemRecorder {
	return &itemRecorder{dir: dir, item: item, plan: plan, startedAt: time.Now()}
}

// started records the item as running before its first body step starts.
func (r *itemRecorder) started(ctx context.Context) {
	r.write(ctx, ir.NodeRunning, "", time.Time{})
}

// progress records body step changes and releases the runner after each one.
func (r *itemRecorder) progress(ctx context.Context, updates <-chan runtime.ProgressUpdate) {
	for update := range updates {
		r.write(ctx, ir.NodeRunning, "", time.Time{})
		update.Ack(nil)
	}
}

// finished records the item's final status.
func (r *itemRecorder) finished(ctx context.Context, status ir.NodeStatus, errMessage string) {
	r.write(ctx, status, errMessage, time.Now())
}

func (r *itemRecorder) write(ctx context.Context, status ir.NodeStatus, errMessage string, finishedAt time.Time) {
	record := ir.ForeachItemStatus{
		Index:      r.item.index,
		Key:        r.item.key,
		Status:     status,
		Error:      errMessage,
		StartedAt:  stringutil.FormatTime(r.startedAt),
		FinishedAt: stringutil.FormatTime(finishedAt),
		Steps:      bodyStepStatuses(r.plan),
	}
	writeRecord(ctx, filepath.Join(r.dir, logpath.ForeachItemStatusFile), record)
}

func bodyStepStatuses(plan *runtime.Plan) []ir.ForeachBodyStepStatus {
	nodes := plan.Nodes()
	steps := make([]ir.ForeachBodyStepStatus, 0, len(nodes))
	for _, node := range nodes {
		data := node.NodeData()
		step := ir.ForeachBodyStepStatus{
			Name:       data.Step.Name,
			ID:         data.Step.ID,
			Status:     data.State.Status,
			StartedAt:  stringutil.FormatTime(data.State.StartedAt),
			FinishedAt: stringutil.FormatTime(data.State.FinishedAt),
			RetryCount: data.State.RetryCount,
			Foreach:    data.Step.ExecutorConfig.Type == ir.ExecutorTypeForeach,
		}
		if data.State.Error != nil {
			step.Error = data.State.Error.Error()
		}
		if data.State.Stdout != "" {
			step.Stdout = filepath.Base(data.State.Stdout)
		}
		if data.State.Stderr != "" {
			step.Stderr = filepath.Base(data.State.Stderr)
		}
		steps = append(steps, step)
	}
	return steps
}

func writeRecord(ctx context.Context, path string, record any) {
	data, err := json.Marshal(record)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(path), 0750); err == nil {
			err = fileutil.WriteFileAtomic(path, data, 0600)
		}
	}
	if err != nil {
		logger.Warn(ctx, "Failed to write foreach item record",
			tag.Error(err),
			tag.File(path))
	}
}
