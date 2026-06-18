// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveContainerRuntime(t *testing.T) {
	t.Run("unset_defaults_to_docker", func(t *testing.T) {
		rt, err := ResolveContainerRuntime(nil)
		require.NoError(t, err)
		assert.Equal(t, core.ContainerRuntimeDocker, rt)
	})
	t.Run("empty_defaults_to_docker", func(t *testing.T) {
		rt, err := ResolveContainerRuntime(map[string]string{ContainerRuntimeEnv: "  "})
		require.NoError(t, err)
		assert.Equal(t, core.ContainerRuntimeDocker, rt)
	})
	t.Run("docker", func(t *testing.T) {
		rt, err := ResolveContainerRuntime(map[string]string{ContainerRuntimeEnv: "docker"})
		require.NoError(t, err)
		assert.Equal(t, core.ContainerRuntimeDocker, rt)
	})
	t.Run("podman", func(t *testing.T) {
		rt, err := ResolveContainerRuntime(map[string]string{ContainerRuntimeEnv: "podman"})
		require.NoError(t, err)
		assert.Equal(t, core.ContainerRuntimePodman, rt)
	})
	t.Run("invalid_errors", func(t *testing.T) {
		_, err := ResolveContainerRuntime(map[string]string{ContainerRuntimeEnv: "nerdctl"})
		require.Error(t, err)
	})
}

func TestContainerDaemonHost(t *testing.T) {
	t.Run("docker_and_empty_use_from_env", func(t *testing.T) {
		for _, rt := range []core.ContainerRuntime{"", core.ContainerRuntimeDocker} {
			host, err := ContainerDaemonHost(rt, nil)
			require.NoError(t, err)
			assert.Equal(t, "", host)
		}
	})
	t.Run("podman_default_socket", func(t *testing.T) {
		host, err := ContainerDaemonHost(core.ContainerRuntimePodman, nil)
		require.NoError(t, err)
		assert.Equal(t, PodmanDaemonHostDefault, host)
	})
	t.Run("podman_env_override", func(t *testing.T) {
		host, err := ContainerDaemonHost(core.ContainerRuntimePodman,
			map[string]string{PodmanDaemonHostEnv: "unix:///x.sock"})
		require.NoError(t, err)
		assert.Equal(t, "unix:///x.sock", host)
	})
	t.Run("unknown_runtime_errors", func(t *testing.T) {
		_, err := ContainerDaemonHost(core.ContainerRuntime("nerdctl"), nil)
		require.Error(t, err)
	})
}

// ResolveDaemonHost is the production call site (resolve + map in one step).
func TestResolveDaemonHost(t *testing.T) {
	t.Run("nil_and_docker_yield_empty", func(t *testing.T) {
		for _, envs := range []map[string]string{
			nil,
			{ContainerRuntimeEnv: "  "},
			{ContainerRuntimeEnv: "docker"},
		} {
			host, err := ResolveDaemonHost(envs)
			require.NoError(t, err)
			assert.Equal(t, "", host, "envs %v should resolve to the docker default (empty host)", envs)
		}
	})
	t.Run("podman_default_socket", func(t *testing.T) {
		host, err := ResolveDaemonHost(map[string]string{ContainerRuntimeEnv: "podman"})
		require.NoError(t, err)
		assert.Equal(t, PodmanDaemonHostDefault, host)
	})
	t.Run("podman_host_override", func(t *testing.T) {
		host, err := ResolveDaemonHost(map[string]string{
			ContainerRuntimeEnv: "podman",
			PodmanDaemonHostEnv: "unix:///custom/podman.sock",
		})
		require.NoError(t, err)
		assert.Equal(t, "unix:///custom/podman.sock", host)
	})
	t.Run("invalid_errors", func(t *testing.T) {
		_, err := ResolveDaemonHost(map[string]string{ContainerRuntimeEnv: "nerdctl"})
		require.Error(t, err)
	})
}

// TestServiceRuntimeEnv_ReadsProcessEnvOnly locks the contract that runtime/socket
// selection is a SERVICE-LEVEL decision: ServiceRuntimeEnv must source the two
// selection keys from the engine process environment (os.LookupEnv) only, so a
// DAG- or step-level env: entry cannot override it. Regression guard for the
// workflow-overridable-selector finding.
func TestServiceRuntimeEnv_ReadsProcessEnvOnly(t *testing.T) {
	t.Run("unset_yields_empty_map", func(t *testing.T) {
		osUnsetForTest(t, ContainerRuntimeEnv)
		osUnsetForTest(t, PodmanDaemonHostEnv)
		got := ServiceRuntimeEnv()
		_, hasRT := got[ContainerRuntimeEnv]
		_, hasHost := got[PodmanDaemonHostEnv]
		assert.False(t, hasRT, "DAGU_CONTAINER_RUNTIME must be absent when unset in process env")
		assert.False(t, hasHost, "DAGU_PODMAN_HOST must be absent when unset in process env")
	})
	t.Run("reads_process_env_values", func(t *testing.T) {
		t.Setenv(ContainerRuntimeEnv, "podman")
		t.Setenv(PodmanDaemonHostEnv, "unix:///run/podman/podman.sock")
		got := ServiceRuntimeEnv()
		assert.Equal(t, "podman", got[ContainerRuntimeEnv])
		assert.Equal(t, "unix:///run/podman/podman.sock", got[PodmanDaemonHostEnv])
	})
}

// osUnsetForTest unsets an environment variable for the duration of the test,
// restoring any prior value on cleanup. t.Setenv cannot unset, so we manage it here.
func osUnsetForTest(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
