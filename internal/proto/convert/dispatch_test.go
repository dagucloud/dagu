// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package convert_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatchTaskToProtoClonesWorkerSelector(t *testing.T) {
	t.Parallel()

	task := &exec.DispatchTask{
		WorkerSelector: map[string]string{"host": "server-a"},
	}

	protoTask, err := convert.DispatchTaskToProto(task)
	require.NoError(t, err)
	require.NotNil(t, protoTask)

	task.WorkerSelector["host"] = "server-b"
	task.WorkerSelector["zone"] = "zone-1"

	assert.Equal(t, map[string]string{"host": "server-a"}, protoTask.WorkerSelector)
}

func TestDispatchTaskProfileNameRoundTrips(t *testing.T) {
	t.Parallel()

	task := &exec.DispatchTask{ProfileName: "prod"}

	protoTask, err := convert.DispatchTaskToProto(task)
	require.NoError(t, err)
	require.NotNil(t, protoTask)
	assert.Equal(t, "prod", protoTask.ProfileName)

	got, err := convert.ProtoToDispatchTask(protoTask)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "prod", got.ProfileName)
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

			_, err := convert.DispatchTaskToProto(&exec.DispatchTask{
				Owner: exec.CoordinatorEndpoint{Port: tt.port},
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
