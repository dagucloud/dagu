// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package foreach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/command"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
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

// The step's error keeps a masked read in a body, so it is neither retried
// nor marked successful, but no other body error: a body's timeout is not the
// step's own.
func TestSummarizeKeepsOnlyAMaskedRead(t *testing.T) {
	t.Parallel()

	read := &cmnvalue.MaskedReadError{Step: "login", Output: "TOKEN"}
	masked := summarize([]itemResult{{Status: ir.NodeFailed.String(), Error: read.Error(), err: read}}, nil)
	assert.ErrorIs(t, masked.err, read)

	// The masked read is kept whichever body failed first.
	maskedLater := summarize([]itemResult{
		{Status: ir.NodeFailed.String(), Error: "exit status 1"},
		{Status: ir.NodeFailed.String(), Error: read.Error(), err: read},
	}, nil)
	assert.ErrorIs(t, maskedLater.err, read)
	assert.EqualError(t, maskedLater.err, "all 2 item bodies failed; first error: exit status 1")

	timedOut := summarize([]itemResult{{Status: ir.NodeFailed.String(), Error: "deadline", err: context.DeadlineExceeded}}, nil)
	require.Error(t, timedOut.err)
	assert.NotErrorIs(t, timedOut.err, context.DeadlineExceeded)
	assert.EqualError(t, timedOut.err, "all 1 item bodies failed; first error: deadline")
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

// abortingExecutor stands for a body step whose end stops the run.
type abortingExecutor struct{}

func (abortingExecutor) SetStdout(io.Writer)  {}
func (abortingExecutor) SetStderr(io.Writer)  {}
func (abortingExecutor) Kill(os.Signal) error { return nil }
func (abortingExecutor) Run(context.Context) error {
	return fmt.Errorf("end: %w", untrusted.ErrRunAborted)
}

// failingWriter refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// A failed write of the aggregate does not hide that a body stopped its run.
func TestRunAbortedSurvivesFailedAggregate(t *testing.T) {
	executorType := "test-foreach-run-aborted"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return abortingExecutor{}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	e := &foreachExecutor{step: ir.Step{
		Name: "each",
		Foreach: &ir.ForeachConfig{
			Items: []any{"a"},
			Steps: []ir.Step{{Name: "agent", ID: "agent", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}},
		},
	}}
	e.SetStdout(failingWriter{})
	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "each"}, "run-1", "", runtime.WithDAGRunLogDir(t.TempDir()))

	err := e.Run(ctx)
	require.ErrorIs(t, err, untrusted.ErrRunAborted)
	assert.ErrorContains(t, err, "disk full")
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

// operationsExecutor stands for a body step that drives an agent, such as a
// browser step: item 0 needs the model to fix its operation, item 1 replays
// it, and item 2 is refused with an error naming the secret it typed. Every
// item's session also names the secret in an answered question.
type operationsExecutor struct {
	session *ir.AgentSession
}

const operationsSecret = "vendor-token-1234"

func (e *operationsExecutor) SetStdout(io.Writer)                {}
func (e *operationsExecutor) SetStderr(io.Writer)                {}
func (e *operationsExecutor) Kill(os.Signal) error               { return nil }
func (e *operationsExecutor) SetAgentSession(s *ir.AgentSession) { e.session = s }
func (e *operationsExecutor) GetAgentSession() *ir.AgentSession  { return e.session }
func (e *operationsExecutor) Run(ctx context.Context) error {
	index := runtime.GetEnv(ctx).Foreach["index"]
	event := ir.AgentSessionEvent{Type: "tool", Name: "act", Status: "cache-hit", Via: "element", Content: "Type " + operationsSecret + " into the vendor field"}
	if index == "0" {
		llm.RecordUsage(ctx, llm.UsageRecord{Provider: "openrouter", Model: "test/model", Usage: llm.Usage{PromptTokens: 40, CompletionTokens: 2, TotalTokens: 42}})
		event.Status, event.Via, event.Tokens = "healed", "model", 42
	}
	e.session = &ir.AgentSession{
		Provider: "browser", State: ir.AgentSessionSucceeded, Events: []ir.AgentSessionEvent{event},
		Interactions: []ir.AgentInteraction{{
			ID: "ask-1", Kind: ir.AgentInteractionQuestion, Status: ir.AgentInteractionAnswered,
			Questions: []ir.AgentQuestion{{Header: "Code", Question: "Is " + operationsSecret + " the vendor code?",
				Options: []ir.AgentQuestionOption{{Label: operationsSecret}}}},
			Answers: [][]string{{operationsSecret}},
		}},
	}
	if index == "2" {
		return errors.New("site refused " + operationsSecret)
	}
	return nil
}

// Each item's record keeps its body steps' agent sessions and model usage,
// with the run's secrets masked as the run status masks them.
func TestItemRecordKeepsBodyAgentSessions(t *testing.T) {
	executorType := "test-foreach-operations"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &operationsExecutor{}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	logDir := t.TempDir()
	e := &foreachExecutor{step: ir.Step{
		Name: "each",
		Foreach: &ir.ForeachConfig{
			Items: []any{"a", "b", "c"},
			Steps: []ir.Step{{Name: "enter", ID: "enter", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}},
		},
	}}
	e.SetStdout(io.Discard)
	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "each"}, "run-1", "",
		runtime.WithDAGRunLogDir(logDir),
		runtime.WithSecrets([]string{"VENDOR_TOKEN=" + operationsSecret}))
	require.NoError(t, e.Run(ctx))

	readStep := func(index string) ir.ForeachBodyStepStatus {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(logDir, logpath.ForeachLogDirName, "each", index, logpath.ForeachItemStatusFile))
		require.NoError(t, err)
		var record ir.ForeachItemStatus
		require.NoError(t, json.Unmarshal(data, &record))
		require.Len(t, record.Steps, 1)
		require.NotContains(t, string(data), operationsSecret)
		return record.Steps[0]
	}

	healed := readStep("0")
	require.NotNil(t, healed.AgentSession)
	require.Len(t, healed.AgentSession.Events, 1)
	assert.Equal(t, "healed", healed.AgentSession.Events[0].Status)
	assert.Equal(t, "Type ******* into the vendor field", healed.AgentSession.Events[0].Content)
	require.NotNil(t, healed.ModelUsage)
	assert.Equal(t, []ir.ModelUsage{{Provider: "openrouter", Model: "test/model", Calls: 1, InputTokens: 40, OutputTokens: 2}}, healed.ModelUsage.Total())

	replayed := readStep("1")
	require.NotNil(t, replayed.AgentSession)
	assert.Equal(t, "cache-hit", replayed.AgentSession.Events[0].Status)
	assert.Empty(t, replayed.ModelUsage.Total())

	refused := readStep("2")
	assert.Equal(t, ir.NodeFailed, refused.Status)
	assert.Contains(t, refused.Error, "site refused *******")
	require.NotNil(t, refused.AgentSession)
}

// A reader finds the items of a step of this version even when a step of an
// earlier version could have shared its directory, as fetch data could with
// fetch_data.
func TestStepDirIsFoundBesideSimilarNames(t *testing.T) {
	executorType := "test-foreach-found"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &itemExecutor{report: func(string) {}}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	logDir := t.TempDir()
	e := &foreachExecutor{step: ir.Step{
		Name: "fetch_data",
		Foreach: &ir.ForeachConfig{
			Items: []any{"a"},
			Steps: []ir.Step{{Name: "body", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}},
		},
	}}
	e.SetStdout(io.Discard)
	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "each"}, "run-1", "", runtime.WithDAGRunLogDir(logDir))
	require.NoError(t, e.Run(ctx))

	dir := logpath.ForeachStepDir(logDir, "fetch_data", []string{"fetch data", "fetch_data"})
	require.NotEmpty(t, dir)
	assert.FileExists(t, filepath.Join(dir, logpath.ForeachItemsFile))
}

// itemExecutor stands for a body step and reports the foreach item it runs
// in.
type itemExecutor struct {
	report func(item string)
}

func (e *itemExecutor) SetStdout(io.Writer)  {}
func (e *itemExecutor) SetStderr(io.Writer)  {}
func (e *itemExecutor) Kill(os.Signal) error { return nil }
func (e *itemExecutor) Run(ctx context.Context) error {
	e.report(runtime.GetEnv(ctx).ForeachItem)
	return nil
}

// Every item body, of a nested foreach too, tells its body steps an item
// identity of its own, and the same one each time the step runs.
func TestBodyStepsKnowTheirItem(t *testing.T) {
	executorType := "test-foreach-item"
	var mu sync.Mutex
	var items []string
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &itemExecutor{report: func(item string) {
			mu.Lock()
			defer mu.Unlock()
			items = append(items, item)
		}}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	look := ir.Step{Name: "look", ID: "look", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}
	inner := ir.Step{
		Name: "inner", ID: "inner", ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
		Foreach: &ir.ForeachConfig{Items: []any{"x", "y"}, Steps: []ir.Step{look}},
	}
	run := func() []string {
		t.Helper()
		items = nil
		e := &foreachExecutor{step: ir.Step{
			Name:    "each",
			Foreach: &ir.ForeachConfig{Items: []any{"a", "b"}, Steps: []ir.Step{look, inner}},
		}}
		e.SetStdout(io.Discard)
		ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "each"}, "run-1", "", runtime.WithDAGRunLogDir(t.TempDir()))
		require.NoError(t, e.Run(ctx))
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(items)
	}

	first := run()
	require.Len(t, first, 6, "a look in each item and in each item of its inner foreach")
	distinct := map[string]bool{}
	for _, item := range first {
		assert.NotEmpty(t, item)
		distinct[item] = true
	}
	assert.Len(t, distinct, 6, "each body has an item of its own: %q", first)
	assert.ElementsMatch(t, first, run(), "a body keeps its item from run to run")
}

// A body step has a span of its own, so the trace context of the foreach
// step's span must not reach it as a value the step set itself.
func TestBodyStepsDropForeachTraceContext(t *testing.T) {
	t.Parallel()

	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "each"}, "run-1", "")
	parent := runtime.NewEnv(ctx, ir.Step{Name: "each"})
	parent.Scope = parent.Scope.
		WithEntry("REGION", "eu", cmnvalue.EnvSourceStepEnv).
		WithEntry(runenv.EnvKeyTraceParent, "foreach-span", cmnvalue.EnvSourceStepEnv).
		WithEntry(runenv.EnvKeyTraceState, "foreach-state", cmnvalue.EnvSourceStepEnv)
	ctx = runtime.WithEnv(ctx, parent)

	itemCtx, err := contextWithItemScope(ctx, "each", "item", 0, "0", "a")
	require.NoError(t, err)

	body := runtime.NewEnv(itemCtx, ir.Step{Name: "body"})
	region, ok := body.Scope.Get("REGION")
	require.True(t, ok)
	assert.Equal(t, "eu", region)
	_, ok = body.Scope.Get(runenv.EnvKeyTraceParent)
	assert.False(t, ok)
	_, ok = body.Scope.Get(runenv.EnvKeyTraceState)
	assert.False(t, ok)
}
