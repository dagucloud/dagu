// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testSnapshotSoulStore struct {
	souls map[string]*Soul
}

func (s *testSnapshotSoulStore) Create(context.Context, *Soul) error { return nil }
func (s *testSnapshotSoulStore) GetByID(_ context.Context, id string) (*Soul, error) {
	soul, ok := s.souls[id]
	if !ok {
		return nil, ErrSoulNotFound
	}
	return soul, nil
}
func (s *testSnapshotSoulStore) List(context.Context) ([]*Soul, error) { return nil, nil }
func (s *testSnapshotSoulStore) Search(context.Context, SearchSoulsOptions) (*exec.PaginatedResult[SoulMetadata], error) {
	result := exec.NewPaginatedResult([]SoulMetadata{}, 0, exec.DefaultPaginator())
	return &result, nil
}
func (s *testSnapshotSoulStore) Update(context.Context, *Soul) error  { return nil }
func (s *testSnapshotSoulStore) Delete(context.Context, string) error { return nil }

func TestMarshalSnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	snapshot := &Snapshot{
		Config: &Config{
			Enabled:        true,
			DefaultModelID: "model-default",
		},
		Models: []*ModelConfig{
			testModelConfig("model-default"),
		},
		Souls: []*Soul{
			{ID: "helper", Name: "Helper", Content: "be precise"},
		},
		Memory: &MemorySnapshot{
			Global: "global memory",
			PerDAG: map[string]string{"parent": "parent memory"},
		},
	}

	data, err := MarshalSnapshot(snapshot)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	decoded, err := UnmarshalSnapshot(data)
	require.NoError(t, err)
	require.NotNil(t, decoded)

	assert.Equal(t, SnapshotVersion, decoded.Version)
	require.NotNil(t, decoded.Config)
	assert.Equal(t, "model-default", decoded.Config.DefaultModelID)
	require.Len(t, decoded.Models, 1)
	assert.Equal(t, "model-default", decoded.Models[0].ID)
	require.Len(t, decoded.Souls, 1)
	assert.Equal(t, "helper", decoded.Souls[0].ID)
	require.NotNil(t, decoded.Memory)
	assert.Equal(t, "global memory", decoded.Memory.Global)
	assert.Equal(t, "parent memory", decoded.Memory.PerDAG["parent"])
}

func TestUnmarshalSnapshotRejectsUnknownVersion(t *testing.T) {
	t.Parallel()

	data := gzipJSON(t, map[string]any{
		"version": 99,
	})

	snapshot, err := UnmarshalSnapshot(data)
	require.Nil(t, snapshot)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnsupportedSnapshotWire))
}

func TestUnmarshalSnapshotRejectsOversizedUncompressedPayload(t *testing.T) {
	t.Parallel()

	data := gzipBytes(t, bytes.Repeat([]byte{'a'}, maxUncompressedSnapshotBytes+1))

	snapshot, err := UnmarshalSnapshot(data)
	require.Nil(t, snapshot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent snapshot exceeds max uncompressed size")
}

func TestBuildSnapshotForDAG_SkipsNonAgentGraphs(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Name: "plain",
		Steps: []core.Step{
			{Name: "main"},
		},
	}

	data, err := BuildSnapshotForDAG(context.Background(), dag, SnapshotStores{}, SnapshotBuildOptions{})
	require.NoError(t, err)
	require.Nil(t, data)
}

func TestBuildSnapshotForDAG_CapturesLocalSubDAGRequirements(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	configStore := newMockConfigStore(true)
	configStore.config.DefaultModelID = "default-model"
	configStore.config.WebTools = &WebToolsConfig{
		Enabled: true,
		Backend: WebToolsBackendTavily,
		Tavily: &TavilyWebToolsConfig{
			APIKey: "tvly-test",
		},
	}

	modelStore := newMockModelStore().
		addModel(testModelConfig("default-model")).
		addModel(testModelConfig("child-model"))

	soulStore := &testSnapshotSoulStore{
		souls: map[string]*Soul{
			"helper": {ID: "helper", Name: "Helper", Content: "be precise"},
		},
	}
	memoryStore := newMockMemoryStore()
	memoryStore.global = "global memory"
	memoryStore.dag["parent"] = "parent memory"
	memoryStore.dag["child"] = "child memory"

	child := &core.DAG{
		Name: "child",
		Steps: []core.Step{
			{
				Name: "child-agent",
				Agent: &core.AgentStepConfig{
					Model:  "child-model",
					Soul:   "helper",
					Memory: &core.AgentMemoryConfig{Enabled: true},
				},
			},
		},
	}
	parent := &core.DAG{
		Name: "parent",
		Steps: []core.Step{
			{
				Name: "root-agent",
				Agent: &core.AgentStepConfig{
					Memory: &core.AgentMemoryConfig{Enabled: true},
				},
			},
			{
				Name:   "call-child",
				SubDAG: &core.SubDAG{Name: "child"},
			},
		},
		LocalDAGs: map[string]*core.DAG{
			"child": child,
		},
	}

	data, err := BuildSnapshotForDAG(ctx, parent, SnapshotStores{
		ConfigStore: configStore,
		ModelStore:  modelStore,
		SoulStore:   soulStore,
		MemoryStore: memoryStore,
	}, SnapshotBuildOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, data)

	decoded, err := UnmarshalSnapshot(data)
	require.NoError(t, err)
	require.NotNil(t, decoded)

	assert.Equal(t, []string{"child-model", "default-model"}, modelIDs(decoded.Models))
	require.NotNil(t, decoded.Config)
	require.NotNil(t, decoded.Config.WebTools)
	require.NotNil(t, decoded.Config.WebTools.Tavily)
	assert.Equal(t, "tvly-test", decoded.Config.WebTools.Tavily.APIKey)
	assert.Equal(t, []string{"helper"}, soulIDs(decoded.Souls))
	require.NotNil(t, decoded.Memory)
	assert.Equal(t, "global memory", decoded.Memory.Global)
	assert.Equal(t, map[string]string{
		"child":  "child memory",
		"parent": "parent memory",
	}, decoded.Memory.PerDAG)
}

func TestBuildSnapshotForDAG_OnlySnapshotsMemoryForEnabledDAGs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	configStore := newMockConfigStore(true)
	configStore.config.DefaultModelID = "default-model"

	modelStore := newMockModelStore().
		addModel(testModelConfig("default-model")).
		addModel(testModelConfig("child-model"))

	memoryStore := newMockMemoryStore()
	memoryStore.global = "global memory"
	memoryStore.dag["parent"] = "parent memory"
	memoryStore.dag["child"] = "child memory"

	child := &core.DAG{
		Name: "child",
		Steps: []core.Step{
			{
				Name: "child-agent",
				Agent: &core.AgentStepConfig{
					Model: "child-model",
				},
			},
		},
	}
	parent := &core.DAG{
		Name: "parent",
		Steps: []core.Step{
			{
				Name: "root-agent",
				Agent: &core.AgentStepConfig{
					Memory: &core.AgentMemoryConfig{Enabled: true},
				},
			},
			{
				Name:   "call-child",
				SubDAG: &core.SubDAG{Name: "child"},
			},
		},
		LocalDAGs: map[string]*core.DAG{
			"child": child,
		},
	}

	data, err := BuildSnapshotForDAG(ctx, parent, SnapshotStores{
		ConfigStore: configStore,
		ModelStore:  modelStore,
		MemoryStore: memoryStore,
	}, SnapshotBuildOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, data)

	decoded, err := UnmarshalSnapshot(data)
	require.NoError(t, err)
	require.NotNil(t, decoded)
	require.NotNil(t, decoded.Memory)
	assert.Equal(t, "global memory", decoded.Memory.Global)
	assert.Equal(t, map[string]string{
		"parent": "parent memory",
	}, decoded.Memory.PerDAG)
}

func TestBuildSnapshotForDAG_FailsWhenSoulMissing(t *testing.T) {
	t.Parallel()

	configStore := newMockConfigStore(true)
	configStore.config.DefaultModelID = "default-model"
	modelStore := newMockModelStore().addModel(testModelConfig("default-model"))
	soulStore := &testSnapshotSoulStore{souls: map[string]*Soul{}}

	dag := &core.DAG{
		Name: "agent-dag",
		Steps: []core.Step{
			{
				Name: "agent-step",
				Agent: &core.AgentStepConfig{
					Soul: "missing-soul",
				},
			},
		},
	}

	data, err := BuildSnapshotForDAG(context.Background(), dag, SnapshotStores{
		ConfigStore: configStore,
		ModelStore:  modelStore,
		SoulStore:   soulStore,
	}, SnapshotBuildOptions{})
	require.Nil(t, data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `load soul "missing-soul" for snapshot`)
	assert.True(t, errors.Is(err, ErrSoulNotFound))
}

func TestBuildSnapshotForDAG_PropagatesResolveErrors(t *testing.T) {
	t.Parallel()

	resolveErr := errors.New("boom")
	dag := &core.DAG{
		Name: "parent",
		Steps: []core.Step{
			{
				Name:   "call-child",
				SubDAG: &core.SubDAG{Name: "child"},
			},
		},
	}

	data, err := BuildSnapshotForDAG(context.Background(), dag, SnapshotStores{}, SnapshotBuildOptions{
		ResolveDAG: func(context.Context, string) (*core.DAG, error) {
			return nil, resolveErr
		},
	})
	require.Nil(t, data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `resolve subdag "child" for snapshot`)
	assert.ErrorIs(t, err, resolveErr)
}

func TestNeedsSnapshotForDAG_PropagatesResolveErrors(t *testing.T) {
	t.Parallel()

	resolveErr := errors.New("boom")
	dag := &core.DAG{
		Name: "parent",
		Steps: []core.Step{
			{
				Name:   "call-child",
				SubDAG: &core.SubDAG{Name: "child"},
			},
		},
	}

	needsSnapshot, err := NeedsSnapshotForDAG(context.Background(), dag, func(context.Context, string) (*core.DAG, error) {
		return nil, resolveErr
	})
	assert.False(t, needsSnapshot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `resolve subdag "child" for snapshot`)
	assert.ErrorIs(t, err, resolveErr)
}

func TestBuildSnapshotForDAG_EnforcesSnapshotSizeLimit(t *testing.T) {
	t.Parallel()

	configStore := newMockConfigStore(true)
	configStore.config.DefaultModelID = "default-model"
	modelStore := newMockModelStore().addModel(testModelConfig("default-model"))

	dag := &core.DAG{
		Name: "agent-dag",
		Steps: []core.Step{
			{
				Name:  "agent-step",
				Agent: &core.AgentStepConfig{},
			},
		},
	}

	data, err := BuildSnapshotForDAG(context.Background(), dag, SnapshotStores{
		ConfigStore: configStore,
		ModelStore:  modelStore,
	}, SnapshotBuildOptions{MaxBytes: 1})
	require.Nil(t, data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent snapshot exceeds max size")
}

func gzipJSON(t *testing.T, v any) []byte {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err = zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	out, err := io.ReadAll(&buf)
	require.NoError(t, err)
	return out
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	out, err := io.ReadAll(&buf)
	require.NoError(t, err)
	return out
}

func modelIDs(models []*ModelConfig) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		out = append(out, model.ID)
	}
	sort.Strings(out)
	return out
}

func soulIDs(souls []*Soul) []string {
	out := make([]string, 0, len(souls))
	for _, soul := range souls {
		out = append(out, soul.ID)
	}
	sort.Strings(out)
	return out
}
