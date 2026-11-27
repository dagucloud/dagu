// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// stateDAG returns a DAG that writes the results of a state.get of key in
// each of scopes to out, as a JSON object keyed by scope.
func stateDAG(out, key string, scopes ...string) string {
	var b strings.Builder
	b.WriteString("steps:\n")
	var ids, fields []string
	for _, scope := range scopes {
		output := "GOT_" + strings.ToUpper(scope)
		fmt.Fprintf(&b, "  - id: get_%s\n    action: state.get\n    with:\n      scope: %s\n      key: %s\n    output: %s\n",
			scope, scope, key, output)
		ids = append(ids, "get_"+scope)
		fields = append(fields, fmt.Sprintf(`"%s": ${%s}`, scope, output))
	}
	fmt.Fprintf(&b, "  - id: save\n    depends: [%s]\n    action: file.write\n    with:\n      path: %q\n      content: '{%s}'\n",
		strings.Join(ids, ", "), filepath.ToSlash(out), strings.Join(fields, ", "))
	return b.String()
}

// setStateDAG returns a DAG that sets key to value in each of scopes and
// keeps an artifact.
func setStateDAG(key, value string, scopes ...string) string {
	var b strings.Builder
	b.WriteString("artifacts:\n  enabled: true\nsteps:\n")
	for _, scope := range scopes {
		fmt.Fprintf(&b, "  - id: set_%s\n    action: state.set\n    with:\n      scope: %s\n      key: %s\n      value: %s\n",
			scope, scope, key, value)
	}
	b.WriteString("  - id: keep\n    action: artifact.write\n    with:\n      path: kept.txt\n      content: kept\n")
	return b.String()
}

// stateFound reads what stateDAG wrote to out and returns the value found
// in each scope that has the key.
func stateFound(t *testing.T, out string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	var results map[string]struct {
		Found bool   `json:"found"`
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(data, &results), string(data))
	found := map[string]string{}
	for scope, result := range results {
		if result.Found {
			found[scope] = result.Value
		}
	}
	return found
}

// requireFileUnder fails unless a file named name lies below dir.
func requireFileUnder(t *testing.T, dir, name string) {
	t.Helper()
	var found bool
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == name {
			found = true
		}
		return nil
	})
	require.True(t, found, "no %s under %s", name, dir)
}

// TestLibraryRunsApart proves Spec 102 "Runs of Library DAGs" for an
// explicit start: the run of the library DAG etl in weather, its log, its
// artifacts, and its DAG state with scope dag and root_dag stay in the
// library directories of weather. A project DAG etl added later starts with
// no runs and no such state, shares global state, and the library run stays
// reachable by its file.
func TestLibraryRunsApart(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	out := filepath.Join(t.TempDir(), "state.json")
	library := in.write(t, "library/etl.yaml", setStateDAG("owner", "library", "dag", "root_dag", "global"))
	in.run(true, nil, "start", "--project", "weather", "--run-id", "lib-1", library).ExpectExitCode(0)

	in.write(t, "projects/weather/etl.yaml", stateDAG(out, "owner", "dag", "root_dag", "global"))
	in.run(true, nil, "start", "--project", "weather", "--run-id", "own-1", "etl").ExpectExitCode(0)
	require.Equal(t, map[string]string{"global": "library"}, stateFound(t, out))

	in.run(true, nil, "status", "--project", "weather", "--run-id", "lib-1", library).ExpectExitCode(0)
	in.run(true, nil, "status", "--project", "weather", "--run-id", "lib-1", "etl").ExpectNonZeroExitCode()
	in.run(true, nil, "status", "--project", "weather", "--run-id", "own-1", library).ExpectNonZeroExitCode()
	in.run(true, nil, "retry", "--project", "weather", "--run-id", "lib-1", library).ExpectExitCode(0)

	weather := filepath.Join(in.home, "data", "projects", "weather")
	requireFileUnder(t, filepath.Join(weather, "library", "runs"), "status.jsonl")
	require.DirExists(t, filepath.Join(weather, "library", "logs", "etl"))
	requireFileUnder(t, filepath.Join(weather, "library", "artifacts"), "kept.txt")
	require.NoDirExists(t, filepath.Join(weather, "artifacts"))
}

// TestLibraryChildStateApart proves Spec 102 "Runs of Library DAGs" for a
// dag.run child: the library DAG ABC that a project DAG calls keeps its DAG
// state with scope dag in the library directories, and shares root_dag and
// global state with its caller.
func TestLibraryChildStateApart(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	callerOut := filepath.Join(t.TempDir(), "caller.json")
	ownOut := filepath.Join(t.TempDir(), "own.json")
	in.write(t, "library/ABC.yaml", setStateDAG("owner", "child", "dag", "root_dag", "global"))
	reads := strings.TrimPrefix(stateDAG(callerOut, "owner", "root_dag", "global"), "steps:\n")
	reads = strings.ReplaceAll(reads, "    action: state.get\n", "    depends: call\n    action: state.get\n")
	in.write(t, "projects/weather/caller.yaml", callerOf("ABC")+reads)
	in.run(true, nil, "start", "--project", "weather", "caller").ExpectExitCode(0)
	require.Equal(t, map[string]string{"root_dag": "child", "global": "child"}, stateFound(t, callerOut))

	in.write(t, "projects/weather/ABC.yaml", stateDAG(ownOut, "owner", "dag", "global"))
	in.run(true, nil, "start", "--project", "weather", "ABC").ExpectExitCode(0)
	require.Equal(t, map[string]string{"global": "child"}, stateFound(t, ownOut))
}

// TestLibraryRunsProjectsOff proves that with projects off a library DAG's
// DAG state is the default project's, as before, and no library directory
// is made. With projects on, the default project keeps it in its library
// directory.
func TestLibraryRunsProjectsOff(t *testing.T) {
	t.Parallel()

	for _, projectsOn := range []bool{false, true} {
		in := newLibraryInstall(t)
		out := filepath.Join(t.TempDir(), "state.json")
		library := in.write(t, "library/etl.yaml", setStateDAG("owner", "library", "dag"))
		in.run(projectsOn, nil, "start", library).ExpectExitCode(0)
		in.write(t, "dags/etl.yaml", stateDAG(out, "owner", "dag"))
		in.run(projectsOn, nil, "start", "etl").ExpectExitCode(0)

		libraryDir := filepath.Join(in.home, "data", "projects", "default", "library")
		if projectsOn {
			require.Empty(t, stateFound(t, out))
			requireFileUnder(t, filepath.Join(libraryDir, "runs"), "status.jsonl")
			continue
		}
		require.Equal(t, map[string]string{"dag": "library"}, stateFound(t, out))
		require.NoDirExists(t, filepath.Join(in.home, "data", "projects"))
	}
}

// TestLibraryQueueRefused proves that, with projects on, a library DAG's run
// is not queued, by `dagu enqueue` or by dag.enqueue, since no scheduler
// reads a project's library queues. With projects off, it is queued as
// before.
func TestLibraryQueueRefused(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	library := in.write(t, "library/etl.yaml", "steps:\n  - id: noop\n    run: \"true\"\n")
	caller := in.write(t, "dags/caller.yaml", "steps:\n  - id: queue\n    action: dag.enqueue\n    with:\n      dag: etl\n")
	const refusal = "library DAGs cannot be queued; no scheduler reads a project's library queues; run it with dagu start instead"

	result := in.run(true, nil, "enqueue", library)
	result.ExpectNonZeroExitCode()
	requireOutput(t, result, refusal)
	in.run(true, nil, "start", caller).ExpectNonZeroExitCode()

	in.run(false, nil, "enqueue", library).ExpectExitCode(0)
	in.run(false, nil, "start", caller).ExpectExitCode(0)
}

// TestLibraryEnqueueOfProjectDAG proves that a project DAG that a library
// run queues with dag.enqueue is the project's: its queued run points its
// log into the project's logs, not the library's.
func TestLibraryEnqueueOfProjectDAG(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	library := in.write(t, "library/etl.yaml", "steps:\n  - id: queue\n    action: dag.enqueue\n    with:\n      dag: forecast\n")
	in.write(t, "dags/forecast.yaml", "steps:\n  - id: noop\n    run: \"true\"\n")
	in.run(true, nil, "start", library).ExpectExitCode(0)

	result := in.run(true, nil, "status", "forecast")
	result.ExpectExitCode(0)
	require.NotContains(t, filepath.ToSlash(result.Stdout()), "/library/logs/")
	require.NoDirExists(t, filepath.Join(in.home, "data", "projects", "default", "library", "logs", "forecast"))
}

// TestLibraryLocalDAGState proves Spec 102 "Runs of Library DAGs" for a DAG
// defined in a library DAG's file: it is a library DAG too, so its DAG
// state with scope dag stays in the library directories.
func TestLibraryLocalDAGState(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	out := filepath.Join(t.TempDir(), "state.json")
	library := in.write(t, "library/etl.yaml", callerOf("sub")+"---\nname: sub\n"+setStateDAG("owner", "library-local", "dag"))
	in.run(true, nil, "start", library).ExpectExitCode(0)

	in.write(t, "dags/sub.yaml", stateDAG(out, "owner", "dag"))
	in.run(true, nil, "start", "sub").ExpectExitCode(0)
	require.Empty(t, stateFound(t, out))
}
