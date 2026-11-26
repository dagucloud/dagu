// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// projectsInstall is the install of Spec 102 "Examples": the default
// project, intraday, and day-ahead, with a library. Each DAG writes the
// DAGU_PROJECT its step gets to a file named after the DAG.
type projectsInstall struct {
	dagu *harness.Runner
	home string
	out  string
	env  []string
}

func newProjectsInstall(t *testing.T) projectsInstall {
	t.Helper()
	dagu := harness.NewRunner(t)
	home := filepath.Join(t.TempDir(), "dagu")
	in := projectsInstall{
		dagu: dagu,
		home: home,
		out:  t.TempDir(),
		env: []string{
			"DAGU_HOME=" + home,
			"DAGU_EXPERIMENTAL_PROJECTS=true",
			"DAGU_ALT_DAGS_DIR=" + filepath.Join(home, "library"),
		},
	}
	for _, name := range []string{"intraday", "day-ahead"} {
		in.run(t, "project", "create", name).ExpectExitCode(0)
	}
	in.writeDAG(t, "dags/cleanup.yaml")
	in.writeDAG(t, "projects/intraday/forecast.yaml")
	in.writeDAG(t, "projects/day-ahead/auction.yaml")
	return in
}

func (in projectsInstall) writeDAG(t *testing.T, rel string) {
	t.Helper()
	name := strings.TrimSuffix(filepath.Base(rel), ".yaml")
	out := filepath.ToSlash(filepath.Join(in.out, name))
	path := filepath.Join(in.home, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, fmt.Appendf(nil,
		"steps:\n  - id: record\n    run: printf '%%s' \"$DAGU_PROJECT\" > '%s'\n", out), 0o600))
}

func (in projectsInstall) run(t *testing.T, args ...string) *harness.Result {
	t.Helper()
	return in.dagu.RunWithEnv(in.env, args...)
}

// projectOf returns the DAGU_PROJECT that the last run of the DAG name got,
// or "" when it did not run.
func (in projectsInstall) projectOf(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(in.out, name))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(in.out, name)))
	return string(data)
}

// TestCLIChoosesProject proves Spec 102 "Choosing a Project in the CLI" and
// "Steps": a command addresses --project, else DAGU_PROJECT, else the
// project of the current directory, else the default project, and each
// step of the run gets DAGU_PROJECT set to its run's project.
func TestCLIChoosesProject(t *testing.T) {
	t.Parallel()
	in := newProjectsInstall(t)
	projects := filepath.Join(in.home, "projects")

	tests := []struct {
		dir     string
		env     []string
		args    []string
		dag     string
		project string
	}{
		{dir: filepath.Join(projects, "intraday"), args: []string{"start", "forecast"}, dag: "forecast", project: "intraday"},
		{dir: filepath.Join(projects, "intraday"), args: []string{"start", "--project", "day-ahead", "auction"}, dag: "auction", project: "day-ahead"},
		{env: []string{"DAGU_PROJECT=Intraday"}, args: []string{"start", "forecast"}, dag: "forecast", project: "intraday"},
		{args: []string{"start", "cleanup"}, dag: "cleanup", project: "default"},
		{dir: projects, args: []string{"start", "cleanup"}, dag: "cleanup", project: "default"},
	}
	for _, tt := range tests {
		dagu := in.dagu
		if tt.dir != "" {
			dagu = dagu.InDir(tt.dir)
		}
		dagu.RunWithEnv(append(slices.Clone(in.env), tt.env...), tt.args...).ExpectExitCode(0)
		require.Equal(t, tt.project, in.projectOf(t, tt.dag), "%v in %s", tt.args, tt.dir)
	}

	forecast := filepath.Join(projects, "intraday", "forecast.yaml")
	result := in.run(t, "start", "--project", "day-ahead", forecast)
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("belongs to project intraday")
	require.Empty(t, in.projectOf(t, "forecast"))

	result = in.run(t, "start", "--project", "nowhere", "cleanup")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`project "nowhere" does not exist`)
	require.Empty(t, in.projectOf(t, "cleanup"), "starts no run")
}

// TestCLIProjectCommands proves Spec 102 "CLI": dagu project create and
// dagu project list, and that create refuses a name that is taken.
func TestCLIProjectCommands(t *testing.T) {
	t.Parallel()
	in := newProjectsInstall(t)

	result := in.run(t, "project", "list")
	result.ExpectExitCode(0)
	lines := strings.Split(strings.TrimSpace(result.Stdout()), "\n")
	require.Len(t, lines, 4, result.Stdout())
	for i, name := range []string{"default", "day-ahead", "intraday"} {
		require.Equal(t, name, strings.Fields(lines[i+1])[0])
	}

	result = in.run(t, "project", "create", "Intraday")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("already exists")
}

// TestCLITestSelectsProject proves Spec 102 criterion 47: dagu test with no
// argument inside a project's DAG directory runs that project's test files
// and no other project's.
func TestCLITestSelectsProject(t *testing.T) {
	t.Parallel()
	in := newProjectsInstall(t)
	testFile := "cases:\n  - name: records\n    mocks:\n      record: {}\n    expect:\n      status: succeeded\n"
	for _, rel := range []string{"dags/cleanup.test.yaml", "projects/intraday/forecast.test.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(in.home, filepath.FromSlash(rel)), []byte(testFile), 0o600))
	}

	result := in.dagu.InDir(filepath.Join(in.home, "projects", "intraday")).RunWithEnv(in.env, "test")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "forecast.test.yaml")
	require.NotContains(t, result.Stdout(), "cleanup.test.yaml")
}

// TestCLIProjectsOff proves that with projects off the CLI is as before:
// --project is an unknown flag and DAGU_PROJECT changes nothing.
func TestCLIProjectsOff(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"), "DAGU_PROJECT=nowhere"}

	result := dagu.RunWithEnv(env, "validate", "--project", "weather", "noop.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("unknown flag: --project")
	dagu.RunWithEnv(env, "validate", "noop.yaml").ExpectExitCode(0)

	result = dagu.RunWithEnv(env, "project", "list")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`unknown command "project" for "dagu"`)
}
