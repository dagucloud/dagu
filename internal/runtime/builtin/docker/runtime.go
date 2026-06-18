// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"fmt"
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
)

// Container runtime selection is a SERVICE-LEVEL setting, not a per-step or
// per-DAG YAML field. DAGU_CONTAINER_RUNTIME chooses docker (default) or podman
// for every containerized step in this engine: harness.run agent steps,
// step-level container jobs, and the DAG-level container. DAGU_PODMAN_HOST
// optionally overrides podman's Docker-compatible socket path.
const (
	PodmanDaemonHostDefault = "unix:///run/podman/podman.sock"
	PodmanDaemonHostEnv     = "DAGU_PODMAN_HOST"
	ContainerRuntimeEnv     = "DAGU_CONTAINER_RUNTIME"
)

// ServiceRuntimeEnv returns the runtime-selection variables read from the engine
// PROCESS environment only (os.LookupEnv), never from the DAG/step runtime scope.
// Runtime selection is a service-level decision, so it must not be overridable by
// a DAG- or step-level env: entry (the runtime scope precedence is
// StepEnv > Outputs > Secrets > DAGEnv > OS, which would let a workflow redirect
// the daemon socket). Only the two selection keys are surfaced.
func ServiceRuntimeEnv() map[string]string {
	m := make(map[string]string, 2)
	if v, ok := os.LookupEnv(ContainerRuntimeEnv); ok {
		m[ContainerRuntimeEnv] = v
	}
	if v, ok := os.LookupEnv(PodmanDaemonHostEnv); ok {
		m[PodmanDaemonHostEnv] = v
	}
	return m
}

// ResolveContainerRuntime reads the deployment's DAGU_CONTAINER_RUNTIME setting
// and parses it into the runtime enum, defaulting to docker when unset. This is
// the single place that decides docker vs podman; there is no per-step field.
func ResolveContainerRuntime(envs map[string]string) (core.ContainerRuntime, error) {
	raw := ""
	if envs != nil {
		raw = strings.TrimSpace(envs[ContainerRuntimeEnv])
	}
	if raw == "" {
		return core.ContainerRuntimeDocker, nil
	}
	rt, err := core.ParseContainerRuntime(raw)
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", ContainerRuntimeEnv, err)
	}
	return rt, nil
}

// ContainerDaemonHost maps the resolved container runtime to the daemon API host
// the Moby SDK client should drive. docker (or empty) returns "" so the client
// keeps upstream client.FromEnv behavior (honoring DOCKER_HOST). podman returns
// its Docker-compatible socket, overridable via DAGU_PODMAN_HOST.
func ContainerDaemonHost(rt core.ContainerRuntime, envs map[string]string) (string, error) {
	switch rt {
	case "", core.ContainerRuntimeDocker:
		return "", nil
	case core.ContainerRuntimePodman:
		if envs != nil {
			if host := strings.TrimSpace(envs[PodmanDaemonHostEnv]); host != "" {
				return host, nil
			}
		}
		return PodmanDaemonHostDefault, nil
	default:
		return "", fmt.Errorf("unsupported container runtime %q", rt)
	}
}

// ResolveDaemonHost is the production call site: it resolves DAGU_CONTAINER_RUNTIME
// from the given env map and returns the daemon API host to drive ("" for docker,
// the podman socket for podman). Callers pass ServiceRuntimeEnv() so the selection
// comes only from the engine process environment.
func ResolveDaemonHost(envs map[string]string) (string, error) {
	rt, err := ResolveContainerRuntime(envs)
	if err != nil {
		return "", err
	}
	return ContainerDaemonHost(rt, envs)
}
