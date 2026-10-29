// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"

	"github.com/dagucloud/dagu/v2/internal/ir"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/foreach"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForeachRuntimeRunsBodyAndPublishesAggregate(t *testing.T) {
	probeType, state := registerForeachProbeExecutor(t)
	r := setupRunner(t)

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "one", "url": "https://example.com/one"},
		map[string]any{"slug": "two", "url": "https://example.com/two"},
	}, 2)

	result := r.newPlan(t, parent).assertRun(t, ir.Succeeded)
	node := result.nodeByName(t, "each")
	raw, ok := node.NodeData().StringFormOutputValue()
	require.True(t, ok)

	var aggregate foreachAggregate
	require.NoError(t, json.Unmarshal([]byte(raw), &aggregate))

	assert.Equal(t, 2, aggregate.Summary.Total)
	assert.Equal(t, 2, aggregate.Summary.Succeeded)
	assert.Equal(t, 0, aggregate.Summary.Failed)
	require.Len(t, aggregate.Items, 2)
	assert.Equal(t, foreachAggregateItem{
		Index:   0,
		Key:     "one",
		Status:  ir.NodeSucceeded.String(),
		Outputs: map[string]string{"summary": "https://example.com/one"},
	}, aggregate.Items[0])
	assert.Equal(t, foreachAggregateItem{
		Index:   1,
		Key:     "two",
		Status:  ir.NodeSucceeded.String(),
		Outputs: map[string]string{"summary": "https://example.com/two"},
	}, aggregate.Items[1])
	assert.Equal(t, []map[string]string{
		{"summary": "https://example.com/one"},
		{"summary": "https://example.com/two"},
	}, aggregate.Outputs)

	assert.ElementsMatch(t, []foreachProbeRecord{
		{Value: "https://example.com/one", Key: "one"},
		{Value: "https://example.com/two", Key: "two"},
	}, state.records())
}

func TestForeachCollectStepJSON(t *testing.T) {
	probeType, _ := registerForeachProbeExecutor(t)
	r := setupRunner(t)
	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "one", "url": `a"b`},
	}, 1)
	parent.Foreach.Steps[0].Name = "foreach"
	parent.Foreach.Steps[0].ID = "foreach"
	parent.Foreach.Collect["summary"] = `{"result":"${foreach.outputs["value"]}"}`

	result := r.newPlan(t, parent).assertRun(t, ir.Succeeded)
	raw, ok := result.nodeByName(t, "each").NodeData().StringFormOutputValue()
	require.True(t, ok)
	var aggregate foreachAggregate
	require.NoError(t, json.Unmarshal([]byte(raw), &aggregate))
	require.Len(t, aggregate.Outputs, 1)

	var decoded map[string]string
	require.NoError(t, json.Unmarshal([]byte(aggregate.Outputs[0]["summary"]), &decoded))
	assert.Equal(t, `a"b`, decoded["result"])
}

// Top-level item fields are resolved in the key, body, and collect phases.
func TestForeachRuntimeTopLevelFieldResolution(t *testing.T) {
	items := []any{
		map[string]any{"slug": "one", "url": "https://example.com/one", "_row": 2, "請求番号": "INV-001"},
		map[string]any{"slug": "two", "url": "https://example.com/two", "_row": 3, "請求番号": "INV-002"},
	}

	tests := []struct {
		name       string
		key        string
		body       string
		collect    string
		wantKeys   []string
		wantValues []string
		wantOutput []string
	}{
		{
			name:       "ascii plain",
			key:        "${foreach.episode.slug}",
			body:       "${foreach.episode.url}",
			collect:    "${write.outputs.value}",
			wantKeys:   []string{"one", "two"},
			wantValues: []string{"https://example.com/one", "https://example.com/two"},
			wantOutput: []string{"https://example.com/one", "https://example.com/two"},
		},
		{
			name:       "underscore plain",
			key:        "${foreach.episode._row}",
			body:       "${foreach.episode._row}",
			collect:    "${write.outputs.value}",
			wantKeys:   []string{"2", "3"},
			wantValues: []string{"2", "3"},
			wantOutput: []string{"2", "3"},
		},
		{
			name:       "ascii quoted",
			key:        `${foreach.episode."url"}`,
			body:       `${foreach.episode."url"}`,
			collect:    "body-ok",
			wantKeys:   []string{"https://example.com/one", "https://example.com/two"},
			wantValues: []string{"https://example.com/one", "https://example.com/two"},
			wantOutput: []string{"body-ok", "body-ok"},
		},
		{
			name:       "japanese quoted dot",
			key:        `${foreach.episode."請求番号"}`,
			body:       `${foreach.episode."請求番号"}`,
			collect:    "body-ok",
			wantKeys:   []string{"INV-001", "INV-002"},
			wantValues: []string{"INV-001", "INV-002"},
			wantOutput: []string{"body-ok", "body-ok"},
		},
		{
			name:       "japanese quoted dot in collect",
			key:        "${foreach.episode.slug}",
			body:       "${foreach.episode.url}",
			collect:    `${foreach.episode."請求番号"}`,
			wantKeys:   []string{"one", "two"},
			wantValues: []string{"https://example.com/one", "https://example.com/two"},
			wantOutput: []string{"INV-001", "INV-002"},
		},
		{
			name:       "japanese plain",
			key:        "${foreach.episode.請求番号}",
			body:       "${foreach.episode.請求番号}",
			collect:    "body-ok",
			wantKeys:   []string{"INV-001", "INV-002"},
			wantValues: []string{"INV-001", "INV-002"},
			wantOutput: []string{"body-ok", "body-ok"},
		},
		{
			name:       "japanese plain in collect",
			key:        "${foreach.episode.slug}",
			body:       "${foreach.episode.url}",
			collect:    "${foreach.episode.請求番号}",
			wantKeys:   []string{"one", "two"},
			wantValues: []string{"https://example.com/one", "https://example.com/two"},
			wantOutput: []string{"INV-001", "INV-002"},
		},
		{
			name:       "japanese bracket",
			key:        `${foreach.episode["請求番号"]}`,
			body:       `${foreach.episode["請求番号"]}`,
			collect:    "body-ok",
			wantKeys:   []string{"INV-001", "INV-002"},
			wantValues: []string{"INV-001", "INV-002"},
			wantOutput: []string{"body-ok", "body-ok"},
		},
		{
			name:       "japanese bracket in collect",
			key:        "${foreach.episode.slug}",
			body:       "${foreach.episode.url}",
			collect:    `${foreach.episode["請求番号"]}`,
			wantKeys:   []string{"one", "two"},
			wantValues: []string{"https://example.com/one", "https://example.com/two"},
			wantOutput: []string{"INV-001", "INV-002"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probeType, state := registerForeachProbeExecutor(t)
			r := setupRunner(t)
			parent := foreachRuntimeStep(probeType, items, 2)
			parent.Foreach.Key = tt.key
			parent.Foreach.Steps[0].ExecutorConfig.Config["value"] = tt.body
			parent.Foreach.Collect["summary"] = tt.collect

			result := r.newPlan(t, parent).assertRun(t, ir.Succeeded)
			node := result.nodeByName(t, "each")
			raw, ok := node.NodeData().StringFormOutputValue()
			require.True(t, ok)
			var aggregate foreachAggregate
			require.NoError(t, json.Unmarshal([]byte(raw), &aggregate))

			records := state.records()
			assert.ElementsMatch(t, []foreachProbeRecord{
				{Value: tt.wantValues[0], Key: tt.wantKeys[0]},
				{Value: tt.wantValues[1], Key: tt.wantKeys[1]},
			}, records)
			require.Len(t, aggregate.Outputs, 2)
			assert.Equal(t, tt.wantOutput, []string{aggregate.Outputs[0]["summary"], aggregate.Outputs[1]["summary"]})
		})
	}
}

func TestForeachRuntimeWorkingDirResolvesItemField(t *testing.T) {
	probeType, state := registerForeachProbeExecutor(t)
	r := setupRunner(t)
	firstDir := t.TempDir()
	secondDir := t.TempDir()

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "one", "url": "one", "workdir": firstDir},
		map[string]any{"slug": "two", "url": "two", "workdir": secondDir},
	}, 2)
	parent.Foreach.Steps[0].Dir = `${foreach.episode."workdir"}`
	parent.Foreach.Steps[0].ExecutorConfig.Config["record_working_dir"] = true

	r.newPlan(t, parent).assertRun(t, ir.Succeeded)
	assert.ElementsMatch(t, []foreachProbeRecord{
		{Value: "one", Key: "one", WorkingDir: firstDir},
		{Value: "two", Key: "two", WorkingDir: secondDir},
	}, state.records())
}

func TestForeachMissingCollect(t *testing.T) {
	for _, ref := range []string{`${foreach.episode.missing}`, `${foreach.episode."missing"}`, `${foreach.episode["missing"]}`} {
		t.Run(ref, func(t *testing.T) {
			probeType, _ := registerForeachProbeExecutor(t)
			r := setupRunner(t)
			parent := foreachRuntimeStep(probeType, []any{
				map[string]any{"slug": "one", "url": "body-ok"},
			}, 1)
			parent.Foreach.Collect["summary"] = ref

			result := r.newPlan(t, parent).assertRun(t, ir.Failed)
			assert.ErrorContains(t, result.Error, "foreach.collect.summary resolved with unresolved reference: "+ref)
		})
	}
}

func TestForeachUnbracedBody(t *testing.T) {
	probeType, state := registerForeachProbeExecutor(t)
	r := setupRunner(t)
	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "one", "url": "$SECRET"},
	}, 1)
	parent.Foreach.Steps[0].Env = []string{"SECRET=synthetic-secret"}
	parent.Foreach.Steps[0].ExecutorConfig.Config["value"] = "$foreach.episode.url"

	r.newPlan(t, parent).assertRun(t, ir.Succeeded)
	assert.Equal(t, []foreachProbeRecord{{Value: "$foreach.episode.url", Key: "one"}}, state.records())
}

func TestForeachQuotedRetry(t *testing.T) {
	for _, ref := range []string{`${foreach.episode.limit}`, `${foreach.episode."limit"}`, `${foreach.episode["limit"]}`} {
		t.Run(ref, func(t *testing.T) {
			probeType, _ := registerForeachProbeExecutor(t)
			r := setupRunner(t)
			parent := foreachRuntimeStep(probeType, []any{
				map[string]any{"slug": "one", "url": "body-ok", "limit": 1000000},
			}, 1)
			parent.Foreach.Steps[0].RetryPolicy = ir.RetryPolicy{LimitStr: ref}

			r.newPlan(t, parent).assertRun(t, ir.Succeeded)
		})
	}
}

func TestForeachRuntimeBodyEnvFieldKeepsStructuredItemScope(t *testing.T) {
	probeType, state := registerForeachProbeExecutor(t)
	r := setupRunner(t)

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "one", "url": "one", "請求番号": "INV-001"},
		map[string]any{"slug": "two", "url": "two", "請求番号": "INV-002"},
	}, 2)
	parent.Foreach.Steps[0].Env = []string{
		"foreach=shadow",
		`CAPTURE=${foreach.episode."請求番号"}`,
	}
	parent.Foreach.Steps[0].ExecutorConfig.Config["value"] = `${foreach.episode["請求番号"]}|${env.CAPTURE}|$foreach`

	r.newPlan(t, parent).assertRun(t, ir.Succeeded)
	assert.ElementsMatch(t, []foreachProbeRecord{
		{Value: "INV-001|INV-001|shadow", Key: "one"},
		{Value: "INV-002|INV-002|shadow", Key: "two"},
	}, state.records())
}

// A body step reads the foreach step's own env: and the output variables of the
// steps the foreach step depends on, as a step in the DAG would.
func TestForeachRuntimeBodyReadsForeachStepScope(t *testing.T) {
	probeType, state := registerForeachProbeExecutor(t)
	r := setupRunner(t)

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "one", "url": "one"},
	}, 1)
	parent.Depends = []string{"upstream"}
	parent.Env = []string{"FROM_FOREACH_STEP=parent-env"}
	parent.Foreach.Steps[0].ExecutorConfig.Config["value"] =
		"${env.FROM_FOREACH_STEP}|${env.FROM_UPSTREAM}|${env.PWD}|${env.DAG_RUN_STEP_NAME}"

	upstream := newStep("upstream", withCommand("echo from-upstream"), withOutput("FROM_UPSTREAM"))
	r.newPlan(t, upstream, parent).assertRun(t, ir.Succeeded)

	records := state.records()
	require.Len(t, records, 1)
	parts := strings.Split(records[0].Value, "|")
	require.Len(t, parts, 4)
	assert.Equal(t, "parent-env", parts[0])
	assert.Equal(t, "from-upstream", parts[1])
	// Values Dagu sets per step belong to the body step, not the foreach step.
	assert.Equal(t, "write", parts[3])
}

// A loop some item bodies failed is partially succeeded: the steps after it
// run, and the aggregate names the failed items.
func TestForeachRuntimePartialFailureIsPartiallySucceeded(t *testing.T) {
	probeType, _ := registerForeachProbeExecutor(t)
	r := setupRunner(t)

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "a", "url": "a"},
		map[string]any{"slug": "b", "url": "b"},
		map[string]any{"slug": "c", "url": "c"},
	}, 3)
	parent.Foreach.Steps[0].ExecutorConfig.Config["fail_for"] = "b"
	after := successStep("after", "each")

	result := r.newPlan(t, parent, after).assertRun(t, ir.PartiallySucceeded)
	require.NoError(t, result.Error, "a partially succeeded loop is not a failed run")
	node := result.nodeByName(t, "each")
	assert.Equal(t, ir.NodePartiallySucceeded, node.State().Status)
	assert.Equal(t, ir.NodeSucceeded, result.nodeByName(t, "after").State().Status, "the dependent runs without continue_on")

	raw, ok := node.NodeData().StringFormOutputValue()
	require.True(t, ok, "the aggregate is published")
	var aggregate foreachAggregate
	require.NoError(t, json.Unmarshal([]byte(raw), &aggregate))
	assert.Equal(t, 3, aggregate.Summary.Total)
	assert.Equal(t, 2, aggregate.Summary.Succeeded)
	assert.Equal(t, 1, aggregate.Summary.Failed)
	require.Len(t, aggregate.Items, 3)
	assert.Equal(t, ir.NodeFailed.String(), aggregate.Items[1].Status)
	assert.NotEmpty(t, aggregate.Items[1].Error)
	assert.Equal(t, []map[string]string{{"summary": "a"}, {"summary": "c"}}, aggregate.Outputs, "outputs holds the successful bodies only")
}

// A loop every item body failed is failed, and the steps after it do not run.
func TestForeachRuntimeAllItemsFailedIsFailed(t *testing.T) {
	probeType, _ := registerForeachProbeExecutor(t)
	r := setupRunner(t)

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "a", "url": "a"},
		map[string]any{"slug": "b", "url": "b"},
	}, 2)
	parent.Foreach.Steps[0].ExecutorConfig.Config["fail_for"] = "a,b"
	after := successStep("after", "each")

	result := r.newPlan(t, parent, after).assertRun(t, ir.Failed)
	node := result.nodeByName(t, "each")
	assert.Equal(t, ir.NodeFailed, node.State().Status)
	assert.Contains(t, node.State().Error.Error(), "all 2 item bodies failed; first error:")
	assert.Equal(t, ir.NodeAborted, result.nodeByName(t, "after").State().Status, "the dependent does not run")

	raw, ok := node.NodeData().StringFormOutputValue()
	require.True(t, ok, "the aggregate is still published")
	var aggregate foreachAggregate
	require.NoError(t, json.Unmarshal([]byte(raw), &aggregate))
	assert.Equal(t, 2, aggregate.Summary.Failed)
	assert.Empty(t, aggregate.Outputs)
}

func TestForeachRuntimeHonorsMaxConcurrent(t *testing.T) {
	probeType, state := registerForeachProbeExecutor(t)
	r := setupRunner(t)

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "a", "url": "a"},
		map[string]any{"slug": "b", "url": "b"},
		map[string]any{"slug": "c", "url": "c"},
	}, 2)
	parent.Foreach.Steps[0].ExecutorConfig.Config["wait_for_active"] = "2"

	r.newPlan(t, parent).assertRun(t, ir.Succeeded)

	assert.Equal(t, 2, state.maxActive())
}

// Concurrent items keep body logs in foreach/<step>/<index>/ instead of one
// shared directory, and each item records its body run beside those logs.
func TestForeachRuntimeItemLogsStaySeparate(t *testing.T) {
	probeType, _ := registerForeachProbeExecutor(t)
	r := setupRunner(t)

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "one", "url": "one"},
		map[string]any{"slug": "two", "url": "two"},
	}, 2)
	result := r.newPlan(t, parent).assertRun(t, ir.Succeeded)

	base := filepath.Join(filepath.Dir(result.nodeByName(t, "each").State().Stdout), "foreach", "each")
	for _, index := range []string{"0", "1"} {
		matches, err := filepath.Glob(filepath.Join(base, index, "*.out"))
		require.NoError(t, err)
		require.Len(t, matches, 1)
	}
}

// The step records its items up front and each item's body steps with their
// log file names, so the item bodies can be inspected without the run
// status carrying them.
func TestForeachRuntimeRecordsItems(t *testing.T) {
	probeType, _ := registerForeachProbeExecutor(t)
	r := setupRunner(t)

	parent := foreachRuntimeStep(probeType, []any{
		map[string]any{"slug": "one", "url": "one"},
		map[string]any{"slug": "two", "url": "two"},
	}, 2)
	result := r.newPlan(t, parent).assertRun(t, ir.Succeeded)
	base := filepath.Join(filepath.Dir(result.nodeByName(t, "each").State().Stdout), "foreach", "each")

	var items ir.ForeachItems
	readJSONFile(t, filepath.Join(base, "items.json"), &items)
	assert.Equal(t, ir.ForeachItems{Total: 2, Items: []ir.ForeachItemRef{
		{Index: 0, Key: "one"},
		{Index: 1, Key: "two"},
	}}, items)

	var item ir.ForeachItemStatus
	readJSONFile(t, filepath.Join(base, "1", "status.json"), &item)
	assert.Equal(t, 1, item.Index)
	assert.Equal(t, "two", item.Key)
	assert.Equal(t, ir.NodeSucceeded, item.Status)
	assert.NotEmpty(t, item.StartedAt)
	assert.NotEmpty(t, item.FinishedAt)
	require.Len(t, item.Steps, 1)
	assert.Equal(t, "write", item.Steps[0].Name)
	assert.Equal(t, ir.NodeSucceeded, item.Steps[0].Status)
	assert.FileExists(t, filepath.Join(base, "1", item.Steps[0].Stdout))
	assert.FileExists(t, filepath.Join(base, "1", item.Steps[0].Stderr))
}

// A running item's record follows its body while the item is still going,
// so it can be inspected before the step finishes.
func TestForeachRuntimeRecordsRunningItem(t *testing.T) {
	probeType, _ := registerForeachProbeExecutor(t)
	r := setupRunner(t)
	release := filepath.Join(t.TempDir(), "release")

	parent := foreachRuntimeStep(probeType, []any{map[string]any{"slug": "one", "url": "one"}}, 1)
	parent.Foreach.Steps[0].ExecutorConfig.Config["wait_for_file"] = release
	ph := r.newPlan(t, parent)
	recordPath := filepath.Join(ph.cfg.LogDir, "foreach", "each", "0", "status.json")

	// The body stays blocked until the running record is seen or the wait
	// gives up; the record observed is checked once the run is over.
	observed := make(chan ir.ForeachItemStatus, 1)
	go func() {
		defer func() { _ = os.WriteFile(release, nil, 0600) }()
		deadline := time.Now().Add(platformTestDuration(10*time.Second, 20*time.Second))
		for time.Now().Before(deadline) {
			var record ir.ForeachItemStatus
			if data, err := os.ReadFile(recordPath); err == nil && json.Unmarshal(data, &record) == nil &&
				len(record.Steps) == 1 && record.Steps[0].Status == ir.NodeRunning {
				observed <- record
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		close(observed)
	}()

	ph.assertRun(t, ir.Succeeded)

	record, ok := <-observed
	require.True(t, ok, "the item record never showed the running body step")
	assert.Equal(t, ir.NodeRunning, record.Status)
	assert.Empty(t, record.FinishedAt)

	var final ir.ForeachItemStatus
	readJSONFile(t, recordPath, &final)
	assert.Equal(t, ir.NodeSucceeded, final.Status)
	assert.Equal(t, ir.NodeSucceeded, final.Steps[0].Status)
}

func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, v))
}

type foreachAggregate struct {
	Summary struct {
		Total     int `json:"total"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	} `json:"summary"`
	Items   []foreachAggregateItem `json:"items"`
	Outputs []map[string]string    `json:"outputs"`
}

type foreachAggregateItem struct {
	Index   int               `json:"index"`
	Key     string            `json:"key"`
	Status  string            `json:"status"`
	Outputs map[string]string `json:"outputs,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func foreachRuntimeStep(probeType string, items []any, maxConcurrent int) ir.Step {
	return ir.Step{
		Name:           "each",
		Output:         "RESULT",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
		Foreach: &ir.ForeachConfig{
			Items:         items,
			As:            "episode",
			Key:           "${foreach.episode.slug}",
			MaxConcurrent: maxConcurrent,
			Steps: []ir.Step{
				{
					Name: "write",
					ID:   "write",
					ExecutorConfig: ir.ExecutorConfig{
						Type: probeType,
						Config: map[string]any{
							"value": "${foreach.episode.url}",
							"key":   "${foreach.key}",
						},
					},
				},
			},
			Collect: map[string]string{
				"summary": "${write.outputs.value}",
			},
		},
	}
}

func registerForeachProbeExecutor(t *testing.T) (string, *foreachProbeState) {
	t.Helper()

	state := &foreachProbeState{activeChanged: make(chan struct{})}
	executorType := "foreach_probe_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	executor.RegisterExecutor(executorType, func(ctx context.Context, step ir.Step) (executor.Executor, error) {
		workingDir := ""
		if _, ok := step.ExecutorConfig.Config["record_working_dir"]; ok {
			workingDir = runtimepkg.GetEnv(ctx).WorkingDir
		}
		return &foreachProbeExecutor{
			state:      state,
			cfg:        step.ExecutorConfig.Config,
			workingDir: workingDir,
		}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() {
		executor.UnregisterExecutor(executorType)
		registry.UnregisterExecutorCapabilities(executorType)
	})
	return executorType, state
}

type foreachProbeState struct {
	mu            sync.Mutex
	active        int
	max           int
	seen          []foreachProbeRecord
	activeChanged chan struct{}
}

type foreachProbeRecord struct {
	Value      string
	Key        string
	WorkingDir string
}

func (s *foreachProbeState) start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.active++
	if s.active > s.max {
		s.max = s.active
	}
	close(s.activeChanged)
	s.activeChanged = make(chan struct{})
}

func (s *foreachProbeState) waitForActive(ctx context.Context, minimum int) error {
	timer := time.NewTimer(platformTestDuration(2*time.Second, 5*time.Second))
	defer timer.Stop()

	for {
		s.mu.Lock()
		maxActive := s.max
		activeChanged := s.activeChanged
		s.mu.Unlock()
		if maxActive >= minimum {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("timed out waiting for %d active foreach executions", minimum)
		case <-activeChanged:
		}
	}
}

func (s *foreachProbeState) finish(record foreachProbeRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	s.seen = append(s.seen, record)
}

func (s *foreachProbeState) maxActive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.max
}

func (s *foreachProbeState) records() []foreachProbeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]foreachProbeRecord(nil), s.seen...)
}

type foreachProbeExecutor struct {
	state      *foreachProbeState
	cfg        map[string]any
	workingDir string
	stdout     io.Writer
	stderr     io.Writer
	output     map[string]any
}

func (e *foreachProbeExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *foreachProbeExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *foreachProbeExecutor) Kill(_ os.Signal) error {
	return nil
}

func (e *foreachProbeExecutor) Run(ctx context.Context) error {
	e.state.start()
	defer func() {
		e.state.finish(foreachProbeRecord{
			Value:      stringConfigValue(e.cfg["value"]),
			Key:        stringConfigValue(e.cfg["key"]),
			WorkingDir: e.workingDir,
		})
	}()

	if value := stringConfigValue(e.cfg["wait_for_active"]); value != "" {
		minimum, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		if err := e.state.waitForActive(ctx, minimum); err != nil {
			return err
		}
	}

	// wait_for_file holds the body until the named file exists.
	if path := stringConfigValue(e.cfg["wait_for_file"]); path != "" {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(path); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}

	if delay := stringConfigValue(e.cfg["delay_ms"]); delay != "" {
		millis, err := strconv.Atoi(delay)
		if err == nil && millis > 0 {
			timer := time.NewTimer(time.Duration(millis) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}

	// fail_for names the values whose item body fails, comma-separated.
	for failing := range strings.SplitSeq(stringConfigValue(e.cfg["fail_for"]), ",") {
		if failing != "" && failing == stringConfigValue(e.cfg["value"]) {
			return fmt.Errorf("probe refused %s", failing)
		}
	}

	e.output = map[string]any{
		"value": stringConfigValue(e.cfg["value"]),
		"key":   stringConfigValue(e.cfg["key"]),
	}
	return nil
}

func (e *foreachProbeExecutor) GetOutputs() map[string]any {
	return e.output
}

func stringConfigValue(value any) string {
	v, _ := value.(string)
	return v
}
