// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package convert_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestDispatchTaskToProtoClonesWorkerSelector(t *testing.T) {
	t.Parallel()

	task := &dispatch.DispatchTask{
		WorkerSelector: map[string]string{"host": "server-a"},
	}

	protoTask, err := convert.DispatchTaskToProto(task)
	require.NoError(t, err)
	require.NotNil(t, protoTask)

	task.WorkerSelector["host"] = "server-b"
	task.WorkerSelector["zone"] = "zone-1"

	assert.Equal(t, map[string]string{"host": "server-a"}, protoTask.WorkerSelector)
}

func TestDispatchTaskAttributionRoundTrips(t *testing.T) {
	t.Parallel()

	baseWorkspace := "ops"
	task := &dispatch.DispatchTask{
		BaseConfigWorkspace: &baseWorkspace,
		ProfileName:         "prod",
		DefinitionID:        "ops/daily",
		TriggerActor:        "alice",
		ParallelItem:        "item-1",
		IncludeDownstream:   true,
	}

	protoTask, err := convert.DispatchTaskToProto(task)
	require.NoError(t, err)
	require.NotNil(t, protoTask)
	assert.Equal(t, "prod", protoTask.ProfileName)
	assert.Equal(t, "ops/daily", protoTask.DefinitionId)
	assert.Equal(t, "alice", protoTask.TriggerActor)
	assert.Equal(t, "item-1", protoTask.ParallelItem)
	assert.True(t, protoTask.IncludeDownstream)
	assert.Equal(t, &baseWorkspace, protoTask.BaseConfigWorkspace)

	got, err := convert.ProtoToDispatchTask(protoTask)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "prod", got.ProfileName)
	assert.Equal(t, "ops/daily", got.DefinitionID)
	assert.Equal(t, "alice", got.TriggerActor)
	assert.Equal(t, "item-1", got.ParallelItem)
	assert.True(t, got.IncludeDownstream)
	assert.Equal(t, &baseWorkspace, got.BaseConfigWorkspace)
}

func TestRetryBypassRoundTrips(t *testing.T) {
	t.Parallel()

	for _, bypass := range []bool{false, true} {
		task := &dispatch.DispatchTask{BypassPreconditions: bypass}
		wire, err := convert.DispatchTaskToProto(task)
		require.NoError(t, err)
		data, err := proto.Marshal(wire)
		require.NoError(t, err)
		received := new(coordinatorv1.Task)
		require.NoError(t, proto.Unmarshal(data, received))
		require.Equal(t, bypass, received.BypassPreconditions)

		got, err := convert.ProtoToDispatchTask(received)
		require.NoError(t, err)
		require.Equal(t, bypass, got.BypassPreconditions)
	}
}

func TestDispatchTaskTargetWorkerRoundTrips(t *testing.T) {
	t.Parallel()

	task := &dispatch.DispatchTask{TargetWorkerID: "worker-a"}
	protoTask, err := convert.DispatchTaskToProto(task)
	require.NoError(t, err)
	assert.Equal(t, "worker-a", protoTask.TargetWorkerId)

	got, err := convert.ProtoToDispatchTask(protoTask)
	require.NoError(t, err)
	assert.Equal(t, "worker-a", got.TargetWorkerID)
}

func TestDispatchTaskToProtoValidatesOwnerPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{name: "zero", port: 0},
		{name: "max", port: 65535},
		{name: "negative", port: -1, wantErr: true},
		{name: "too_large", port: 65536, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := convert.DispatchTaskToProto(&dispatch.DispatchTask{
				Owner: dispatch.CoordinatorEndpoint{Port: tt.port},
			})
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "owner coordinator port out of range")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestProtoToDispatchTaskClonesWorkerSelector(t *testing.T) {
	t.Parallel()

	protoTask := &coordinatorv1.Task{
		WorkerSelector: map[string]string{"host": "server-a"},
	}

	task, err := convert.ProtoToDispatchTask(protoTask)
	require.NoError(t, err)
	require.NotNil(t, task)

	protoTask.WorkerSelector["host"] = "server-b"
	protoTask.WorkerSelector["zone"] = "zone-1"

	assert.Equal(t, map[string]string{"host": "server-a"}, task.WorkerSelector)
}

func TestProtoToDispatchTaskValidatesOwnerPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		port    int32
		wantErr bool
	}{
		{name: "zero", port: 0},
		{name: "max", port: 65535},
		{name: "negative", port: -1, wantErr: true},
		{name: "too_large", port: 65536, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := convert.ProtoToDispatchTask(&coordinatorv1.Task{
				OwnerCoordinatorPort: tt.port,
			})
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "owner coordinator port out of range")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestDispatchTaskPassedEnvRoundTrips(t *testing.T) {
	t.Parallel()

	task := &dispatch.DispatchTask{
		PassedEnv: []string{"TODAY=2026-01-01", "GH_USER=octocat"},
	}

	protoTask, err := convert.DispatchTaskToProto(task)
	require.NoError(t, err)
	require.NotNil(t, protoTask)
	assert.Equal(t, []string{"TODAY=2026-01-01", "GH_USER=octocat"}, protoTask.PassedEnvs)

	got, err := convert.ProtoToDispatchTask(protoTask)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []string{"TODAY=2026-01-01", "GH_USER=octocat"}, got.PassedEnv)
}
