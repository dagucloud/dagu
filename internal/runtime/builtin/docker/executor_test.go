// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewDocker_SelectsDaemonHostFromServiceEnv proves that NATIVE container jobs
// (step-level container: blocks and the legacy executor.config map) honor the
// service-level DAGU_CONTAINER_RUNTIME setting, the same selector used by the
// harness and DAG-level container paths. Runtime selection comes from the engine
// process env (ServiceRuntimeEnv), so these tests set it with t.Setenv.
func TestNewDocker_SelectsDaemonHostFromServiceEnv(t *testing.T) {
	withEnv := func(ctx context.Context) context.Context {
		return runtime.WithEnv(ctx, runtime.NewEnv(ctx, core.Step{Name: "test"}))
	}
	daemonHostOf := func(t *testing.T, e any) string {
		t.Helper()
		d, ok := e.(*docker)
		require.True(t, ok, "executor is *docker")
		require.NotNil(t, d.cfg, "native container job should build a Config")
		return d.cfg.DaemonHost
	}

	t.Run("step_container_image_mode_podman", func(t *testing.T) {
		t.Setenv(ContainerRuntimeEnv, "podman")
		ctx := withEnv(context.Background())
		step := core.Step{Name: "job", Container: &core.Container{Image: "alpine:latest"}}
		exec, err := newDocker(ctx, step)
		require.NoError(t, err)
		assert.Equal(t, PodmanDaemonHostDefault, daemonHostOf(t, exec))
	})

	t.Run("step_container_exec_mode_podman", func(t *testing.T) {
		t.Setenv(ContainerRuntimeEnv, "podman")
		ctx := withEnv(context.Background())
		step := core.Step{Name: "job", Container: &core.Container{Exec: "existing"}}
		exec, err := newDocker(ctx, step)
		require.NoError(t, err)
		assert.Equal(t, PodmanDaemonHostDefault, daemonHostOf(t, exec))
	})

	t.Run("legacy_map_config_podman", func(t *testing.T) {
		t.Setenv(ContainerRuntimeEnv, "podman")
		ctx := withEnv(context.Background())
		step := core.Step{
			Name: "job",
			ExecutorConfig: core.ExecutorConfig{
				Type:   "docker",
				Config: map[string]any{"image": "alpine:latest"},
			},
		}
		exec, err := newDocker(ctx, step)
		require.NoError(t, err)
		assert.Equal(t, PodmanDaemonHostDefault, daemonHostOf(t, exec))
	})

	t.Run("docker_and_unset_leave_daemon_host_empty", func(t *testing.T) {
		// Unset -> docker default; explicit docker -> docker default. Either way
		// DaemonHost stays empty so the client keeps upstream client.FromEnv.
		for _, val := range []string{"", "docker"} {
			if val == "" {
				osUnsetForTest(t, ContainerRuntimeEnv)
			} else {
				t.Setenv(ContainerRuntimeEnv, val)
			}
			ctx := withEnv(context.Background())
			step := core.Step{Name: "job", Container: &core.Container{Image: "alpine:latest"}}
			exec, err := newDocker(ctx, step)
			require.NoError(t, err)
			assert.Equal(t, "", daemonHostOf(t, exec), "runtime %q must leave DaemonHost empty", val)
		}
	})

	t.Run("invalid_runtime_fails_the_step", func(t *testing.T) {
		t.Setenv(ContainerRuntimeEnv, "nerdctl")
		ctx := withEnv(context.Background())
		step := core.Step{Name: "job", Container: &core.Container{Image: "alpine:latest"}}
		_, err := newDocker(ctx, step)
		require.Error(t, err)
		assert.Contains(t, err.Error(), ContainerRuntimeEnv)
	})
}
