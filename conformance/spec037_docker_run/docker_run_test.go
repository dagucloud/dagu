// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec037_docker_run_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestDockerRunCreateBasic proves create mode: a new container is created
// from with.image, runs the command, and its output is captured as a step
// output. Values referenced in with.command are resolved on the host before
// dispatch, so the container itself needs no matching environment variable.
func TestDockerRunCreateBasic(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "create_basic.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("out.txt", "hello BAR\n")
}

// TestDockerRunCreateNamed proves that setting both with.image and
// with.container_name creates a new container under that explicit name.
func TestDockerRunCreateNamed(t *testing.T) {
	dockerClient := requireDockerDaemon(t)

	name := uniqueContainerName(t)
	env := []string{"CONTAINER_NAME=" + name}

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "create_named.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("out.txt", "named\n")

	// auto_remove: true means the named container is gone once the step
	// completes.
	if containerExists(t, dockerClient, name) {
		t.Fatalf("container %s should have been auto-removed", name)
	}
}

// TestDockerRunCreateNamedIgnoresExec proves that with.exec has no effect
// when with.image is also set: image present always selects create mode
// (per "Mode selection"), and the config validation that rejects with.exec
// only fires when with.container_name is absent, so this combination is
// accepted but with.exec's working_dir is silently never applied.
func TestDockerRunCreateNamedIgnoresExec(t *testing.T) {
	requireDockerDaemon(t)

	name := uniqueContainerName(t)
	env := []string{"CONTAINER_NAME=" + name}

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "create_named_with_exec.yaml")
	result.ExpectExitCode(0)
	// If with.exec.working_dir had been applied, this would be "/tmp".
	dagu.ExpectFileContent("out.txt", "/\n")
}

// TestDockerRunExecExisting proves exec mode: with.container_name alone (no
// with.image) runs the command inside an already-running container instead
// of creating one.
func TestDockerRunExecExisting(t *testing.T) {
	dockerClient := requireDockerDaemon(t)

	name := uniqueContainerName(t)
	env := []string{"CONTAINER_NAME=" + name}
	startLongRunningContainer(t, dockerClient, name)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "exec_existing.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("out.txt", "hello-existing\n")

	// Exec mode never removes the container it ran inside.
	if !containerExists(t, dockerClient, name) {
		t.Fatalf("container %s should still exist after exec mode", name)
	}
}

// TestDockerRunMissingTarget proves that omitting both with.image and
// with.container_name is rejected by config-schema validation at DAG-build
// time, before the DAG even starts running, when with has another field
// alongside command.
func TestDockerRunMissingTarget(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "missing_target.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("image", "container_name")
}

// TestDockerRunMissingTargetBare proves a narrower case of the same rule:
// when with contains only command, command is extracted into the step's
// own field and the schema validator above never runs (with is left empty),
// so the step instead fails at run time with a generic "configuration is
// required" error rather than the specific image/container_name message.
func TestDockerRunMissingTargetBare(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "missing_target_bare.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("docker step configuration is required")
}

// TestDockerRunExecOptionsRequireContainerName proves that with.exec is only
// valid alongside with.container_name; supplying it with only with.image is
// rejected before the daemon is contacted.
func TestDockerRunExecOptionsRequireContainerName(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "exec_without_container_name.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("container_name")
}

// TestDockerRunPullNeverMissingImage proves with.pull: never fails instead
// of silently falling back to a pull when the image is not present locally.
func TestDockerRunPullNeverMissingImage(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "pull_never_missing.yaml")
	result.ExpectNonZeroExitCode()
}

// TestDockerRunExecNonexistentContainer proves exec mode fails with a
// daemon-reported error when with.container_name names no container at
// all. The name is generated fresh for this test, so it is guaranteed not
// to name a real container.
func TestDockerRunExecNonexistentContainer(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	env := []string{"CONTAINER_NAME=" + uniqueContainerName(t)}
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "exec_nonexistent_container.yaml")
	result.ExpectNonZeroExitCode()
}

// TestDockerRunExitCodePropagates proves a non-zero command exit fails the
// step and the failure carries the command's own exit code.
func TestDockerRunExitCodePropagates(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "exit_code.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("exit status 7")
}

// TestDockerRunMultipleCommandsStopOnFailure proves with.command as a list
// runs each entry in order in the same container, and a failing entry stops
// the remaining ones from running. The first entry writes a marker outside
// the bind-mounted /work directory; the second only writes second.txt after
// reading that marker back, so an implementation that ran each entry in its
// own fresh container (with only /work actually shared, via the host bind
// mount) would fail to produce second.txt at all, rather than passing this
// test by coincidence.
func TestDockerRunMultipleCommandsStopOnFailure(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.Mkdir("work")

	result := dagu.Run("start", "multi_command.yaml")
	result.ExpectNonZeroExitCode()
	dagu.ExpectFileContent("work/first.txt", "first\n")
	dagu.ExpectFileContent("work/second.txt", "second\n")
	dagu.ExpectNoFile("work/third.txt")
}

// TestDockerRunWorkingDirAndVolumes proves the with.working_dir and
// with.volumes shortcuts: the command runs in the configured working
// directory, and a host directory is bind-mounted and readable inside the
// container.
func TestDockerRunWorkingDirAndVolumes(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.Mkdir("voldata")
	dagu.WriteFile("voldata/file.txt", "host-content\n")

	result := dagu.Run("start", "working_dir_volumes.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains("out.txt", "/data", "host-content")
}
