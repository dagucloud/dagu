// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectsHome is a Dagu home whose projects_dir and alt_dags_dir lie in its
// dags directory, with the projects weather and intraday registered.
type projectsHome struct {
	home   string
	config string
	dags   string
}

func newProjectsHome(t *testing.T, switchOn bool) projectsHome {
	t.Helper()
	home := t.TempDir()
	dags := filepath.Join(home, "dags")
	content := "paths:\n" +
		"  dags_dir: " + strconv.Quote(dags) + "\n" +
		"  projects_dir: " + strconv.Quote(filepath.Join(dags, "projects")) + "\n" +
		"  alt_dags_dir: " + strconv.Quote(filepath.Join(dags, "library")) + "\n"
	if switchOn {
		content += "experimental_projects: true\n"
	}
	configPath := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))
	registry := projectregistry.New(filepath.Join(home, "data"))
	for _, name := range []string{"weather", "intraday"} {
		_, err := registry.Create(context.Background(), project.CreateInput{Name: name})
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(dags, "projects", name), 0o750))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dags, "library"), 0o750))
	return projectsHome{home: home, config: configPath, dags: dags}
}

// command returns the command called name with the home's flags set, and
// --project when projectName is set.
func (h projectsHome) command(t *testing.T, name, projectName string) *cobra.Command {
	t.Helper()
	command := &cobra.Command{Use: name}
	initFlags(command)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", h.home))
	require.NoError(t, command.Flags().Set("config", h.config))
	if projectName != "" {
		require.NoError(t, command.Flags().Set(projectFlag.name, projectName))
	}
	return command
}

func (h projectsHome) file(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(h.dags, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("steps:\n  - run: echo ok\n"), 0o600))
	return path
}

// TestAddressedProject proves the order of Spec 102 "CLI": --project, then
// DAGU_PROJECT, then the project whose DAG directory holds the current
// directory, then the default project. It sets the environment and the
// working directory, so it does not run in parallel.
func TestAddressedProject(t *testing.T) {
	h := newProjectsHome(t, true)
	weatherDir := filepath.Join(h.dags, "projects", "weather")
	require.NoError(t, os.MkdirAll(filepath.Join(weatherDir, "etl"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(h.dags, "projects", "stray"), 0o750))

	tests := []struct {
		name    string
		flag    string
		env     string
		dir     string
		project string
		dagsDir string
	}{
		{name: "Default", dir: h.home, project: "default", dagsDir: h.dags},
		{name: "Flag", flag: "weather", dir: h.home, project: "weather", dagsDir: weatherDir},
		{name: "FlagAnyCase", flag: "WEATHER", dir: h.home, project: "weather", dagsDir: weatherDir},
		{name: "FlagDefault", flag: "Default", dir: weatherDir, project: "default", dagsDir: h.dags},
		{name: "Env", env: "weather", dir: h.home, project: "weather", dagsDir: weatherDir},
		{name: "FlagBeforeEnv", flag: "intraday", env: "weather", dir: h.home, project: "intraday",
			dagsDir: filepath.Join(h.dags, "projects", "intraday")},
		{name: "EnvBeforeDirectory", env: "intraday", dir: weatherDir, project: "intraday",
			dagsDir: filepath.Join(h.dags, "projects", "intraday")},
		{name: "Directory", dir: filepath.Join(weatherDir, "etl"), project: "weather", dagsDir: weatherDir},
		{name: "UnregisteredDirectory", dir: filepath.Join(h.dags, "projects", "stray"), project: "default", dagsDir: h.dags},
		{name: "DefaultDirectory", dir: h.dags, project: "default", dagsDir: h.dags},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(runenv.EnvKeyProject, tt.env)
			t.Chdir(tt.dir)

			ctx, err := NewContext(h.command(t, "status", tt.flag), nil)
			require.NoError(t, err)
			assert.Equal(t, tt.project, ctx.project.Name)
			assert.Equal(t, tt.project, ctx.DAGRunMgr.Project())
			assert.Equal(t, tt.dagsDir, ctx.projectPaths().DAGsDir)
		})
	}

	t.Run("Unknown", func(t *testing.T) {
		for _, tt := range []struct{ flag, env string }{{flag: "nowhere"}, {env: "nowhere"}} {
			t.Setenv(runenv.EnvKeyProject, tt.env)
			_, err := NewContext(h.command(t, "status", tt.flag), nil)
			require.ErrorContains(t, err, `project "nowhere" does not exist`)
		}
	})
}

// TestProjectsOff proves the dark rule: with projects off, --project is the
// unknown flag it was, DAGU_PROJECT and the current directory change
// nothing, and runs bind to no project.
func TestProjectsOff(t *testing.T) {
	h := newProjectsHome(t, false)
	t.Setenv(runenv.EnvKeyProject, "nowhere")
	t.Chdir(filepath.Join(h.dags, "projects", "weather"))

	_, err := NewContext(h.command(t, "status", "weather"), nil)
	require.ErrorIs(t, err, errProjectFlagUnknown)
	assert.EqualError(t, err, "unknown flag: --project")

	ctx, err := NewContext(h.command(t, "status", ""), nil)
	require.NoError(t, err)
	assert.Empty(t, ctx.DAGRunMgr.Project())
	assert.Equal(t, h.dags, ctx.projectPaths().DAGsDir)
	require.NoError(t, ctx.checkDAGFile([]string{h.file(t, "projects/weather/forecast.yaml")}))

	// A child that a parent started while projects were on still names the
	// default project.
	ctx, err = NewContext(h.command(t, "status", "default"), nil)
	require.NoError(t, err)
	assert.Empty(t, ctx.DAGRunMgr.Project())

	// Each command keeps its own handling of a flag error.
	_, _, err = h.runDagu(t, []*cobra.Command{Test()}, "test", "--project", "weather")
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitInvalid, exit.ExitStatus())
}

// TestProjectCommandsAddressNoProject proves that DAGU_PROJECT naming a
// project not created yet does not keep dagu project from creating it. It
// sets the environment, so it does not run in parallel.
func TestProjectCommandsAddressNoProject(t *testing.T) {
	h := newProjectsHome(t, true)
	t.Setenv(runenv.EnvKeyProject, "ops")

	for _, args := range [][]string{{"project", "create", "ops"}, {"project", "list"}} {
		_, stderr, err := h.runDagu(t, []*cobra.Command{Project()}, args...)
		require.NoError(t, err, "%v: %s", args, stderr)
	}
	stdout, _, err := h.runDagu(t, []*cobra.Command{Project()}, "project", "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Create and list projects")
}

// TestProjectEnqueue proves that a project other than the default one does
// not queue a run that no scheduler would start.
func TestProjectEnqueue(t *testing.T) {
	t.Parallel()
	h := newProjectsHome(t, true)
	h.file(t, "projects/weather/forecast.yaml")

	_, _, err := h.runDagu(t, []*cobra.Command{Enqueue()}, "enqueue", "--project", "weather", "forecast")
	require.ErrorContains(t, err, "project weather cannot queue runs; only the default project's queues are processed; run it with dagu start instead")
	ctx, err := NewContext(h.command(t, "enqueue", "weather"), nil)
	require.NoError(t, err)
	items, err := ctx.Persistence.QueueStore.All(ctx)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestProjectFlagHidden(t *testing.T) {
	t.Parallel()

	for _, command := range []*cobra.Command{Start(), Status(), Test(), Validate(), Server()} {
		flag := command.Flags().Lookup(projectFlag.name)
		require.NotNil(t, flag, command.Name())
		assert.True(t, flag.Hidden, command.Name())
		assert.NotContains(t, command.UsageString(), "--"+projectFlag.name, command.Name())
	}
}

func TestProjectFlagRefusedByServices(t *testing.T) {
	t.Parallel()
	h := newProjectsHome(t, true)

	for _, name := range []string{"server", "scheduler", "coordinator", "start-all", "worker"} {
		_, err := NewContext(h.command(t, name, "weather"), nil)
		require.ErrorContains(t, err, "serves every project", name)
	}
}

// TestCheckDAGFile proves Spec 102 criterion 50: a DAG file of another
// project is refused, and the error names its project. A file in the
// library or in no project's DAG directory runs in the addressed project.
func TestCheckDAGFile(t *testing.T) {
	t.Parallel()
	h := newProjectsHome(t, true)
	ctx, err := NewContext(h.command(t, "start", "weather"), nil)
	require.NoError(t, err)

	for _, rel := range []string{"projects/weather/forecast.yaml", "library/abc.yaml", "projects/stray/x.yaml"} {
		require.NoError(t, ctx.checkDAGFile([]string{h.file(t, rel)}), rel)
	}
	outside := filepath.Join(h.home, "elsewhere.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("steps: []\n"), 0o600))
	require.NoError(t, ctx.checkDAGFile([]string{outside}))
	require.NoError(t, ctx.checkDAGFile([]string{"forecast"}), "a name resolves in the project")

	for rel, owner := range map[string]string{"cleanup.yaml": "default", "projects/intraday/auction.yaml": "intraday"} {
		err := ctx.checkDAGFile([]string{h.file(t, rel)})
		require.ErrorContains(t, err, "belongs to project "+owner+", not to project weather", rel)
	}
	// A name is checked at the file the loader finds for it.
	err = ctx.checkDAGFile([]string{filepath.Join("..", "intraday", "auction")})
	require.ErrorContains(t, err, "belongs to project intraday")

	// A project's DAG directory that links out of paths.projects_dir still
	// holds its files.
	checkout := filepath.Join(h.home, "checkout")
	require.NoError(t, os.MkdirAll(checkout, 0o750))
	if err := os.Symlink(checkout, filepath.Join(h.dags, "projects", "linked")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	_, err = projectregistry.New(filepath.Join(h.home, "data")).Create(context.Background(), project.CreateInput{Name: "linked"})
	require.NoError(t, err)
	linked := filepath.Join(checkout, "job.yaml")
	require.NoError(t, os.WriteFile(linked, []byte("steps: []\n"), 0o600))
	require.ErrorContains(t, ctx.checkDAGFile([]string{linked}), "belongs to project linked")

	ctx.Command = &cobra.Command{Use: "secret"}
	require.NoError(t, ctx.checkDAGFile([]string{h.file(t, "cleanup.yaml")}), "only a DAG argument is checked")
}

// TestProjectTestFiles proves Spec 102 criterion 47: dagu test with no
// argument selects the addressed project's test files and no other
// project's, and a test file given by path runs in its own project.
func TestProjectTestFiles(t *testing.T) {
	t.Parallel()
	h := newProjectsHome(t, true)
	own := h.file(t, "ops/cleanup.test.yaml")
	weather := h.file(t, "projects/weather/forecast.test.yaml")
	h.file(t, "library/abc.test.yaml")

	selected := func(projectName string) []string {
		ctx, err := NewContext(h.command(t, "test", projectName), nil)
		require.NoError(t, err)
		files, err := selectTestFiles(ctx, ctx.projectPaths().DAGsDir, nil)
		require.NoError(t, err)
		files, err = ctx.ownTestFiles(files)
		require.NoError(t, err)
		var paths []string
		for _, f := range files {
			paths = append(paths, f.path)
		}
		return paths
	}
	assert.Equal(t, []string{own}, selected("default"))
	assert.Equal(t, []string{weather}, selected("weather"))

	ctx, err := NewContext(h.command(t, "test", ""), nil)
	require.NoError(t, err)
	fileCtx, err := ctx.inProjectOf(weather)
	require.NoError(t, err)
	assert.Equal(t, "weather", fileCtx.project.Name)
	assert.Equal(t, "weather", fileCtx.DAGRunMgr.Project())
	assert.Equal(t, "default", ctx.project.Name, "the addressed project stays as it was")
}

// runDagu runs args with the home's flags on a root that holds commands.
func (h projectsHome) runDagu(t *testing.T, commands []*cobra.Command, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := &cobra.Command{Use: "dagu"}
	root.AddCommand(commands...)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append(args, "--dagu-home="+h.home, "--config="+h.config))
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// TestProjectCommandOff proves the dark rule for dagu project: with projects
// off, the root help does not list it, and running it prints what an
// unknown command prints.
// It replaces how help ends the process, so it does not run in parallel.
func TestProjectCommandOff(t *testing.T) {
	h := newProjectsHome(t, false)
	exitCode := 0
	exitProjectHelp = func(code int) { exitCode = code }
	t.Cleanup(func() { exitProjectHelp = os.Exit })

	_, before, beforeErr := h.runDagu(t, []*cobra.Command{Version()}, "project", "list")
	require.Error(t, beforeErr)
	require.Contains(t, before, `unknown command "project" for "dagu"`)
	for _, args := range [][]string{{"project"}, {"project", "list"}, {"project", "create", "ops"}} {
		_, stderr, err := h.runDagu(t, []*cobra.Command{Version(), Project()}, args...)
		require.EqualError(t, err, beforeErr.Error(), args)
		assert.Equal(t, before, stderr, args)
	}
	for _, args := range [][]string{{"project", "--help"}, {"project", "create", "--help"}} {
		exitCode = 0
		stdout, stderr, _ := h.runDagu(t, []*cobra.Command{Version(), Project()}, args...)
		assert.Equal(t, 1, exitCode, args)
		assert.Equal(t, before, stderr, args)
		assert.Empty(t, stdout, args)
	}

	root := &cobra.Command{Use: "dagu"}
	root.AddCommand(Version(), Project())
	assert.NotContains(t, root.UsageString(), projectCommandName)
	assert.NoDirExists(t, filepath.Join(h.dags, "projects", "ops"))
}

func TestProjectCommands(t *testing.T) {
	t.Parallel()
	h := newProjectsHome(t, true)
	commands := []*cobra.Command{Project()}

	stdout, _, err := h.runDagu(t, commands, "project", "create", "ops")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Created project ops")
	assert.DirExists(t, filepath.Join(h.dags, "projects", "ops"))

	_, _, err = h.runDagu(t, commands, "project", "create", "OPS")
	require.ErrorIs(t, err, project.ErrNameTaken)

	require.NoError(t, os.RemoveAll(filepath.Join(h.dags, "projects", "intraday")))
	stdout, _, err = h.runDagu(t, commands, "project", "list")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 5, stdout)
	// The DAG directory, the last column, may hold spaces.
	row := func(line, dir string) []string {
		require.True(t, strings.HasSuffix(line, dir), "%q ends in %q", line, dir)
		return strings.Fields(strings.TrimSuffix(line, dir))
	}
	assert.Equal(t, []string{"NAME", "DISPLAY", "NAME", "STATE", "DAG"}, row(lines[0], "DIRECTORY"))
	assert.Equal(t, []string{"default", "default", "active"}, row(lines[1], h.dags))
	assert.Equal(t, []string{"intraday", "intraday", "missing"}, row(lines[2], filepath.Join(h.dags, "projects", "intraday")))
	assert.Equal(t, []string{"ops", "ops", "active"}, row(lines[3], filepath.Join(h.dags, "projects", "ops")))
	assert.Equal(t, []string{"weather", "weather", "active"}, row(lines[4], filepath.Join(h.dags, "projects", "weather")))
}

// TestStartGivesStepsTheProject proves Spec 102 "Steps": each step of a run
// gets DAGU_PROJECT set to the run's project while projects are on, and
// none while they are off. It sets the environment, so it does not run in
// parallel.
func TestStartGivesStepsTheProject(t *testing.T) {
	t.Setenv(runenv.EnvKeyProject, "")
	out := t.TempDir()
	writeDAG := func(t *testing.T, h projectsHome, rel string) string {
		t.Helper()
		name := strings.TrimSuffix(filepath.Base(rel), ".yaml")
		target := filepath.ToSlash(filepath.Join(out, name))
		path := filepath.Join(h.dags, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		run := test.ForOS(
			"printf '%s' \"$"+runenv.EnvKeyProject+"\" > '"+target+"'",
			"Set-Content -NoNewline -LiteralPath '"+target+"' -Value ([string]$env:"+runenv.EnvKeyProject+")",
		)
		require.NoError(t, os.WriteFile(path, []byte("steps:\n  - id: record\n    run: "+strconv.Quote(run)+"\n"), 0o600))
		return filepath.Join(out, name)
	}
	readOut := func(t *testing.T, path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return string(data)
	}

	on := newProjectsHome(t, true)
	forecast := writeDAG(t, on, "projects/weather/forecast.yaml")
	cleanup := writeDAG(t, on, "cleanup.yaml")
	_, stderr, err := on.runDagu(t, []*cobra.Command{Start()}, "start", "--project", "weather", "forecast")
	require.NoError(t, err, stderr)
	assert.Equal(t, "weather", readOut(t, forecast))

	// The run, and one of selected steps, logs in the project's directory.
	_, stderr, err = on.runDagu(t, []*cobra.Command{Start()}, "start", "--project", "weather", "--only", "record", "forecast")
	require.NoError(t, err, stderr)
	assert.Equal(t, "weather", readOut(t, forecast))
	weather, err := NewContext(on.command(t, "status", "weather"), nil)
	require.NoError(t, err)
	runs, err := weather.Persistence.DAGRunRepository.ListStatuses(weather, persis.DAGRunListOptions{})
	require.NoError(t, err)
	require.Len(t, runs, 2)
	for _, run := range runs {
		rel, err := filepath.Rel(weather.projectPaths().LogDir, run.Log)
		require.NoError(t, err)
		assert.False(t, strings.HasPrefix(rel, ".."), "%s is not under %s", run.Log, weather.projectPaths().LogDir)
	}

	_, stderr, err = on.runDagu(t, []*cobra.Command{Start()}, "start", "cleanup")
	require.NoError(t, err, stderr)
	assert.Equal(t, "default", readOut(t, cleanup))

	off := newProjectsHome(t, false)
	cleanup = writeDAG(t, off, "cleanup.yaml")
	_, stderr, err = off.runDagu(t, []*cobra.Command{Start()}, "start", "cleanup")
	require.NoError(t, err, stderr)
	assert.Empty(t, readOut(t, cleanup))
}

// A dispatch task does not carry its project yet, so only the default
// project's runs go to workers.
func TestProjectDispatch(t *testing.T) {
	t.Parallel()
	h := newProjectsHome(t, true)
	stop := errors.New("stop after dispatch")

	for projectName, dispatched := range map[string]bool{"weather": false, "default": true} {
		ctx, err := NewContext(h.command(t, "start", projectName), nil)
		require.NoError(t, err)
		ctx.Quiet = true
		client := &smtpDispatchClient{err: stop}
		err = dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "gpu"}, "run", runOptions{}, client)
		if dispatched {
			require.ErrorIs(t, err, stop)
			require.NotNil(t, client.task)
			continue
		}
		require.ErrorContains(t, err, "project weather cannot run DAGs on workers")
		require.Nil(t, client.task)
	}
}
