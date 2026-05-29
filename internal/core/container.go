// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"strconv"
	"time"
)

// Container defines the container configuration for the DAG.
type Container struct {
	// Exec specifies an existing container name to exec into.
	// Mutually exclusive with Image.
	Exec string `yaml:"exec,omitempty"`
	// Name is the container name to use. If empty, Docker generates a random name.
	Name string `yaml:"name,omitempty"`
	// Image is the container image to use.
	Image string `yaml:"image,omitempty"`
	// PullPolicy is the policy to pull the image (e.g., "Always", "IfNotPresent").
	PullPolicy PullPolicy `yaml:"pull_policy,omitempty"`
	// Env specifies environment variables for the container.
	// Serialized to JSON so it can be restored when ReadDAG deserializes from
	// the stored DAGDefinition file.
	Env []string `yaml:"env,omitempty" json:"env,omitempty"` // List of environment variables in "key=value" format
	// Volumes specifies the volumes to mount in the container.
	Volumes []string `yaml:"volumes,omitempty"` // Map of volume names to volume definitions
	// User is the user to run the container as.
	User string `yaml:"user,omitempty"` // User to run the container as
	// WorkingDir is the working directory inside the container.
	WorkingDir string `yaml:"working_dir,omitempty"` // Working directory inside the container
	// Platform specifies the platform for the container (e.g., "linux/amd64").
	Platform string `yaml:"platform,omitempty"` // Platform for the container
	// Ports specifies the ports to expose from the container.
	Ports []string `yaml:"ports,omitempty"` // List of ports to expose
	// Network is the network configuration for the container.
	Network string `yaml:"network,omitempty"` // Network configuration for the container
	// KeepContainer is the flag to keep the container after the DAG run.
	KeepContainer bool `yaml:"keep_container,omitempty"` // Keep the container after the DAG run
	// Startup determines how the DAG-level container starts up.
	// One of: "keepalive" (default), "entrypoint", "command".
	Startup ContainerStartup `yaml:"startup,omitempty"`
	// Command is used when Startup == "command".
	Command []string `yaml:"command,omitempty"`
	// WaitFor determines readiness gate before steps run: "running" (default) or "healthy".
	WaitFor ContainerWaitFor `yaml:"wait_for,omitempty"`
	// LogPattern optionally waits for a regex to appear in container logs before proceeding.
	LogPattern string `yaml:"log_pattern,omitempty"`
	// RestartPolicy applies Docker restart policy for long-running containers ("no", "always", or "unless-stopped").
	RestartPolicy string `yaml:"restart_policy,omitempty"`
	// Healthcheck specifies a custom healthcheck for the container.
	// If specified with waitFor: healthy, this healthcheck is used instead of
	// relying on the image's built-in healthcheck.
	Healthcheck *Healthcheck `yaml:"healthcheck,omitempty"`
	// Shell specifies the shell wrapper for executing step commands.
	// When specified, all step commands are wrapped with this shell.
	// Format: ["/bin/bash", "-o", "errexit", "-o", "xtrace", "-c"]
	// The step command will be appended as the final argument.
	// Works in both exec mode and image mode.
	Shell []string `yaml:"shell,omitempty"`
}

// Healthcheck defines a custom health check for a container.
// This allows waitFor: healthy to work with images that don't have built-in healthchecks.
type Healthcheck struct {
	// Test is the command to run to check health. Must start with:
	// - ["NONE"] - disable healthcheck
	// - ["CMD", "command", "arg1", ...] - run command directly
	// - ["CMD-SHELL", "command"] - run command in shell
	Test []string `yaml:"test,omitempty"`
	// Interval is the time between health checks (e.g., "5s", "1m").
	Interval time.Duration `yaml:"interval,omitempty"`
	// Timeout is how long to wait for the health check to complete (e.g., "3s").
	Timeout time.Duration `yaml:"timeout,omitempty"`
	// StartPeriod is the grace period for the container to initialize (e.g., "10s").
	StartPeriod time.Duration `yaml:"start_period,omitempty"`
	// Retries is the number of consecutive failures needed to consider unhealthy.
	Retries int `yaml:"retries,omitempty"`
}

// ContainerStartup is an enum for DAG-level container startup modes.
type ContainerStartup string

const (
	StartupKeepalive  ContainerStartup = "keepalive"
	StartupEntrypoint ContainerStartup = "entrypoint"
	StartupCommand    ContainerStartup = "command"
)

// ContainerWaitFor is an enum for container readiness conditions.
type ContainerWaitFor string

const (
	WaitForRunning ContainerWaitFor = "running"
	WaitForHealthy ContainerWaitFor = "healthy"
)

// GetWorkingDir returns the working directory inside the container
func (ct Container) GetWorkingDir() string {
	return ct.WorkingDir
}

// IsExecMode returns true if this container is configured to exec into an existing container
func (ct Container) IsExecMode() bool {
	return ct.Exec != ""
}

// PullPolicy defines image pull policy for a container execution
type PullPolicy int

func (p PullPolicy) String() string {
	switch p {
	case PullPolicyAlways:
		return "always"
	case PullPolicyNever:
		return "never"
	case PullPolicyMissing:
		return "missing"
	default:
		return "unknown"
	}
}

const (
	PullPolicyAlways PullPolicy = iota
	PullPolicyNever
	PullPolicyMissing
)

var pullPolicyMap = map[string]PullPolicy{
	"always":  PullPolicyAlways,
	"missing": PullPolicyMissing,
	"never":   PullPolicyNever,
}

// boolToPullPolicy converts a boolean to a PullPolicy.
func boolToPullPolicy(b bool) PullPolicy {
	if b {
		return PullPolicyAlways
	}
	return PullPolicyNever
}

// ParsePullPolicy parses a pull policy from a raw value.
func ParsePullPolicy(raw any) (PullPolicy, error) {
	switch value := raw.(type) {
	case nil:
		// If the value is nil, return PullPolicyMissing
		return PullPolicyMissing, nil
	case string:
		if value == "" {
			// If the string is empty, return PullPolicyMissing
			return PullPolicyMissing, nil
		}
		// Try to parse the string as a pull policy
		pull, ok := pullPolicyMap[value]
		if ok {
			return pull, nil
		}

		// If the string is not a valid pull policy, try to parse it as a boolean
		b, err := strconv.ParseBool(value)
		if err != nil {
			return PullPolicyMissing, fmt.Errorf("failed to parse pull policy as boolean: %w", err)
		}
		return boolToPullPolicy(b), nil
	case bool:
		return boolToPullPolicy(value), nil
	default:
		return PullPolicyMissing, fmt.Errorf("invalid pull policy type: %T", raw)
	}
}
