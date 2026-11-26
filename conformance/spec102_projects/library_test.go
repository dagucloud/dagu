// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// libraryInstall is a Dagu home with a library and a registered project
// weather besides the default project.
type libraryInstall struct {
	dagu    *harness.Runner
	home    string
	library string
	env     []string
}

func newLibraryInstall(t *testing.T) *libraryInstall {
	t.Helper()
	home := filepath.Join(t.TempDir(), "dagu")
	in := &libraryInstall{
		dagu:    harness.NewRunner(t),
		home:    home,
		library: filepath.Join(home, "library"),
	}
	in.env = []string{"DAGU_HOME=" + home, "DAGU_ALT_DAGS_DIR=" + in.library}
	// The registry is written as `dagu project create` would write it.
	in.write(t, "data/projects.json", `{"schema_version":1,"projects":[`+
		`{"id":"5b0c43a2-5f0e-4f55-9d0a-4a8f3c1c2e11","name":"weather","display_name":"weather",`+
		`"created_at":"2026-01-01T00:00:00Z","slot_seed":"5b0c43a2-5f0e-4f55-9d0a-4a8f3c1c2e11"}]}`)
	return in
}

// write writes a file at rel below the Dagu home and returns its path.
func (in *libraryInstall) write(t *testing.T, rel, content string) string {
	t.Helper()
	path := filepath.Join(in.home, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// run runs dagu with projects on or off.
func (in *libraryInstall) run(projectsOn bool, extraEnv []string, args ...string) *harness.Result {
	env := append(slices.Clone(in.env), extraEnv...)
	if projectsOn {
		env = append(env, "DAGU_EXPERIMENTAL_PROJECTS=true")
	}
	return in.dagu.RunWithEnv(env, args...)
}

// requireOutput fails the test unless the command printed want.
func requireOutput(t *testing.T, result *harness.Result, want string) {
	t.Helper()
	require.Contains(t, result.Stdout()+result.Stderr(), want)
}

// callerOf returns a DAG whose one step runs target with dag.run.
func callerOf(target string) string {
	return fmt.Sprintf("steps:\n  - id: call\n    action: dag.run\n    with:\n      dag: %q\n", target)
}

// TestLibraryDAGRunsInCaller proves Spec 102 "Resolution" and "Base Config":
// dag.run finds a library DAG the caller's project lacks, and the child runs
// with the caller's project's base config.
func TestLibraryDAGRunsInCaller(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	out := filepath.Join(t.TempDir(), "child.txt")
	in.write(t, "base.yaml", "env:\n  - PROJECT_MARK: default-base\n")
	in.write(t, "library/ABC.yaml", fmt.Sprintf(
		"steps:\n  - id: mark\n    run: echo \"$PROJECT_MARK\" > %q\n", filepath.ToSlash(out)))
	caller := in.write(t, "dags/caller.yaml", callerOf("ABC"))

	in.run(true, nil, "start", caller).ExpectExitCode(0)
	content, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "default-base\n", string(content))
}

// TestDAGRunOutsideProject proves Spec 102 "Resolution" and "Loading": a
// target in neither the caller's project nor the library fails the step
// with a load error that names the caller's project and the library, and a
// path into another project names both projects.
func TestDAGRunOutsideProject(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	forecast := in.write(t, "projects/weather/forecast.yaml", "steps:\n  - id: noop\n    run: \"true\"\n")
	byName := in.write(t, "dags/by-name.yaml", callerOf("forecast"))
	byPath := in.write(t, "dags/by-path.yaml", callerOf(filepath.ToSlash(forecast)))

	result := in.run(true, nil, "start", byName)
	result.ExpectNonZeroExitCode()
	requireOutput(t, result, `DAG forecast is in neither project "default" nor the library`)

	result = in.run(true, nil, "start", byPath)
	result.ExpectNonZeroExitCode()
	requireOutput(t, result, `belongs to project "weather"; project "default" runs only its own DAGs and library DAGs`)
}

// TestDAGRunSymlinkOutOfProject proves Spec 102 "Discovery": with projects
// on, dag.run does not follow a file symlink out of the project's DAG
// directory, though dag_discovery.symlinks is on. With projects off, it
// follows the link as before.
func TestDAGRunSymlinkOutOfProject(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	target := filepath.Join(t.TempDir(), "outside.yaml")
	require.NoError(t, os.WriteFile(target, []byte("steps:\n  - id: noop\n    run: \"true\"\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(in.home, "dags"), 0o750))
	if err := os.Symlink(target, filepath.Join(in.home, "dags", "linked.yaml")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	caller := in.write(t, "dags/caller.yaml", callerOf("linked"))
	symlinksOn := []string{"DAGU_DAG_DISCOVERY_SYMLINKS=true"}

	in.run(false, symlinksOn, "start", caller).ExpectExitCode(0)
	result := in.run(true, symlinksOn, "start", caller)
	result.ExpectNonZeroExitCode()
	requireOutput(t, result, "a project never follows a symbolic link out of its DAG directory")
}

// TestShadowedLibraryDAGStartRefused proves Spec 102 "Runs of Library DAGs":
// an explicit start of a library DAG is refused in a project that has a DAG
// of the same name, and starts in one that has none.
func TestShadowedLibraryDAGStartRefused(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	shared := in.write(t, "library/ABC.yaml", "steps:\n  - id: noop\n    run: \"true\"\n")
	cleanup := in.write(t, "library/cleanup.yaml", "steps:\n  - id: noop\n    run: \"true\"\n")
	in.write(t, "dags/ABC.yaml", "steps:\n  - id: noop\n    run: \"true\"\n")

	result := in.run(true, nil, "start", shared)
	result.ExpectNonZeroExitCode()
	requireOutput(t, result, `cannot start in project "default", which has a DAG named ABC`)
	in.run(true, nil, "start", cleanup).ExpectExitCode(0)
	in.run(false, nil, "start", shared).ExpectExitCode(0)
}

// TestLibraryScheduleWarning proves Spec 102 "Library Definitions":
// validation warns about the schedule of a library DAG.
func TestLibraryScheduleWarning(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	nightly := in.write(t, "library/nightly.yaml", "schedule: \"0 1 * * *\"\nsteps:\n  - id: noop\n    run: \"true\"\n")
	const warning = "library DAGs are not scheduled"

	result := in.run(true, nil, "validate", nightly)
	result.ExpectExitCode(0)
	requireOutput(t, result, warning)
	result = in.run(false, nil, "validate", nightly)
	result.ExpectExitCode(0)
	require.NotContains(t, result.Stdout()+result.Stderr(), warning)
}
