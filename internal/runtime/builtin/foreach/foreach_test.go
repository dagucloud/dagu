// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package foreach

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/command"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForeachStatusDetailsIdentifyItems(t *testing.T) {
	t.Parallel()

	items := []expandedItem{
		{index: 0, key: "0", value: "customer-a"},
		{index: 1, key: "1", value: map[string]any{"customer": "customer-b"}},
	}
	results := []itemResult{
		{Index: 0, Key: "0", Status: ir.NodeFailed.String()},
		{Index: 1, Key: "1", Status: ir.NodeSucceeded.String()},
	}

	assert.Equal(t, []ir.NodeStatusDetail{
		{Label: "customer-a", Status: ir.NodeFailed},
		{Label: `{"customer":"customer-b"}`, Status: ir.NodeSucceeded},
	}, foreachStatusDetails(items, results, false))
}

func TestForeachStatusDetailsUseConfiguredKeys(t *testing.T) {
	t.Parallel()

	items := []expandedItem{{index: 0, key: "customer-a", value: map[string]any{"id": 42}}}
	results := []itemResult{{Index: 0, Key: "customer-a", Status: ir.NodeFailed.String()}}

	assert.Equal(t, []ir.NodeStatusDetail{
		{Label: "customer-a", Status: ir.NodeFailed},
	}, foreachStatusDetails(items, results, true))
}

func TestSummarizeLeavesUnstartedItemsOutOfTheFailedCount(t *testing.T) {
	t.Parallel()

	results := []itemResult{
		{Index: 0, Status: ir.NodeSucceeded.String()},
		{Index: 1, Status: ir.NodeFailed.String(), Error: "exit status 1"},
		{Index: 2, Status: ir.NodeNotStarted.String()},
	}
	outcome := summarize(results, nil)
	assert.Equal(t, 3, outcome.total)
	assert.Equal(t, 1, outcome.failed)
	assert.NoError(t, outcome.err)

	allFailed := summarize(results[1:2], nil)
	assert.EqualError(t, allFailed.err, "all 1 item bodies failed; first error: exit status 1")
}

// A run without a log directory keeps body logs in a scratch directory that
// must not outlive the item: nothing records its path, so nothing else
// could ever remove it.
func TestRunItemRemovesScratchLogDir(t *testing.T) {
	tmpDir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, tmpDir)
	}

	e := &foreachExecutor{step: ir.Step{
		Name: "each",
		Foreach: &ir.ForeachConfig{
			Items: []any{"a"},
			Steps: []ir.Step{{Name: "body", ID: "body", Command: "true"}},
		},
	}}
	e.SetStdout(io.Discard)
	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "each"}, "run-1", "")
	require.NoError(t, e.Run(ctx))

	leftover, err := filepath.Glob(filepath.Join(tmpDir, scratchLogDirPrefix+"*"))
	require.NoError(t, err)
	assert.Empty(t, leftover, "the scratch log directory should be removed")
}

// An item whose body cannot even be planned has no body logs, but its
// record must still say it failed, or the item list shows it as not started
// while the aggregate counts it as failed.
func TestRunItemRecordsFailureBeforeBodyRuns(t *testing.T) {
	t.Parallel()

	e := &foreachExecutor{
		step: ir.Step{
			Name: "each",
			Foreach: &ir.ForeachConfig{
				Steps: []ir.Step{{Name: "body", Command: "true", Depends: []string{"missing"}}},
			},
		},
		stepDir: t.TempDir(),
	}
	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "each"}, "run-1", "")
	result := e.runItem(ctx, expandedItem{index: 0, key: "0", value: "a"})
	require.Equal(t, ir.NodeFailed.String(), result.Status)
	require.NotEmpty(t, result.Error)

	data, err := os.ReadFile(filepath.Join(e.stepDir, "0", "status.json"))
	require.NoError(t, err)
	var record ir.ForeachItemStatus
	require.NoError(t, json.Unmarshal(data, &record))
	assert.Equal(t, ir.NodeFailed, record.Status)
	assert.Equal(t, result.Error, record.Error)
	assert.NotEmpty(t, record.FinishedAt)
	assert.Empty(t, record.Steps)
}
