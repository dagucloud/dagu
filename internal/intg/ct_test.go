// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	testImage       = "alpine:3"
	nginxTestImage  = "nginx:alpine"
	containerPrefix = "dagu-test"
)

type dockerExecutorTest struct {
	name            string
	dagConfig       string
	expectedOutputs map[string]any
}

func currentDockerPlatform(ctx context.Context, dockerClient *client.Client) (specs.Platform, error) {
	info, err := dockerClient.Info(ctx, client.InfoOptions{})
	if err != nil {
		return specs.Platform{}, err
	}

	return specs.Platform{
		Architecture: info.Info.Architecture,
		OS:           info.Info.OSType,
	}, nil
}

func inspectContainer(ctx context.Context, dockerClient *client.Client, containerID string) (container.InspectResponse, error) {
	result, err := dockerClient.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, err
	}
	return result.Container, nil
}

func TestDockerExecutor(t *testing.T) {
	requireDockerDaemon(t)
	requireLinuxContainerRuntime(t)

	tests := []dockerExecutorTest{
		{
			name: "BasicExecution",
			dagConfig: `
env:
  - FOO: BAR
  - ABC=XYZ
steps:
  - action: docker.run
    with:
      image: alpine:3
      auto_remove: true
      command: echo 123 abc $FOO $ABC
    output: DOCKER_EXEC_OUT1
`,
			expectedOutputs: map[string]any{
				"DOCKER_EXEC_OUT1": "123 abc BAR XYZ",
			},
		},
		{
			name: "AutoStartContainer",
			dagConfig: `
steps:
  - action: docker.run
    with:
      image: alpine:3
      auto_remove: true
      container_name: dagu-autostart
      command: echo "container started"
    output: DOCKER_EXEC_OUT1
`,
			expectedOutputs: map[string]any{
				"DOCKER_EXEC_OUT1": "container started",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th := test.Setup(t)
			dag := th.DAG(t, tt.dagConfig)
			dag.Agent().RunSuccess(t)
			dag.AssertLatestStatus(t, core.Succeeded)
			dag.AssertOutputs(t, tt.expectedOutputs)
		})
	}
}

type containerTest struct {
	name            string
	dagConfigFunc   func(tempDir string) string
	expectedOutputs map[string]any
	setupFunc       func(t *testing.T, tempDir string)
}

func TestDAGLevelContainer(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()
	requireLinuxContainerRuntime(t)

	tests := []containerTest{
		{
			name: "VolumeBindMounts",
			dagConfigFunc: func(tempDir string) string {
				return fmt.Sprintf(`
container:
  image: %s
  volumes:
    - %s:/data:rw
steps:
  - run: sh -c "echo 'Hello from step 1' > /data/test.txt"
  - run: cat /data/test.txt
    depends: container_1
    output: BIND_MOUNT_OUT1
  - run: sh -c "echo 'Hello from step 3' >> /data/test.txt"
    depends: container_2
  - run: cat /data/test.txt
    depends: container_3
    output: BIND_MOUNT_OUT2
`, testImage, tempDir)
			},
			expectedOutputs: map[string]any{
				"BIND_MOUNT_OUT1": "Hello from step 1",
				"BIND_MOUNT_OUT2": "Hello from step 1\nHello from step 3",
			},
		},
		{
			name: "BasicExecution",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
env:
  - FOO: BAR
container:
  image: %s
steps:
  - run: echo 123 abc $FOO
    output: CONTAINER_BASIC_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"CONTAINER_BASIC_OUT1": "123 abc BAR",
			},
		},
		{
			name: "DollarEscape_NoShell",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
steps:
  - run: printf '%%s' '\$HOME'
    output: DOLLAR_NO_SHELL_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"DOLLAR_NO_SHELL_OUT1": "$HOME",
			},
		},
		{
			name: "DollarEscape_WithShell",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  shell: ["/bin/sh", "-c"]
steps:
  - run: 'echo "\$HOME"'
    output: DOLLAR_SHELL_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"DOLLAR_SHELL_OUT1": "$HOME",
			},
		},
		{
			name: "CommandWithArguments",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
steps:
  - run: echo hello world
    output: CMD_WITH_ARGS_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"CMD_WITH_ARGS_OUT1": "hello world",
			},
		},
		{
			name: "WorkingDirectory",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  working_dir: /tmp
steps:
  - run: pwd
    output: WORK_DIR_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"WORK_DIR_OUT1": "/tmp",
			},
		},
		{
			name: "UserSpecification",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  user: "nobody"
steps:
  - run: whoami
    output: WITH_USER_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"WITH_USER_OUT1": "nobody",
			},
		},
		{
			name: "NamedVolume",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  volumes:
    - test-volume:/data
steps:
  - run: sh -c "echo 'Data in named volume' > /data/volume.txt"
  - run: cat /data/volume.txt
    output: NAMED_VOL_OUT1
  - run: ls -la /data/
    output: NAMED_VOL_OUT2
`, testImage)
			},
			expectedOutputs: map[string]any{
				"NAMED_VOL_OUT1": "Data in named volume",
			},
		},
		{
			name: "RelativeBindMountsWithWorkingDirectory",
			setupFunc: func(t *testing.T, tempDir string) {
				subDir := fmt.Sprintf("%s/work", tempDir)
				if err := os.MkdirAll(subDir, 0o755); err != nil {
					t.Fatalf("failed to create subdirectory %s: %v", subDir, err)
				}

				testFile := fmt.Sprintf("%s/initial.txt", subDir)
				if err := os.WriteFile(testFile, []byte("Initial content"), 0o644); err != nil {
					t.Fatalf("failed to create test file %s: %v", testFile, err)
				}
			},
			dagConfigFunc: func(tempDir string) string {
				subDir := fmt.Sprintf("%s/work", tempDir)
				return fmt.Sprintf(`
working_dir: %s
container:
  image: %s
  volumes:
    - ./:/workspace:rw
steps:
  - run: cat /workspace/initial.txt
    output: WORK_DIR_VOL_OUT1
  - run: sh -c "echo 'New content' > /workspace/new.txt"
  - run: cat /workspace/new.txt
    depends: container_2
    output: WORK_DIR_VOL_OUT2
  - run: ls -la /workspace/
    output: WORK_DIR_VOL_OUT3
`, subDir, testImage)
			},
			expectedOutputs: map[string]any{
				"WORK_DIR_VOL_OUT1": "Initial content",
				"WORK_DIR_VOL_OUT2": "New content",
			},
		},
		{
			// Test for: https://github.com/dagucloud/dagu/issues/1589
			// Shell field allows DRY approach to wrapping commands with a shell
			name: "ShellFieldBasic",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  shell: ["/bin/sh", "-c"]
steps:
  - run: echo hello shell
    output: SHELL_BASIC_OUT1
  - run: echo world
    output: SHELL_BASIC_OUT2
`, testImage)
			},
			expectedOutputs: map[string]any{
				"SHELL_BASIC_OUT1": "hello shell",
				"SHELL_BASIC_OUT2": "world",
			},
		},
		{
			// Test shell field with command chaining operator
			name: "ShellFieldWithOperators",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  shell: ["/bin/sh", "-c"]
steps:
  - run: echo line1 && echo line2
    output: SHELL_OPERATOR_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"SHELL_OPERATOR_OUT1": "line1\nline2",
			},
		},
		{
			// Test shell field without -c flag (auto-added)
			name: "ShellFieldAutoAddsFlag",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  shell: ["/bin/sh"]
steps:
  - run: echo auto-flag-test
    output: SHELL_AUTO_FLAG_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"SHELL_AUTO_FLAG_OUT1": "auto-flag-test",
			},
		},
		{
			// Test shell field with pipe operator
			name: "ShellFieldWithPipe",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  shell: ["/bin/sh", "-c"]
steps:
  - run: echo hello | tr a-z A-Z
    output: SHELL_PIPE_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"SHELL_PIPE_OUT1": "HELLO",
			},
		},
		{
			// Test shell strict mode flags from issue #1589
			// Using /bin/sh with errexit flag (alpine doesn't have bash)
			name: "ShellFieldWithStrictMode",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
  shell: ["/bin/sh", "-e", "-x", "-c"]
steps:
  - run: echo "strict mode enabled"
    output: STRICT_MODE_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STRICT_MODE_OUT1": "strict mode enabled",
			},
		},
		{
			// Test backward compatibility: no shell field specified
			name: "NoShellFieldBackwardCompat",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
container:
  image: %s
steps:
  - run: echo no shell wrapper
    output: NO_SHELL_OUT1
`, testImage)
			},
			expectedOutputs: map[string]any{
				"NO_SHELL_OUT1": "no shell wrapper",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", fmt.Sprintf("%s-%s-*", containerPrefix, tt.name))
			require.NoError(t, err, "failed to create temporary directory")
			t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

			if tt.setupFunc != nil {
				tt.setupFunc(t, tempDir)
			}

			th := test.Setup(t)
			dag := th.DAG(t, tt.dagConfigFunc(tempDir))
			dag.Agent().RunSuccess(t)
			dag.AssertLatestStatus(t, core.Succeeded)
			dag.AssertOutputs(t, tt.expectedOutputs)
		})
	}
}

func TestContainerPullPolicy(t *testing.T) {
	requireDockerDaemon(t)
	requireLinuxContainerRuntime(t)

	th := test.Setup(t)

	pullPolicyTestDAG := fmt.Sprintf(`
container:
  image: %s
  pull_policy: never
steps:
  - run: echo 'pull policy test'
    output: OUT1
`, testImage)

	ensureImageDAG := fmt.Sprintf(`
container:
  image: %s
steps:
  - run: "true"
`, testImage)

	// First, ensure the image exists by running with default pull policy
	ensureImageDag := th.DAG(t, ensureImageDAG)
	ensureImageDag.Agent().RunSuccess(t)

	// Now test that pull policy "never" works with the pre-existing image
	dag := th.DAG(t, pullPolicyTestDAG)
	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"OUT1": "pull policy test",
	})
}

func TestContainerStartup_Entrypoint_WithHealthyFallback(t *testing.T) {
	requireDockerDaemon(t)
	requireLinuxContainerRuntime(t)

	th := test.Setup(t)

	// Use nginx which stays up by default; most tags have no healthcheck,
	// so waitFor: healthy should fall back to running.
	dagConfig := fmt.Sprintf(`
container:
  image: %s
  startup: entrypoint
  wait_for: healthy
steps:
  - run: echo entrypoint-ok
    output: ENTRYPOINT_OK
`, nginxTestImage)

	dag := th.DAG(t, dagConfig)
	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"ENTRYPOINT_OK": "entrypoint-ok",
	})
}

func TestContainerStartup_Command_LongRunning(t *testing.T) {
	requireDockerDaemon(t)
	requireLinuxContainerRuntime(t)
	t.Parallel()

	th := test.Setup(t)

	dagConfig := fmt.Sprintf(`
container:
  image: %s
  startup: command
  command: ["sh", "-c", "while true; do sleep 3600; done"]
steps:
  - run: echo command-ok
    output: COMMAND_OK
`, testImage)

	dag := th.DAG(t, dagConfig)
	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"COMMAND_OK": "command-ok",
	})
}

func TestDockerExecutor_ExecInExistingContainer(t *testing.T) {
	requireLinuxContainerRuntime(t)

	th := test.Setup(t)
	dockerClient := requireDockerClient(t)

	containerName := fmt.Sprintf("dagu-existing-%d", time.Now().UnixNano())
	containerID := createLongRunningContainer(t, th, dockerClient, containerName)
	defer removeContainer(t, th, dockerClient, containerID)

	dagConfig := fmt.Sprintf(`
steps:
  - action: docker.run
    with:
      container_name: %s
      exec:
        working_dir: /
      command: echo hello-existing
    output: EXEC_EXISTING_OUT
`, containerName)

	dag := th.DAG(t, dagConfig)
	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"EXEC_EXISTING_OUT": "hello-existing",
	})
}

func TestDockerExecutor_ErrorIncludesRecentStderr(t *testing.T) {
	requireDockerDaemon(t)
	requireLinuxContainerRuntime(t)

	th := test.Setup(t)

	dagConfig := fmt.Sprintf(`
steps:
  - action: docker.run
    with:
      image: %s
      auto_remove: true
      command: sh -c 'echo first 1>&2; echo second 1>&2; exit 7'
`, testImage)

	dag := th.DAG(t, dagConfig)
	agent := dag.Agent()

	err := agent.Run(agent.Context)
	require.Error(t, err)

	// Should contain recent stderr from docker executor
	require.Contains(t, err.Error(), "first")
	require.Contains(t, err.Error(), "second")
}

// Helper functions
func createLongRunningContainer(t *testing.T, th test.Helper, dockerClient *client.Client, containerName string) string {
	platform, err := currentDockerPlatform(th.Context, dockerClient)
	if err != nil {
		t.Fatalf("failed to get docker platform: %v", err)
	}
	pullOpts := client.ImagePullOptions{Platforms: []specs.Platform{platform}}

	// Pull the image to ensure it exists; consume the stream so the daemon registers it
	reader, err := dockerClient.ImagePull(th.Context, testImage, pullOpts)
	if err != nil {
		t.Fatalf("failed to pull image %s: %v", testImage, err)
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		_ = reader.Close()
		t.Fatalf("failed to read pull response for %s: %v", testImage, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("failed to close pull response for %s: %v", testImage, err)
	}

	// Create and start the container
	created, err := dockerClient.ContainerCreate(th.Context, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: testImage,
			Cmd:   []string{"sh", "-c", "while true; do sleep 3600; done"},
		},
		HostConfig: &container.HostConfig{AutoRemove: true},
		Name:       containerName,
	})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	if _, err := dockerClient.ContainerStart(th.Context, created.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("failed to start container: %v", err)
	}

	// Wait for container to be running
	if err := waitForContainerRunning(th.Context, dockerClient, created.ID); err != nil {
		t.Fatalf("failed to wait for container to be running: %v", err)
	}

	return created.ID
}

func removeContainer(t *testing.T, th test.Helper, dockerClient *client.Client, containerID string) {
	t.Helper()

	const (
		stopTimeout  = 5 * time.Second
		pollInterval = 100 * time.Millisecond
	)

	// Stop the container gracefully
	if _, err := dockerClient.ContainerStop(th.Context, containerID, client.ContainerStopOptions{}); err != nil {
		t.Logf("failed to stop container %s: %v", containerID, err)
	}

	// Wait for container to stop with timeout
	if !waitForContainerStop(t, th, dockerClient, containerID, stopTimeout, pollInterval) {
		t.Logf("timeout waiting for container %s to stop, forcing removal", containerID)
	}

	// Remove the container
	if _, err := dockerClient.ContainerRemove(th.Context, containerID, client.ContainerRemoveOptions{Force: true}); err != nil {
		t.Logf("failed to remove container %s: %v", containerID, err)
	}
}

func waitForContainerRunning(ctx context.Context, dockerClient *client.Client, containerID string) error {
	const (
		timeout      = 30 * time.Second
		pollInterval = 100 * time.Millisecond
	)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inspect, err := inspectContainer(ctx, dockerClient, containerID)
		if err != nil {
			return fmt.Errorf("failed to inspect container %s: %w", containerID, err)
		}
		if inspect.State.Running {
			return nil
		}
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("timeout waiting for container %s to be running", containerID)
}

func waitForContainerStop(t *testing.T, th test.Helper, dockerClient *client.Client, containerID string, timeout, pollInterval time.Duration) bool {
	t.Helper()

	var stopped = assert.Eventually(t, func() bool {
		inspect, err := inspectContainer(th.Context, dockerClient, containerID)
		if err != nil {
			return true
		}
		return !inspect.State.Running
	}, timeout, pollInterval)

	return stopped
}

// TestStepLevelContainer tests the new step-level container syntax
// which allows specifying a container field directly on a step instead of
// using the executor syntax.
func TestStepLevelContainer(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()
	requireLinuxContainerRuntime(t)

	tests := []containerTest{
		{
			name: "BasicStepContainer",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: run-in-container
    container:
      image: %s
    run: echo "hello from step container"
    output: STEP_CONTAINER_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_CONTAINER_OUT": "hello from step container",
			},
		},
		{
			name: "StepContainerWithWorkingDir",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: check-workdir
    container:
      image: %s
      working_dir: /tmp
    run: pwd
    output: STEP_WORKDIR_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_WORKDIR_OUT": "/tmp",
			},
		},
		{
			name: "StepContainerWithEnv",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: check-env
    container:
      image: %s
      env:
        - MY_VAR=hello_world
    run: sh -c "echo $MY_VAR"
    output: STEP_ENV_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_ENV_OUT": "hello_world",
			},
		},
		{
			name: "StepContainerWithVolume",
			dagConfigFunc: func(tempDir string) string {
				return fmt.Sprintf(`
type: graph
steps:
  - name: write-file
    container:
      image: %s
      volumes:
        - %s:/data
    run: sh -c "echo 'step volume test' > /data/step_test.txt"
  - name: read-file
    container:
      image: %s
      volumes:
        - %s:/data
    run: cat /data/step_test.txt
    output: STEP_VOL_OUT
    depends:
      - write-file
`, testImage, tempDir, testImage, tempDir)
			},
			expectedOutputs: map[string]any{
				"STEP_VOL_OUT": "step volume test",
			},
		},
		{
			name: "MultipleStepsWithDifferentContainers",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: alpine-step
    container:
      image: %s
    run: cat /etc/alpine-release
    output: ALPINE_VERSION
  - name: busybox-step
    container:
      image: busybox:latest
    run: echo "busybox step"
    output: BUSYBOX_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"BUSYBOX_OUT": "busybox step",
			},
		},
		{
			name: "StepContainerOverridesDAGContainer",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
# DAG-level container - steps without container field use this
container:
  image: busybox:latest

steps:
  - name: use-dag-container
    run: echo "in DAG container"
    output: DAG_CONTAINER_OUT
  - name: use-step-container
    container:
      image: %s
    run: cat /etc/alpine-release
    output: STEP_CONTAINER_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"DAG_CONTAINER_OUT": "in DAG container",
			},
		},
		{
			name: "StepContainerWithUser",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: check-user
    container:
      image: %s
      user: "nobody"
    run: whoami
    output: STEP_USER_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_USER_OUT": "nobody",
			},
		},
		{
			name: "StepContainerWithPullPolicy",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: pull-never
    container:
      image: %s
      pull_policy: never
    run: echo "pull never ok"
    output: PULL_NEVER_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"PULL_NEVER_OUT": "pull never ok",
			},
		},
		{
			name: "StepEnvMergedIntoContainer",
			dagConfigFunc: func(_ string) string {
				// Test that step.env is merged with container.env
				// container.env takes precedence for shared keys
				// Use printenv to show actual environment in container
				// Note: SEMIC_ prefix is an abbreviation of the test name (StepEnvMergedIntoContainerEnv)
				// to avoid environment variable collisions between tests
				return fmt.Sprintf(`
steps:
  - name: check-merged-env
    env:
      - SEMIC_STEP_VAR=from_step
      - SEMIC_SHARED_VAR=step_value
    container:
      image: %s
      env:
        - SEMIC_CONTAINER_VAR=from_container
        - SEMIC_SHARED_VAR=container_value
    run: printenv SEMIC_SHARED_VAR
    output: SEMIC_MERGED_ENV_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				// SEMIC_SHARED_VAR should be container_value (container.env takes precedence)
				"SEMIC_MERGED_ENV_OUT": "container_value",
			},
		},
		{
			name: "StepEnvOnlyPassedToContainer",
			dagConfigFunc: func(_ string) string {
				// Test that step.env is passed to container even without container.env
				return fmt.Sprintf(`
steps:
  - name: step-env-only
    env:
      - MY_STEP_VAR=hello_from_step
    container:
      image: %s
    run: printenv MY_STEP_VAR
    output: STEP_ENV_ONLY_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_ENV_ONLY_OUT": "hello_from_step",
			},
		},
		{
			// Regression test for: https://github.com/dagucloud/dagu/issues/1565
			// When container.command is specified without a top-level command,
			// the container.command should be executed and output should be captured.
			name: "ContainerCommandWithOutput",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: step1
    container:
      image: %s
      command:
        - echo
        - '{"name": "Alice", "age": 30}'
    output: CONTAINER_CMD_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"CONTAINER_CMD_OUT": `{"name": "Alice", "age": 30}`,
			},
		},
		{
			// Regression test for: https://github.com/dagucloud/dagu/issues/1709
			// Step-level container with shell field should execute commands
			// through the shell, just like DAG-level container does.
			// Uses arithmetic expansion which requires shell interpretation.
			name: "StepContainerShellBasic",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: shell-basic
    container:
      image: %s
      shell: ["/bin/sh", "-c"]
    run: echo $((1 + 2))
    output: STEP_SHELL_BASIC_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_SHELL_BASIC_OUT": "3",
			},
		},
		{
			// Regression test for: https://github.com/dagucloud/dagu/issues/1709
			// Step-level container with shell field should support pipe operators.
			// Without shell wrapping, the pipe character is passed as a literal
			// argument to echo instead of being interpreted as a pipe.
			name: "StepContainerShellWithPipe",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: shell-pipe
    container:
      image: %s
      shell: ["/bin/sh", "-c"]
    run: echo hello | tr a-z A-Z
    output: STEP_SHELL_PIPE_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_SHELL_PIPE_OUT": "HELLO",
			},
		},
		{
			// Regression test for: https://github.com/dagucloud/dagu/issues/1709
			// Step-level container with shell field should support && operators.
			// Without shell wrapping, && is passed as a literal argument.
			name: "StepContainerShellWithOperators",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: shell-operators
    container:
      image: %s
      shell: ["/bin/sh", "-c"]
    run: echo line1 && echo line2
    output: STEP_SHELL_OPERATOR_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_SHELL_OPERATOR_OUT": "line1\nline2",
			},
		},
		{
			// Regression test for: https://github.com/dagucloud/dagu/issues/1709
			// Step-level container with shell field should support environment
			// variable expansion via the shell.
			name: "StepContainerShellVariableExpansion",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: shell-env-var
    container:
      image: %s
      shell: ["/bin/sh", "-c"]
      env:
        - TEST_SHELL_VAR=expanded_value
    run: echo $TEST_SHELL_VAR
    output: STEP_SHELL_ENVVAR_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"STEP_SHELL_ENVVAR_OUT": "expanded_value",
			},
		},
		{
			// Regression test: DAG-level env vars must be resolvable in container.env
			name: "DAGEnvInContainerEnv",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
env:
  - CDEIC_DAG_VAR: dag_env_value
steps:
  - name: check-dag-env
    container:
      image: %s
      env:
        - CDEIC_RESULT: ${CDEIC_DAG_VAR}
    run: printenv CDEIC_RESULT
    output: CDEIC_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"CDEIC_OUT": "dag_env_value",
			},
		},
		{
			// Regression test: DAG-level params must be resolvable in container.env
			name: "DAGParamInContainerEnv",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
params:
  - CDPIC_PARAM: param_value
steps:
  - name: check-dag-param
    container:
      image: %s
      env:
        - CDPIC_RESULT: ${CDPIC_PARAM}
    run: printenv CDPIC_RESULT
    output: CDPIC_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"CDPIC_OUT": "param_value",
			},
		},
		{
			// Regression test: step env referencing DAG env must resolve when merged into container
			name: "StepEnvWithDAGRefMergedIntoContainer",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
env:
  - SEWDR_BASE: base_value
steps:
  - name: check-merged
    env:
      - SEWDR_STEP: ${SEWDR_BASE}
    container:
      image: %s
    run: printenv SEWDR_STEP
    output: SEWDR_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"SEWDR_OUT": "base_value",
			},
		},
		{
			// Regression test: container env entries can reference earlier entries
			// in the same block (sequential evaluation).
			name: "ContainerEnvToEnvReference",
			dagConfigFunc: func(_ string) string {
				return fmt.Sprintf(`
steps:
  - name: check-env-ref
    container:
      image: %s
      env:
        - CETR_FIRST: first_val
        - CETR_SECOND: ${CETR_FIRST}_extended
    run: printenv CETR_SECOND
    output: CETR_OUT
`, testImage)
			},
			expectedOutputs: map[string]any{
				"CETR_OUT": "first_val_extended",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", fmt.Sprintf("%s-step-%s-*", containerPrefix, tt.name))
			require.NoError(t, err, "failed to create temporary directory")
			t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

			if tt.setupFunc != nil {
				tt.setupFunc(t, tempDir)
			}

			th := test.Setup(t)
			dag := th.DAG(t, tt.dagConfigFunc(tempDir))
			dag.Agent().RunSuccess(t)
			dag.AssertLatestStatus(t, core.Succeeded)
			dag.AssertOutputs(t, tt.expectedOutputs)
		})
	}
}

// TestContainerExecMode tests the new exec mode syntax for executing
// commands in existing running containers.
func TestContainerExecMode(t *testing.T) {
	requireLinuxContainerRuntime(t)

	t.Parallel()

	th := test.Setup(t)
	dockerClient := requireDockerClient(t)

	// Create a long-running container for exec tests
	containerName := fmt.Sprintf("dagu-exec-mode-%d", time.Now().UnixNano())
	containerID := createLongRunningContainer(t, th, dockerClient, containerName)
	defer removeContainer(t, th, dockerClient, containerID)

	tests := []struct {
		name            string
		dagConfig       string
		expectedOutputs map[string]any
	}{
		{
			name: "StringForm_DAGLevel",
			dagConfig: fmt.Sprintf(`
container: %s
steps:
  - run: echo "hello from string form"
    output: STRING_FORM_OUT
`, containerName),
			expectedOutputs: map[string]any{
				"STRING_FORM_OUT": "hello from string form",
			},
		},
		{
			name: "StringForm_StepLevel",
			dagConfig: fmt.Sprintf(`
steps:
  - name: exec-string
    container: %s
    run: echo "step string form"
    output: STEP_STRING_OUT
`, containerName),
			expectedOutputs: map[string]any{
				"STEP_STRING_OUT": "step string form",
			},
		},
		{
			name: "ObjectExecForm_DAGLevel",
			dagConfig: fmt.Sprintf(`
container:
  exec: %s
steps:
  - run: echo "hello from exec form"
    output: EXEC_FORM_OUT
`, containerName),
			expectedOutputs: map[string]any{
				"EXEC_FORM_OUT": "hello from exec form",
			},
		},
		{
			name: "ObjectExecForm_WithUser",
			dagConfig: fmt.Sprintf(`
container:
  exec: %s
  user: root
steps:
  - run: whoami
    output: EXEC_USER_OUT
`, containerName),
			expectedOutputs: map[string]any{
				"EXEC_USER_OUT": "root",
			},
		},
		{
			name: "ObjectExecForm_WithWorkingDir",
			dagConfig: fmt.Sprintf(`
container:
  exec: %s
  working_dir: /tmp
steps:
  - run: pwd
    output: EXEC_WORKDIR_OUT
`, containerName),
			expectedOutputs: map[string]any{
				"EXEC_WORKDIR_OUT": "/tmp",
			},
		},
		{
			name: "ObjectExecForm_WithEnv",
			dagConfig: fmt.Sprintf(`
container:
  exec: %s
  env:
    - EXEC_TEST_VAR=exec_env_value
steps:
  - run: printenv EXEC_TEST_VAR
    output: EXEC_ENV_OUT
`, containerName),
			expectedOutputs: map[string]any{
				"EXEC_ENV_OUT": "exec_env_value",
			},
		},
		{
			name: "ObjectExecForm_StepLevel",
			dagConfig: fmt.Sprintf(`
steps:
  - name: step-exec
    container:
      exec: %s
      user: root
    run: whoami
    output: STEP_EXEC_OUT
`, containerName),
			expectedOutputs: map[string]any{
				"STEP_EXEC_OUT": "root",
			},
		},
		{
			name: "MultipleCommands_ExecMode",
			dagConfig: fmt.Sprintf(`
container:
  exec: %s
steps:
  - run: echo "first command"
    output: MULTI_OUT_1
  - run: echo "second command"
    output: MULTI_OUT_2
`, containerName),
			expectedOutputs: map[string]any{
				"MULTI_OUT_1": "first command",
				"MULTI_OUT_2": "second command",
			},
		},
		{
			name: "ExecMode_WithAllOverrides",
			dagConfig: fmt.Sprintf(`
container:
  exec: %s
  user: root
  working_dir: /tmp
  env:
    - CUSTOM_VAR=test123
steps:
  - run: sh -c "echo user=$(whoami) dir=$(pwd) var=$CUSTOM_VAR"
    output: ALL_OVERRIDES_OUT
`, containerName),
			expectedOutputs: map[string]any{
				"ALL_OVERRIDES_OUT": "user=root dir=/tmp var=test123",
			},
		},
		{
			// Test for: https://github.com/dagucloud/dagu/issues/1589
			// Shell field should work in exec mode
			name: "ShellField_ExecMode",
			dagConfig: fmt.Sprintf(`
container:
  exec: %s
  shell: ["/bin/sh", "-c"]
steps:
  - run: echo "shell in exec mode"
    output: EXEC_SHELL_OUT1
  - run: echo "second command"
    output: EXEC_SHELL_OUT2
`, containerName),
			expectedOutputs: map[string]any{
				"EXEC_SHELL_OUT1": "shell in exec mode",
				"EXEC_SHELL_OUT2": "second command",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th := test.Setup(t)
			dag := th.DAG(t, tt.dagConfig)
			dag.Agent().RunSuccess(t)
			dag.AssertLatestStatus(t, core.Succeeded)
			dag.AssertOutputs(t, tt.expectedOutputs)
		})
	}
}

// TestContainerExecNotFound tests that exec mode fails when the container doesn't exist.
func TestContainerExecNotFound(t *testing.T) {
	requireDockerDaemon(t)
	requireLinuxContainerRuntime(t)
	t.Parallel()

	th := test.Setup(t)

	// Use a container name that definitely doesn't exist
	nonExistentContainer := fmt.Sprintf("dagu-nonexistent-%d", time.Now().UnixNano())

	dagConfig := fmt.Sprintf(`
container: %s
steps:
  - run: echo "should not run"
`, nonExistentContainer)

	dag := th.DAG(t, dagConfig)
	dag.Agent().RunCheckErr(t, "timed out waiting for container to be running")
}

// TestContainerExecNotRunning tests that exec mode fails when the container exists but is not running.
func TestContainerExecNotRunning(t *testing.T) {
	requireLinuxContainerRuntime(t)

	t.Parallel()

	th := test.Setup(t)
	dockerClient := requireDockerClient(t)

	// Create a container but don't start it
	containerName := fmt.Sprintf("dagu-exec-stopped-%d", time.Now().UnixNano())

	platform, err := currentDockerPlatform(th.Context, dockerClient)
	require.NoError(t, err)
	pullOpts := client.ImagePullOptions{Platforms: []specs.Platform{platform}}

	// Pull the image
	reader, err := dockerClient.ImagePull(th.Context, testImage, pullOpts)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, reader)
	_ = reader.Close()

	// Create but do NOT start the container
	created, err := dockerClient.ContainerCreate(th.Context, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: testImage,
			Cmd:   []string{"sh", "-c", "while true; do sleep 3600; done"},
		},
		Name: containerName,
	})
	require.NoError(t, err)

	// Clean up the container after the test
	defer func() {
		_, _ = dockerClient.ContainerRemove(th.Context, created.ID, client.ContainerRemoveOptions{Force: true})
	}()

	dagConfig := fmt.Sprintf(`
container: %s
steps:
  - run: echo "should not run"
`, containerName)

	dag := th.DAG(t, dagConfig)
	dag.Agent().RunCheckErr(t, "timed out waiting for container to be running")
}

// TestContainerCustomHealthcheck verifies that custom healthcheck configuration
// is passed to Docker and waitFor: healthy waits for the container to become
// healthy (not just running). Uses a file creation delay to prove actual waiting.
func TestContainerCustomHealthcheck(t *testing.T) {
	requireDockerDaemon(t)
	requireLinuxContainerRuntime(t)
	t.Parallel()

	th := test.Setup(t)

	dagConfig := fmt.Sprintf(`
container:
  image: %s
  startup: command
  command: ["sh", "-c", "sleep 2 && touch /tmp/ready && while true; do sleep 3600; done"]
  healthcheck:
    test: ["CMD", "test", "-f", "/tmp/ready"]
    interval: 1s
    timeout: 5s
    start_period: 5s
    retries: 10
  wait_for: healthy
steps:
  - run: echo "container is healthy"
    output: HEALTHCHECK_OUT
`, testImage)

	dag := th.DAG(t, dagConfig)
	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"HEALTHCHECK_OUT": "container is healthy",
	})
}

// TestContainerCustomHealthcheck_StepLevel tests custom healthcheck at step level.
func TestContainerCustomHealthcheck_StepLevel(t *testing.T) {
	requireDockerDaemon(t)
	requireLinuxContainerRuntime(t)
	t.Parallel()

	th := test.Setup(t)

	dagConfig := fmt.Sprintf(`
steps:
  - name: step-with-healthcheck
    container:
      image: %s
      startup: command
      command: ["sh", "-c", "sleep 1 && touch /tmp/step_ready && while true; do sleep 3600; done"]
      healthcheck:
        test: ["CMD-SHELL", "test -f /tmp/step_ready"]
        interval: 500ms
        timeout: 3s
        start_period: 3s
        retries: 10
      wait_for: healthy
    run: echo "step container healthy"
    output: STEP_HEALTHCHECK_OUT
`, testImage)

	dag := th.DAG(t, dagConfig)
	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"STEP_HEALTHCHECK_OUT": "step container healthy",
	})
}

// TestContainerExecVariableExpansion tests that environment variables are expanded in container names.
func TestContainerExecVariableExpansion(t *testing.T) {
	requireLinuxContainerRuntime(t)

	t.Parallel()

	th := test.Setup(t)
	dockerClient := requireDockerClient(t)

	// Create a long-running container for exec tests
	containerName := fmt.Sprintf("dagu-exec-var-%d", time.Now().UnixNano())
	containerID := createLongRunningContainer(t, th, dockerClient, containerName)
	defer removeContainer(t, th, dockerClient, containerID)

	// Test string form with variable expansion
	t.Run("StringFormWithVariable", func(t *testing.T) {
		th := test.Setup(t)
		dagConfig := fmt.Sprintf(`
env:
  - EXEC_CONTAINER_NAME: %s
container: ${EXEC_CONTAINER_NAME}
steps:
  - run: echo "variable expansion works"
    output: VAR_OUT
`, containerName)

		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"VAR_OUT": "variable expansion works",
		})
	})

	// Test object exec form with variable expansion
	t.Run("ObjectExecFormWithVariable", func(t *testing.T) {
		th := test.Setup(t)
		dagConfig := fmt.Sprintf(`
env:
  - EXEC_CONTAINER_NAME: %s
container:
  exec: ${EXEC_CONTAINER_NAME}
steps:
  - run: echo "object form variable expansion"
    output: OBJ_VAR_OUT
`, containerName)

		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"OBJ_VAR_OUT": "object form variable expansion",
		})
	})

	// Test step-level container with variable expansion
	t.Run("StepLevelWithVariable", func(t *testing.T) {
		th := test.Setup(t)
		dagConfig := fmt.Sprintf(`
env:
  - STEP_CONTAINER: %s
steps:
  - name: step-var
    container: ${STEP_CONTAINER}
    run: echo "step level variable"
    output: STEP_VAR_OUT
`, containerName)

		dag := th.DAG(t, dagConfig)
		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"STEP_VAR_OUT": "step level variable",
		})
	})
}
