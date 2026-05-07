// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	corespec "github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGRunManageListUsesCursorAndReturnsLogRefs(t *testing.T) {
	store := &dagRunManageTestStore{
		page: exec.DAGRunStatusPage{
			Items: []*exec.DAGRunStatus{{
				Name:       "build",
				DAGRunID:   "run-1",
				AttemptID:  "attempt-1",
				Status:     core.Failed,
				StartedAt:  "2026-05-07T00:00:00Z",
				FinishedAt: "2026-05-07T00:01:00Z",
				Log:        "/tmp/build-run-1.log",
				Nodes: []*exec.Node{{
					Step:   core.Step{Name: "agent-step", ExecutorConfig: core.ExecutorConfig{Type: "agent"}},
					Status: core.NodeFailed,
					Stdout: "/tmp/agent-step.out",
					Stderr: "/tmp/agent-step.err",
				}},
			}},
			NextCursor: "next-cursor",
		},
	}
	tool := NewDAGRunManageTool(store)

	out := runJSONTool(t, tool, map[string]any{
		"action":  "list",
		"dagName": "build",
		"status":  []string{"failed"},
		"limit":   1,
		"cursor":  "cursor-1",
	})
	require.False(t, out.IsError, out.Content)

	assert.Equal(t, "build", store.listOpts.ExactName)
	assert.Equal(t, "cursor-1", store.listOpts.Cursor)
	assert.Equal(t, 1, store.listOpts.Limit)
	require.Len(t, store.listOpts.Statuses, 1)
	assert.Equal(t, core.Failed, store.listOpts.Statuses[0])

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.Content), &got))
	assert.Equal(t, "list", got["action"])
	assert.Equal(t, true, got["hasMore"])
	assert.Equal(t, "next-cursor", got["nextCursor"])

	items := got["items"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	assert.Equal(t, "build", item["dagName"])
	assert.Equal(t, "run-1", item["dagRunId"])
	assert.Equal(t, "failed", item["status"])

	nodes := item["nodes"].([]any)
	node := nodes[0].(map[string]any)
	assert.Equal(t, "agent-step", node["stepName"])
	assert.Equal(t, "agent", node["executorType"])

	stepRefs := item["stepLogRefs"].(map[string]any)
	logRefs := stepRefs["agent-step"].(map[string]any)
	assert.Equal(t, "stdout", logRefs["stdout"].(map[string]any)["stream"])
	assert.Equal(t, "stderr", logRefs["stderr"].(map[string]any)["stream"])
}

func TestDAGRunManageReadStepLogAndMessages(t *testing.T) {
	logFile := writeTempLog(t, "line 1\nline 2\nline 3\n")
	store := &dagRunManageTestStore{
		status: &exec.DAGRunStatus{
			Name:     "build",
			DAGRunID: "run-1",
			Status:   core.Failed,
			Nodes: []*exec.Node{{
				Step:   core.Step{Name: "agent-step", ExecutorConfig: core.ExecutorConfig{Type: "agent"}},
				Status: core.NodeFailed,
				Stderr: logFile,
			}},
		},
		messages: []exec.LLMMessage{{
			Role:    exec.RoleAssistant,
			Content: "I found the failing command",
		}},
	}
	tool := NewDAGRunManageTool(store)

	logOut := runJSONTool(t, tool, map[string]any{
		"action":   "read_log",
		"dagName":  "build",
		"dagRunId": "run-1",
		"stepName": "agent-step",
		"stream":   "stderr",
		"tail":     2,
	})
	require.False(t, logOut.IsError, logOut.Content)
	var logGot map[string]any
	require.NoError(t, json.Unmarshal([]byte(logOut.Content), &logGot))
	assert.Equal(t, "line 2\nline 3", logGot["content"])
	assert.EqualValues(t, 2, logGot["lineCount"])
	assert.EqualValues(t, 3, logGot["totalLines"])

	msgOut := runJSONTool(t, tool, map[string]any{
		"action":   "read_messages",
		"dagName":  "build",
		"dagRunId": "run-1",
		"stepName": "agent-step",
	})
	require.False(t, msgOut.IsError, msgOut.Content)
	var msgGot map[string]any
	require.NoError(t, json.Unmarshal([]byte(msgOut.Content), &msgGot))
	messages := msgGot["messages"].([]any)
	require.Len(t, messages, 1)
	assert.Equal(t, "assistant", messages[0].(map[string]any)["role"])
}

func TestDAGRunManageDiagnoseCollectsFailedStepContext(t *testing.T) {
	logFile := writeTempLog(t, "ok\npanic: boom\n")
	store := &dagRunManageTestStore{
		status: &exec.DAGRunStatus{
			Name:     "build",
			DAGRunID: "run-1",
			Status:   core.Failed,
			Log:      logFile,
			Nodes: []*exec.Node{{
				Step:   core.Step{Name: "agent-step", ExecutorConfig: core.ExecutorConfig{Type: "agent"}},
				Status: core.NodeFailed,
				Error:  "exit status 1",
				Stderr: logFile,
			}},
		},
		messages: []exec.LLMMessage{{Role: exec.RoleAssistant, Content: "tool failed"}},
	}
	tool := NewDAGRunManageTool(store)

	out := runJSONTool(t, tool, map[string]any{
		"action":   "diagnose",
		"dagName":  "build",
		"dagRunId": "run-1",
		"tail":     5,
	})
	require.False(t, out.IsError, out.Content)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.Content), &got))
	assert.Equal(t, "failed", got["status"])
	assert.Equal(t, "agent-step", got["primaryFailedStepName"])

	logs := got["logs"].(map[string]any)
	assert.Contains(t, logs["scheduler"].(map[string]any)["content"], "panic: boom")
	assert.Contains(t, logs["stderr"].(map[string]any)["content"], "panic: boom")

	messages := got["messages"].([]any)
	require.Len(t, messages, 1)
	assert.Equal(t, "assistant", messages[0].(map[string]any)["role"])
}

func TestDAGRunManageWatchActions(t *testing.T) {
	watcher := &dagRunManageFakeWatcher{
		info: DAGRunWatchInfo{
			WatchID:  "watch-1",
			DAGName:  "build",
			DAGRunID: "run-1",
			State:    DAGRunWatchStateRunning,
			Status:   core.Running.String(),
		},
	}
	tool := NewDAGRunManageTool(nil, watcher)
	toolCtx := ToolContext{
		Context:   context.Background(),
		SessionID: "session-1",
		User:      UserIdentity{UserID: "user-1", Username: "dev"},
	}

	watchOut := runJSONToolWithContext(t, tool, toolCtx, map[string]any{
		"action":            "watch",
		"dagName":           "build",
		"dagRunId":          "run-1",
		"notifyOn":          []string{"terminal"},
		"diagnoseOnFailure": false,
	})
	require.False(t, watchOut.IsError, watchOut.Content)
	assert.Equal(t, "session-1", watcher.watchReq.SessionID)
	assert.Equal(t, "user-1", watcher.watchReq.User.UserID)
	assert.Equal(t, "build", watcher.watchReq.DAGName)
	assert.Equal(t, "run-1", watcher.watchReq.DAGRunID)
	assert.Equal(t, []string{"terminal"}, watcher.watchReq.NotifyOn)
	assert.False(t, watcher.watchReq.DiagnoseOnFailure)

	var watchGot map[string]any
	require.NoError(t, json.Unmarshal([]byte(watchOut.Content), &watchGot))
	assert.Equal(t, "watch", watchGot["action"])
	assert.Equal(t, "watch-1", watchGot["watchId"])

	statusOut := runJSONToolWithContext(t, tool, toolCtx, map[string]any{
		"action":  "watch_status",
		"watchId": "watch-1",
	})
	require.False(t, statusOut.IsError, statusOut.Content)
	assert.Equal(t, "watch-1", watcher.statusReq.WatchID)
	assert.Equal(t, "session-1", watcher.statusReq.SessionID)

	cancelOut := runJSONToolWithContext(t, tool, toolCtx, map[string]any{
		"action":  "cancel_watch",
		"watchId": "watch-1",
	})
	require.False(t, cancelOut.IsError, cancelOut.Content)
	assert.Equal(t, "watch-1", watcher.cancelReq.WatchID)
	assert.Equal(t, "session-1", watcher.cancelReq.SessionID)
}

func TestDAGRunWatchRegistryNotifiesTerminalRun(t *testing.T) {
	status := &exec.DAGRunStatus{
		Name:     "build",
		DAGRunID: "run-1",
		Status:   core.Failed,
		Error:    "workflow failed",
		Nodes: []*exec.Node{{
			Step:   core.Step{Name: "test"},
			Status: core.NodeFailed,
			Error:  "exit status 1",
		}},
	}
	store := &dagRunManageTestStore{status: status}
	var notifiedReq DAGRunWatchRequest
	var notifiedInfo DAGRunWatchInfo
	var notifiedStatus *exec.DAGRunStatus
	registry := newDAGRunWatchRegistry(
		store,
		func(_ context.Context, req DAGRunWatchRequest, info DAGRunWatchInfo, status *exec.DAGRunStatus) error {
			notifiedReq = req
			notifiedInfo = info
			notifiedStatus = status
			return nil
		},
		slog.Default(),
		withDAGRunWatchPollInterval(time.Millisecond),
	)

	info, err := registry.Watch(context.Background(), DAGRunWatchRequest{
		SessionID:         "session-1",
		User:              UserIdentity{UserID: "user-1"},
		DAGName:           "build",
		DAGRunID:          "run-1",
		DiagnoseOnFailure: true,
	})
	require.NoError(t, err)

	assert.Equal(t, DAGRunWatchStateCompleted, info.State)
	assert.Equal(t, core.Failed.String(), info.Status)
	assert.True(t, info.Notified)
	assert.Equal(t, "session-1", notifiedReq.SessionID)
	assert.Equal(t, info.WatchID, notifiedInfo.WatchID)
	require.NotNil(t, notifiedStatus)
	assert.Equal(t, core.Failed, notifiedStatus.Status)

	message := formatDAGRunWatchNotification(notifiedReq, notifiedInfo, notifiedStatus)
	assert.Contains(t, message, "DAG run finished: build/run-1")
	assert.Contains(t, message, "Status: failed")
	assert.Contains(t, message, "Primary failed step: test")
	assert.Contains(t, message, "dag_run_manage")
}

func TestDAGRunWatchRegistryDeduplicatesActiveRun(t *testing.T) {
	store := &dagRunManageTestStore{
		status: &exec.DAGRunStatus{
			Name:     "build",
			DAGRunID: "run-1",
			Status:   core.Running,
		},
	}
	registry := newDAGRunWatchRegistry(store, nil, slog.Default(), withDAGRunWatchPollInterval(time.Hour))
	req := DAGRunWatchRequest{
		SessionID: "session-1",
		User:      UserIdentity{UserID: "user-1"},
		DAGName:   "build",
		DAGRunID:  "run-1",
	}

	first, err := registry.Watch(context.Background(), req)
	require.NoError(t, err)
	second, err := registry.Watch(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, first.WatchID, second.WatchID)
	assert.Equal(t, DAGRunWatchStateRunning, second.State)

	canceled, err := registry.Cancel(context.Background(), DAGRunWatchCancelRequest{
		SessionID: "session-1",
		WatchID:   first.WatchID,
	})
	require.NoError(t, err)
	assert.Equal(t, DAGRunWatchStateCanceled, canceled.State)
}

func TestDAGRunWatchRegistryClearSessionRemovesActiveWatches(t *testing.T) {
	store := &dagRunManageTestStore{
		status: &exec.DAGRunStatus{
			Name:     "build",
			DAGRunID: "run-1",
			Status:   core.Running,
		},
	}
	registry := newDAGRunWatchRegistry(store, nil, slog.Default(), withDAGRunWatchPollInterval(time.Hour))
	info, err := registry.Watch(context.Background(), DAGRunWatchRequest{
		SessionID: "session-1",
		User:      UserIdentity{UserID: "user-1"},
		DAGName:   "build",
		DAGRunID:  "run-1",
	})
	require.NoError(t, err)

	removed := registry.ClearSession("session-1")
	assert.Equal(t, 1, removed)

	_, err = registry.Status(context.Background(), DAGRunWatchStatusRequest{
		SessionID: "session-1",
		WatchID:   info.WatchID,
	})
	require.Error(t, err)
}

func TestDAGRunWatchRegistryExpiresStuckRun(t *testing.T) {
	store := &dagRunManageTestStore{
		status: &exec.DAGRunStatus{
			Name:     "build",
			DAGRunID: "run-1",
			Status:   core.Running,
		},
	}
	registry := newDAGRunWatchRegistry(
		store,
		nil,
		slog.Default(),
		withDAGRunWatchPollInterval(time.Millisecond),
		withDAGRunWatchMaxDuration(time.Nanosecond),
	)

	info, err := registry.Watch(context.Background(), DAGRunWatchRequest{
		SessionID: "session-1",
		User:      UserIdentity{UserID: "user-1"},
		DAGName:   "build",
		DAGRunID:  "run-1",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		current, err := registry.Status(context.Background(), DAGRunWatchStatusRequest{
			SessionID: "session-1",
			WatchID:   info.WatchID,
		})
		return err == nil && current.State == DAGRunWatchStateExpired
	}, 100*time.Millisecond, time.Millisecond)
}

func TestDAGDefManageListGetValidateAndSchema(t *testing.T) {
	store := &dagDefManageTestStore{
		dags: []*core.DAG{{
			Name:        "build",
			Description: "Build workflow",
			Location:    "/dags/build.yaml",
			Labels:      core.NewLabels([]string{"team:platform"}),
			Steps:       []core.Step{{Name: "test", ExecutorConfig: core.ExecutorConfig{Type: "command"}}},
		}},
		specs: map[string]string{
			"build": "steps:\n  - name: test\n    command: go test ./...\n",
		},
	}
	tool := NewDAGDefManageTool(store)

	listOut := runJSONTool(t, tool, map[string]any{"action": "list", "limit": 1})
	require.False(t, listOut.IsError, listOut.Content)
	assert.Equal(t, 1, store.listParams.Paginator.Limit())

	var listGot map[string]any
	require.NoError(t, json.Unmarshal([]byte(listOut.Content), &listGot))
	items := listGot["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "build", items[0].(map[string]any)["name"])

	getOut := runJSONTool(t, tool, map[string]any{"action": "get", "dagName": "build"})
	require.False(t, getOut.IsError, getOut.Content)
	var getGot map[string]any
	require.NoError(t, json.Unmarshal([]byte(getOut.Content), &getGot))
	assert.Contains(t, getGot["spec"], "go test")
	assert.EqualValues(t, 1, getGot["summary"].(map[string]any)["stepCount"])

	validateOut := runJSONTool(t, tool, map[string]any{
		"action": "validate",
		"spec":   "steps:\n  - name: ok\n    command: echo ok\n",
	})
	require.False(t, validateOut.IsError, validateOut.Content)
	var validateGot map[string]any
	require.NoError(t, json.Unmarshal([]byte(validateOut.Content), &validateGot))
	assert.Equal(t, true, validateGot["valid"])

	schemaOut := runJSONTool(t, tool, map[string]any{"action": "schema", "path": "steps"})
	require.False(t, schemaOut.IsError, schemaOut.Content)
	var schemaGot map[string]any
	require.NoError(t, json.Unmarshal([]byte(schemaOut.Content), &schemaGot))
	assert.Contains(t, schemaGot["schema"], "steps")
}

func runJSONTool(t *testing.T, tool *AgentTool, input map[string]any) ToolOut {
	t.Helper()
	return runJSONToolWithContext(t, tool, ToolContext{Context: context.Background()}, input)
}

func runJSONToolWithContext(t *testing.T, tool *AgentTool, toolCtx ToolContext, input map[string]any) ToolOut {
	t.Helper()
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	return tool.Run(toolCtx, raw)
}

func writeTempLog(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/step.log"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

type dagRunManageTestStore struct {
	page     exec.DAGRunStatusPage
	status   *exec.DAGRunStatus
	messages []exec.LLMMessage
	listOpts exec.ListDAGRunStatusesOptions
}

type dagRunManageFakeWatcher struct {
	info      DAGRunWatchInfo
	watchReq  DAGRunWatchRequest
	statusReq DAGRunWatchStatusRequest
	cancelReq DAGRunWatchCancelRequest
}

func (w *dagRunManageFakeWatcher) Watch(_ context.Context, req DAGRunWatchRequest) (DAGRunWatchInfo, error) {
	w.watchReq = req
	return w.info, nil
}

func (w *dagRunManageFakeWatcher) Status(_ context.Context, req DAGRunWatchStatusRequest) (DAGRunWatchInfo, error) {
	w.statusReq = req
	return w.info, nil
}

func (w *dagRunManageFakeWatcher) Cancel(_ context.Context, req DAGRunWatchCancelRequest) (DAGRunWatchInfo, error) {
	w.cancelReq = req
	info := w.info
	info.State = DAGRunWatchStateCanceled
	return info, nil
}

func (s *dagRunManageTestStore) CreateAttempt(context.Context, *core.DAG, time.Time, string, exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected CreateAttempt")
}

func (s *dagRunManageTestStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	return nil
}

func (s *dagRunManageTestStore) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected LatestAttempt")
}

func (s *dagRunManageTestStore) ListStatuses(context.Context, ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, errors.New("unexpected ListStatuses")
}

func (s *dagRunManageTestStore) ListStatusesPage(_ context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	var parsed exec.ListDAGRunStatusesOptions
	for _, opt := range opts {
		opt(&parsed)
	}
	s.listOpts = parsed
	return s.page, nil
}

func (s *dagRunManageTestStore) CompareAndSwapLatestAttemptStatus(context.Context, exec.DAGRunRef, string, core.Status, func(*exec.DAGRunStatus) error) (*exec.DAGRunStatus, bool, error) {
	return nil, false, errors.New("unexpected CompareAndSwapLatestAttemptStatus")
}

func (s *dagRunManageTestStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return &dagRunManageTestAttempt{status: s.status, messages: s.messages}, nil
}

func (s *dagRunManageTestStore) FindSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return &dagRunManageTestAttempt{status: s.status, messages: s.messages}, nil
}

func (s *dagRunManageTestStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected CreateSubAttempt")
}

func (s *dagRunManageTestStore) RemoveOldDAGRuns(context.Context, string, int, ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, errors.New("unexpected RemoveOldDAGRuns")
}

func (s *dagRunManageTestStore) RenameDAGRuns(context.Context, string, string) error {
	return errors.New("unexpected RenameDAGRuns")
}

func (s *dagRunManageTestStore) RemoveDAGRun(context.Context, exec.DAGRunRef, ...exec.RemoveDAGRunOption) error {
	return errors.New("unexpected RemoveDAGRun")
}

type dagRunManageTestAttempt struct {
	status   *exec.DAGRunStatus
	messages []exec.LLMMessage
}

func (a *dagRunManageTestAttempt) ID() string {
	if a.status == nil {
		return ""
	}
	return a.status.AttemptID
}
func (a *dagRunManageTestAttempt) Open(context.Context) error                     { return nil }
func (a *dagRunManageTestAttempt) Write(context.Context, exec.DAGRunStatus) error { return nil }
func (a *dagRunManageTestAttempt) Close(context.Context) error                    { return nil }
func (a *dagRunManageTestAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	if a.status == nil {
		return nil, exec.ErrNoStatusData
	}
	return a.status, nil
}
func (a *dagRunManageTestAttempt) ReadDAG(context.Context) (*core.DAG, error) { return nil, nil }
func (a *dagRunManageTestAttempt) SetDAG(*core.DAG)                           {}
func (a *dagRunManageTestAttempt) Abort(context.Context) error                { return nil }
func (a *dagRunManageTestAttempt) IsAborting(context.Context) (bool, error)   { return false, nil }
func (a *dagRunManageTestAttempt) Hide(context.Context) error                 { return nil }
func (a *dagRunManageTestAttempt) Hidden() bool                               { return false }
func (a *dagRunManageTestAttempt) WriteOutputs(context.Context, *exec.DAGRunOutputs) error {
	return nil
}
func (a *dagRunManageTestAttempt) ReadOutputs(context.Context) (*exec.DAGRunOutputs, error) {
	return nil, nil
}
func (a *dagRunManageTestAttempt) WriteStepMessages(context.Context, string, []exec.LLMMessage) error {
	return nil
}
func (a *dagRunManageTestAttempt) ReadStepMessages(context.Context, string) ([]exec.LLMMessage, error) {
	return a.messages, nil
}
func (a *dagRunManageTestAttempt) WorkDir() string { return "" }

type dagDefManageTestStore struct {
	dags       []*core.DAG
	specs      map[string]string
	listParams exec.ListDAGsOptions
}

func (s *dagDefManageTestStore) Create(context.Context, string, []byte) error { return nil }
func (s *dagDefManageTestStore) Delete(context.Context, string) error         { return nil }
func (s *dagDefManageTestStore) List(_ context.Context, params exec.ListDAGsOptions) (exec.PaginatedResult[*core.DAG], []string, error) {
	s.listParams = params
	pg := exec.DefaultPaginator()
	if params.Paginator != nil {
		pg = *params.Paginator
	}
	return exec.NewPaginatedResult(s.dags, len(s.dags), pg), nil, nil
}
func (s *dagDefManageTestStore) GetMetadata(context.Context, string) (*core.DAG, error) {
	if len(s.dags) == 0 {
		return nil, exec.ErrDAGNotFound
	}
	return s.dags[0], nil
}
func (s *dagDefManageTestStore) GetDetails(context.Context, string, ...corespec.LoadOption) (*core.DAG, error) {
	if len(s.dags) == 0 {
		return nil, exec.ErrDAGNotFound
	}
	return s.dags[0], nil
}
func (s *dagDefManageTestStore) Grep(context.Context, string) ([]*exec.GrepDAGsResult, []string, error) {
	return nil, nil, nil
}
func (s *dagDefManageTestStore) SearchCursor(context.Context, exec.SearchDAGsOptions) (*exec.CursorResult[exec.SearchDAGResult], []string, error) {
	return nil, nil, nil
}
func (s *dagDefManageTestStore) SearchMatches(context.Context, string, exec.SearchDAGMatchesOptions) (*exec.CursorResult[*exec.Match], error) {
	return nil, nil
}
func (s *dagDefManageTestStore) Rename(context.Context, string, string) error { return nil }
func (s *dagDefManageTestStore) GetSpec(_ context.Context, fileName string) (string, error) {
	raw, ok := s.specs[fileName]
	if !ok {
		return "", exec.ErrDAGNotFound
	}
	return raw, nil
}
func (s *dagDefManageTestStore) UpdateSpec(context.Context, string, []byte) error { return nil }
func (s *dagDefManageTestStore) LoadSpec(ctx context.Context, raw []byte, opts ...corespec.LoadOption) (*core.DAG, error) {
	return corespec.LoadYAML(ctx, raw, opts...)
}
func (s *dagDefManageTestStore) LabelList(context.Context) ([]string, []string, error) {
	return nil, nil, nil
}
func (s *dagDefManageTestStore) ToggleSuspend(context.Context, string, bool) error { return nil }
func (s *dagDefManageTestStore) IsSuspended(context.Context, string) bool          { return false }
