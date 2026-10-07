// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// foreachFixture seeds a root run and a child run whose single step is a
// foreach over four items, with the records and body logs the executor
// writes: item 0 failed, item 1 succeeded and holds a nested foreach, item 2
// never started, and item 3 is retrying a body step.
type foreachFixture struct {
	server  test.Server
	dagName string
	stepDir string
}

func seedForeachRun(t *testing.T) foreachFixture {
	t.Helper()
	server := test.SetupServer(t)
	logDir := t.TempDir()
	stepLog := filepath.Join(logDir, "each.out")
	require.NoError(t, os.WriteFile(stepLog, []byte(`{"summary":{}}`), 0o600))
	stepDir := logpath.ForeachStepDir(stepLog, "each")

	writeJSON := func(path string, v any) {
		data, err := json.Marshal(v)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, data, 0o600))
	}
	writeFile := func(path, content string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	writeJSON(filepath.Join(stepDir, logpath.ForeachItemsFile), ir.ForeachItems{Total: 4, Items: []ir.ForeachItemRef{
		{Index: 0, Key: "a"}, {Index: 1, Key: "b"}, {Index: 2, Key: "c"}, {Index: 3, Key: "d"},
	}})
	writeJSON(filepath.Join(stepDir, "3", logpath.ForeachItemStatusFile), ir.ForeachItemStatus{
		Index: 3, Key: "d", Status: ir.NodeRunning, StartedAt: "2026-10-07T00:00:00Z",
		Steps: []ir.ForeachBodyStepStatus{{Name: "body", ID: "body", Status: ir.NodeRetrying, RetryCount: 1}},
	})
	writeJSON(filepath.Join(stepDir, "0", logpath.ForeachItemStatusFile), ir.ForeachItemStatus{
		Index: 0, Key: "a", Status: ir.NodeFailed, Error: "exit status 3",
		StartedAt: "2026-10-07T00:00:00Z", FinishedAt: "2026-10-07T00:00:02Z",
		Steps: []ir.ForeachBodyStepStatus{{
			Name: "body", ID: "body", Status: ir.NodeFailed, Error: "exit status 3",
			Stdout: "body.out", Stderr: "body.err",
		}},
	})
	writeFile(filepath.Join(stepDir, "0", "body.out"), "line 1\nline 2\nline 3\n")
	writeFile(filepath.Join(stepDir, "0", "body.err"), "real cause for a\n")
	writeJSON(filepath.Join(stepDir, "1", logpath.ForeachItemStatusFile), ir.ForeachItemStatus{
		Index: 1, Key: "b", Status: ir.NodeSucceeded,
		Steps: []ir.ForeachBodyStepStatus{
			{Name: "body", ID: "body", Status: ir.NodeSucceeded, Stdout: "body.out", Stderr: "body.err"},
			{Name: "inner", Status: ir.NodeSucceeded, Foreach: true, Stdout: "inner.out", Stderr: "inner.err"},
		},
	})
	writeFile(filepath.Join(stepDir, "1", "body.out"), "item b\n")
	innerDir := filepath.Join(stepDir, "1", logpath.ForeachLogDirName, "inner")
	writeJSON(filepath.Join(innerDir, logpath.ForeachItemsFile), ir.ForeachItems{Total: 1, Items: []ir.ForeachItemRef{{Index: 0, Key: "0"}}})
	writeJSON(filepath.Join(innerDir, "0", logpath.ForeachItemStatusFile), ir.ForeachItemStatus{
		Index: 0, Key: "0", Status: ir.NodeSucceeded,
		Steps: []ir.ForeachBodyStepStatus{{Name: "deep", Status: ir.NodeSucceeded, Stdout: "deep.out"}},
	})
	writeFile(filepath.Join(innerDir, "0", "deep.out"), "deep output\n")

	dag := &ir.DAG{Name: "foreach-api", Steps: []ir.Step{{
		Name:           "each",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
		Foreach:        &ir.ForeachConfig{Items: []any{"a", "b", "c", "d"}},
	}}}
	root := ir.NewDAGRunRef(dag.Name, "root")
	for _, runID := range []string{root.ID, "child"} {
		opts := persis.DAGRunCreateAttemptOptions{}
		if runID != root.ID {
			opts.RootDAGRun = root
		}
		attempt, err := server.DAGRunRepository.CreateAttempt(server.Context, dag, time.Now(), runID, opts)
		require.NoError(t, err)
		status := ir.InitialStatus(dag)
		status.DAGRunID = runID
		status.AttemptID = attempt.ID()
		status.Root = root
		status.Status = ir.PartiallySucceeded
		status.Log = stepLog
		status.Nodes[0].Stdout = stepLog
		status.Nodes[0].Status = ir.NodePartiallySucceeded
		if runID == root.ID {
			status.Nodes[0].SubRuns = []ir.SubDAGRun{{DAGRunID: "child", DAGName: dag.Name}}
		}
		require.NoError(t, attempt.Open(server.Context))
		require.NoError(t, attempt.Write(server.Context, status))
		require.NoError(t, attempt.Close(server.Context))
	}
	return foreachFixture{server: server, dagName: dag.Name, stepDir: stepDir}
}

func (f foreachFixture) get(t *testing.T, path string, want int) *test.Response {
	t.Helper()
	return f.server.Client().Get("/api/v1/dag-runs/" + f.dagName + "/root" + path).ExpectStatus(want).Send(t)
}

func TestForeachItems(t *testing.T) {
	f := seedForeachRun(t)

	t.Run("failed items lead, then running, and unstarted items show as not started", func(t *testing.T) {
		var list api.ForeachItemList
		f.get(t, "/steps/each/foreach?remoteNode=local", http.StatusOK).Unmarshal(t, &list)
		require.Equal(t, 4, list.Total)
		require.Equal(t, api.ForeachItemCounts{NotStarted: 1, Running: 1, Succeeded: 1, Failed: 1}, list.Counts)
		require.Len(t, list.Items, 4)
		require.Equal(t, "0", list.Items[0].Item)
		require.Equal(t, api.NodeStatusLabelFailed, list.Items[0].StatusLabel)
		require.Equal(t, "exit status 3", *list.Items[0].Error)
		require.Equal(t, api.NodeStatusLabelRunning, list.Items[1].StatusLabel)
		require.Equal(t, "b", list.Items[2].Key)
		require.Equal(t, api.NodeStatusLabelNotStarted, list.Items[3].StatusLabel)
	})

	t.Run("status filter uses the same buckets as the counts", func(t *testing.T) {
		var list api.ForeachItemList
		f.get(t, "/steps/each/foreach?remoteNode=local&status=succeeded", http.StatusOK).Unmarshal(t, &list)
		require.Equal(t, 4, list.Total)
		require.Len(t, list.Items, 1)
		require.Equal(t, 1, list.Items[0].Index)

		f.get(t, "/steps/each/foreach?remoteNode=local&status=running", http.StatusOK).Unmarshal(t, &list)
		require.Len(t, list.Items, 1)
		require.Equal(t, 3, list.Items[0].Index)

		f.get(t, "/steps/each/foreach?remoteNode=local&status=not_started", http.StatusOK).Unmarshal(t, &list)
		require.Len(t, list.Items, 1)
		require.Equal(t, 2, list.Items[0].Index)
	})

	t.Run("paging", func(t *testing.T) {
		var list api.ForeachItemList
		f.get(t, "/steps/each/foreach?remoteNode=local&page=2&perPage=3", http.StatusOK).Unmarshal(t, &list)
		require.Len(t, list.Items, 1)
		require.Equal(t, 2, list.Items[0].Index)
	})

	t.Run("nested items listed by parent", func(t *testing.T) {
		var item api.ForeachItem
		f.get(t, "/steps/each/foreach/1?remoteNode=local", http.StatusOK).Unmarshal(t, &item)
		require.Len(t, item.Steps, 2)
		require.Nil(t, item.Steps[0].ForeachParent)
		require.NotNil(t, item.Steps[1].ForeachParent)
		parent := *item.Steps[1].ForeachParent
		require.Equal(t, "1.inner", parent)

		var list api.ForeachItemList
		f.get(t, "/steps/each/foreach?remoteNode=local&parent="+parent, http.StatusOK).Unmarshal(t, &list)
		require.Len(t, list.Items, 1)
		require.Equal(t, "1.inner.0", list.Items[0].Item)

		var log api.Log
		f.get(t, "/steps/each/foreach/1.inner.0/steps/deep/log?remoteNode=local", http.StatusOK).Unmarshal(t, &log)
		require.Equal(t, "deep output", log.Content)
	})

	t.Run("item detail reports body steps and log availability", func(t *testing.T) {
		var item api.ForeachItem
		f.get(t, "/steps/each/foreach/0?remoteNode=local", http.StatusOK).Unmarshal(t, &item)
		require.Equal(t, "a", item.Key)
		require.Equal(t, api.NodeStatusLabelFailed, item.StatusLabel)
		require.Len(t, item.Steps, 1)
		require.Equal(t, "body", item.Steps[0].Name)
		require.True(t, item.Steps[0].HasStdout)
		require.True(t, item.Steps[0].HasStderr)
	})

	t.Run("body step log honours stream and tail", func(t *testing.T) {
		var log api.Log
		f.get(t, "/steps/each/foreach/0/steps/body/log?remoteNode=local&stream=stderr", http.StatusOK).Unmarshal(t, &log)
		require.Equal(t, "real cause for a", log.Content)

		f.get(t, "/steps/each/foreach/0/steps/body/log?remoteNode=local&stream=stdout&tail=1", http.StatusOK).Unmarshal(t, &log)
		require.Equal(t, "line 3", log.Content)
		require.Equal(t, 3, *log.TotalLines)
	})

	t.Run("download streams the file", func(t *testing.T) {
		resp := f.get(t, "/steps/each/foreach/0/steps/body/log/download?remoteNode=local&stream=stderr", http.StatusOK)
		require.Equal(t, "real cause for a\n", resp.Body)
		require.Contains(t, resp.Response.Header().Get("Content-Disposition"), "0-body-stderr.log")
	})

	t.Run("missing records are not found", func(t *testing.T) {
		f.get(t, "/steps/each/foreach/2?remoteNode=local", http.StatusNotFound)
		f.get(t, "/steps/each/foreach/7?remoteNode=local", http.StatusNotFound)
		f.get(t, "/steps/each/foreach/0/steps/nope/log?remoteNode=local", http.StatusNotFound)
		f.get(t, "/steps/missing/foreach?remoteNode=local", http.StatusNotFound)
		f.get(t, "/steps/each/foreach/0.inner/steps/body/log?remoteNode=local", http.StatusBadRequest)
		f.get(t, "/steps/each/foreach?remoteNode=local&parent=1", http.StatusBadRequest)
	})

	t.Run("sub dag-run routes", func(t *testing.T) {
		var list api.ForeachItemList
		f.get(t, "/sub-dag-runs/child/steps/each/foreach?remoteNode=local", http.StatusOK).Unmarshal(t, &list)
		require.Len(t, list.Items, 4)

		var item api.ForeachItem
		f.get(t, "/sub-dag-runs/child/steps/each/foreach/0?remoteNode=local", http.StatusOK).Unmarshal(t, &item)
		require.Equal(t, "a", item.Key)

		var log api.Log
		f.get(t, "/sub-dag-runs/child/steps/each/foreach/0/steps/body/log?remoteNode=local&stream=stderr", http.StatusOK).Unmarshal(t, &log)
		require.Equal(t, "real cause for a", log.Content)

		resp := f.get(t, "/sub-dag-runs/child/steps/each/foreach/0/steps/body/log/download?remoteNode=local", http.StatusOK)
		require.Equal(t, "line 1\nline 2\nline 3\n", resp.Body)
		f.get(t, "/sub-dag-runs/nope/steps/each/foreach?remoteNode=local", http.StatusNotFound)
	})

	t.Run("step logs archive carries the foreach tree", func(t *testing.T) {
		resp := f.get(t, "/steps/log/download?remoteNode=local", http.StatusOK)
		names := zipEntryNames(t, resp.Body)
		require.Contains(t, names, "001-each/stdout.log")
		require.Contains(t, names, "001-each/foreach/items.json")
		require.Contains(t, names, "001-each/foreach/0/body.err")
		require.Contains(t, names, "001-each/foreach/1/foreach/inner/0/deep.out")
	})
}

func zipEntryNames(t *testing.T, body string) []string {
	t.Helper()
	archive, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	names := make([]string, 0, len(archive.File))
	for _, entry := range archive.File {
		names = append(names, entry.Name)
	}
	return names
}
