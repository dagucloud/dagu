// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"

	"github.com/dagucloud/dagu/v2/internal/ir"
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
	executor.RegisterExecutor(executorType, func(_ context.Context, step ir.Step) (executor.Executor, error) {
		return &foreachProbeExecutor{
			state: state,
			cfg:   step.ExecutorConfig.Config,
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
	Value string
	Key   string
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
	state  *foreachProbeState
	cfg    map[string]any
	stdout io.Writer
	stderr io.Writer
	output map[string]any
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
			Value: stringConfigValue(e.cfg["value"]),
			Key:   stringConfigValue(e.cfg["key"]),
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
