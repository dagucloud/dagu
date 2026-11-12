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
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// Item records are observability only: a failure to write one is logged
// and never fails the item or the step, and they are not fsynced, so a body
// step transition never waits on the disk.

// recordInterval bounds how often a running item's record is rewritten.
// Readers poll every few seconds, so a fast item is written once, when it
// finishes, and a slow one at most once per interval while it changes.
const recordInterval = time.Second

// writeItems records the expanded items of the step in its foreach directory.
func writeItems(ctx context.Context, stepDir string, items []expandedItem) {
	record := ir.ForeachItems{Total: len(items), Items: make([]ir.ForeachItemRef, len(items))}
	for i, item := range items {
		record.Items[i] = ir.ForeachItemRef{Index: item.index, Key: item.key}
	}
	writeRecord(ctx, filepath.Join(stepDir, logpath.ForeachItemsFile), record)
}

// itemRecorder keeps an item's status file current while its body runs.
// The plan is set once the body is planned; until then the record lists no
// body steps.
type itemRecorder struct {
	dir       string
	item      expandedItem
	plan      *runtime.Plan
	startedAt time.Time
	// masker hides the run's secrets in the record, as the run status hides
	// them in its nodes.
	masker *masking.Masker
}

func newItemRecorder(ctx context.Context, dir string, item expandedItem) *itemRecorder {
	return &itemRecorder{dir: dir, item: item, startedAt: time.Now(), masker: newRecordMasker(ctx)}
}

// newRecordMasker returns a masker for the secrets of the run in ctx.
func newRecordMasker(ctx context.Context) *masking.Masker {
	secrets := runtime.GetDAGContext(ctx).EnvScope.AllSecrets()
	pairs := make([]string, 0, len(secrets))
	for name, value := range secrets {
		pairs = append(pairs, name+"="+value)
	}
	return masking.NewMasker(masking.SourcedEnvVars{Secrets: pairs})
}

// planned gives the recorder the body plan whose steps it records.
func (r *itemRecorder) planned(plan *runtime.Plan) {
	r.plan = plan
}

// progress releases the runner on every body step change and rewrites the
// record at most once per recordInterval, so writes never pace the body.
func (r *itemRecorder) progress(ctx context.Context, updates <-chan runtime.ProgressUpdate) {
	ticker := time.NewTicker(recordInterval)
	defer ticker.Stop()
	changed := false
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				return
			}
			update.Ack(nil)
			changed = true
		case <-ticker.C:
			if changed {
				r.write(ctx, ir.NodeRunning, "", time.Time{})
				changed = false
			}
		}
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
		Error:      r.masker.MaskString(errMessage),
		StartedAt:  stringutil.FormatTime(r.startedAt),
		FinishedAt: stringutil.FormatTime(finishedAt),
		Steps:      bodyStepStatuses(r.plan, r.masker),
	}
	writeRecord(ctx, filepath.Join(r.dir, logpath.ForeachItemStatusFile), record)
}

func bodyStepStatuses(plan *runtime.Plan, masker *masking.Masker) []ir.ForeachBodyStepStatus {
	var nodes []*runtime.Node
	if plan != nil {
		nodes = plan.Nodes()
	}
	steps := make([]ir.ForeachBodyStepStatus, 0, len(nodes))
	for _, node := range nodes {
		data := node.NodeData()
		step := ir.ForeachBodyStepStatus{
			Name:         data.Step.Name,
			ID:           data.Step.ID,
			Status:       data.State.Status,
			StartedAt:    stringutil.FormatTime(data.State.StartedAt),
			FinishedAt:   stringutil.FormatTime(data.State.FinishedAt),
			RetryCount:   data.State.RetryCount,
			AgentSession: recordedSession(data.State.AgentSession, masker),
			ModelUsage:   ir.CloneStepModelUsage(data.State.ModelUsage),
			Foreach:      data.Step.ExecutorConfig.Type == ir.ExecutorTypeForeach,
		}
		if data.State.Error != nil {
			step.Error = masker.MaskString(data.State.Error.Error())
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

// recordedSession masks a body step's agent session for its item record.
// The run status keeps interaction answers so a resumed step can read them
// back; nothing reads an item record back, so its interactions are masked
// too.
func recordedSession(session *ir.AgentSession, masker *masking.Masker) *ir.AgentSession {
	masked := ir.MaskAgentSession(session, masker.MaskString)
	if masked == nil {
		return nil
	}
	for i := range masked.Interactions {
		interaction := &masked.Interactions[i]
		maskAll(masker, interaction.Patterns)
		maskAll(masker, interaction.AllowForSessionPatterns)
		for j := range interaction.Questions {
			question := &interaction.Questions[j]
			question.Header = masker.MaskString(question.Header)
			question.Question = masker.MaskString(question.Question)
			for k := range question.Options {
				question.Options[k].Label = masker.MaskString(question.Options[k].Label)
				question.Options[k].Description = masker.MaskString(question.Options[k].Description)
			}
		}
		for _, answer := range interaction.Answers {
			maskAll(masker, answer)
		}
	}
	for i := range masked.PermissionGrants {
		maskAll(masker, masked.PermissionGrants[i].Patterns)
	}
	return masked
}

// maskAll masks each text in place.
func maskAll(masker *masking.Masker, texts []string) {
	for i := range texts {
		texts[i] = masker.MaskString(texts[i])
	}
}

func writeRecord(ctx context.Context, path string, record any) {
	data, err := json.Marshal(record)
	if err == nil {
		err = replaceFile(path, data)
	}
	if err != nil {
		logger.Warn(ctx, "Failed to write foreach item record",
			tag.Error(err),
			tag.File(path))
	}
}

// replaceFile swaps in the new contents through a temp file and rename, so
// a reader never sees a partial record. It does not fsync.
func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = fileutil.ReplaceFile(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
