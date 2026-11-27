// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec101_workflow_tests_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// leftovers match what no case may leave: the *.ran file that each fixture
// command creates where it runs, in the project for a params[].eval and in
// workflows/ for the rest, and the working directory and redirect file of
// uploader.yaml. A case runs no command it does not list under real, and
// without a real step writes nothing outside its case directory.
var leftovers = []string{"*.ran", "workflows/*.ran", "workflows/missing-workdir", "workflows/result.txt"}

// Each case runs a fixture of Spec 101's examples. Retry and repeat
// intervals of 30 seconds or more fit only because no case waits for them.
func TestWorkflowTests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		file string
		exit int
		// lines are report lines that stdout holds in this order.
		lines []string
	}{
		{"release.test.yaml", 0, []string{
			"PASS workflows/release.test.yaml: deploys the built image (",
			"PASS workflows/release.test.yaml: a failed build stops the deploy (",
			"2 cases: 2 passed, 0 failed\n",
		}},
		{"failing.test.yaml", 1, []string{
			"FAIL workflows/failing.test.yaml: a failed build skips the deploy (",
			"  status: expected succeeded, got failed\n",
			"  step deploy: expected skipped, got aborted\n",
			"  step build attempts: expected 2, got 1\n",
			"1 case: 0 passed, 1 failed\n",
		}},
		// The param service has an eval without command substitution, so
		// no line names it.
		{"ship.test.yaml", 2, []string{
			"INVALID workflows/ship.test.yaml: ships\n",
			"  param build_date: ",
			"  step deploy: action ssh.run ",
			"  step hook: action http.request ",
			"  step summary: action chat.completion ",
			"  step lint: command ",
			"  step lint: precondition ",
			"  handler_on.failure: action mail.send ",
			`  fix, ready to paste:
    params:
      build_date: ""
    mocks:
      deploy: {}
      hook: {}
      summary: {}
      lint:
        precondition: met
      handler_on.failure: {}
`,
			"PASS workflows/ship.test.yaml: handling every source ships (",
			"2 cases: 1 passed, 0 failed, 1 invalid\n",
		}},
		{"inputs.test.yaml", 2, []string{
			"INVALID workflows/inputs.test.yaml: a param dagu start rejects is invalid\n  params: ",
			"3 cases: 2 passed, 0 failed, 1 invalid\n",
		}},
		{"default.test.yaml", 2, []string{
			"PASS workflows/default.test.yaml: every step succeeds by default (",
			"  default mock: build, deploy\n",
			"INVALID workflows/default.test.yaml: a mock without its declared output is invalid\n  mocks.build.outputs: ",
			"3 cases: 2 passed, 0 failed, 1 invalid\n",
		}},
		{"change.test.yaml", 0, []string{"4 cases: 4 passed, 0 failed\n"}},
		{"migrate.test.yaml", 2, []string{
			"INVALID workflows/migrate.test.yaml: an unscripted DAG precondition is invalid\n  precondition: ",
			"3 cases: 2 passed, 0 failed, 1 invalid\n",
		}},
		{"poll.test.yaml", 2, []string{
			"PASS workflows/poll.test.yaml: polls until nothing is pending (",
			"INVALID workflows/poll.test.yaml: a result without condition is invalid\n  step poll: ",
			"INVALID workflows/poll.test.yaml: an unbounded side-effect-free step is invalid\n  step idle: ",
			"PASS workflows/poll.test.yaml: without a limit the results bound a mocked step (",
			"FAIL workflows/poll.test.yaml: without a limit an unmocked step fails the case (",
			"  step tick: repeat limit 0 leaves the step continuous; mock it with results\n",
		}},
		{"window.test.yaml", 2, []string{
			"  step choose_window: human.task waits for required values; set review",
			"3 cases: 2 passed, 0 failed, 1 invalid\n",
		}},
		{"gate.test.yaml", 2, []string{
			"PASS workflows/gate.test.yaml: an approved release is announced (",
			"  approved automatically: release\n",
			"  step announce: approval waits for required values; set review",
			"3 cases: 2 passed, 0 failed, 1 invalid\n",
		}},
		{"episodes.test.yaml", 2, []string{
			"PASS workflows/episodes.test.yaml: a mocked foreach succeeds without expanding items (",
			"INVALID workflows/episodes.test.yaml: a foreach with a command body is invalid\n  step summarize, body step one: ",
		}},
		{"parent.test.yaml", 2, []string{
			"PASS workflows/parent.test.yaml: a real child of side-effect-free steps runs in the case (",
			"INVALID workflows/parent.test.yaml: a real child with a command step is invalid\n  step call, child child, step write: ",
		}},
		{"service.test.yaml", 0, []string{"2 cases: 2 passed, 0 failed\n"}},
		{"publish.test.yaml", 2, []string{
			"PASS workflows/publish.test.yaml: a case without a real step needs no secret (",
			"INVALID workflows/publish.test.yaml: a real step needs every secret\n  secret API_TOKEN: ",
		}},
		{"nightly.test.yaml", 0, []string{"PASS workflows/nightly.test.yaml: the second run attempt succeeds ("}},
		{"uploader.test.yaml", 0, []string{
			"PASS workflows/uploader.test.yaml: failing tests send mail (",
			"  notification mail_on.failure: oncall@example.com\n",
		}},
		// A stream assertion reads the last execution, so the first try of
		// the retried check is not in its stdout.
		{"report.test.yaml", 1, []string{
			"PASS workflows/report.test.yaml: the summary reports the count (",
			"PASS workflows/report.test.yaml: a failed check sends one mail (",
			"  notification mail_on.failure: oncall@example.com\n",
			"FAIL workflows/report.test.yaml: every failed assertion is reported (",
			"  attempts: expected 2, got 1\n",
			"  step count output files: expected \"03\", got \"3\"\n",
			"  step count output stats: expected {\"lines\":41}, got {\"files\":3,\"lines\":40}\n",
			"  step summary stdout: does not contain \"4 files\"\n",
			"  step summary stderr: does not match \"error\"\n",
			"  notifications: expected none, got mail_on.failure to oncall@example.com\n",
			"  notification mail_on.failure: oncall@example.com\n",
			"3 cases: 2 passed, 1 failed\n",
		}},
		// The mocked deploy is checked against the command it would have
		// run, with the mocked tag in it, and notify against its with
		// inputs, the secret as its placeholder.
		{"called.test.yaml", 2, []string{
			"PASS workflows/called.test.yaml: deploys the built image (",
			"FAIL workflows/called.test.yaml: a wrong tag reaches the deploy (",
			`  step deploy called_with.run: expected to contain "v1.2.3", got "touch deploy.ran \"v1.2.4\""` + "\n",
			"INVALID workflows/called.test.yaml: an action step takes no run\n",
			"  expect.steps.notify.called_with.run: the step runs action http.request, which takes no run; assert with\n",
			"3 cases: 1 passed, 1 failed, 1 invalid\n",
		}},
		{"smoke.test.yaml", 2, []string{"INVALID workflows/smoke.test.yaml\n", "rename", "0 cases: 0 passed, 0 failed, 1 invalid file\n"}},
		{"compile.test.yaml", 2, []string{"INVALID workflows/compile.test.yaml\n", "Spec 033"}},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("test", "workflows/"+tc.file)
			result.ExpectExitCode(tc.exit)
			expectLines(t, result.Stdout(), tc.lines...)
			result.ExpectStdoutNotContains("param service")
			for _, pattern := range leftovers {
				matches, err := filepath.Glob(dagu.ProjectPath(pattern))
				require.NoError(t, err)
				require.Empty(t, matches)
			}
		})
	}
}

// The fix an invalid case prints, pasted into its test file, makes the case
// valid (RFC 020 criterion 42). The case is the file's last entry, so its
// fields end the file. The JSON report holds the same fix.
func TestWorkflowTestFix(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("test", "workflows/fix.test.yaml")
	result.ExpectExitCode(2)
	fix := pastedBlock(t, result.Stdout(), "  fix, ready to paste:\n")
	fileFix := pastedBlock(t, result.Stdout(), "  fix for the test file, ready to paste:\n")

	report := decodeReport(t, dagu.Run("test", "--format", "json", "workflows/fix.test.yaml").Stdout())
	require.Equal(t, fix, report.Cases[0].Fix)
	require.Equal(t, fileFix, report.Cases[0].FileFix)

	original, err := os.ReadFile(dagu.ProjectPath("workflows/fix.test.yaml"))
	require.NoError(t, err)
	var caseFields strings.Builder
	for line := range strings.Lines(fix) {
		caseFields.WriteString("    " + line)
	}
	dagu.WriteFile("workflows/fix.test.yaml", fileFix+string(original)+caseFields.String())

	result = dagu.Run("test", "workflows/fix.test.yaml")
	result.ExpectExitCode(0)
	expectLines(t, result.Stdout(), "PASS workflows/fix.test.yaml: every source is unhandled (")
	for _, pattern := range leftovers {
		matches, err := filepath.Glob(dagu.ProjectPath(pattern))
		require.NoError(t, err)
		require.Empty(t, matches)
	}
}

// dagu test --init writes a test file whose case passes, and never replaces
// one (RFC 020 criterion 43). The mocks it writes keep the commands of
// release.yaml from running.
func TestWorkflowTestInit(t *testing.T) {
	t.Parallel()

	const written = `mocks:
  build:
    outputs:
      image_tag: ""
  deploy: {}
cases:
  - name: initial
    expect:
      status: succeeded
      steps:
        build: succeeded
        deploy: succeeded
`
	dagu := harness.NewRunner(t)
	require.NoError(t, os.Remove(dagu.ProjectPath("workflows/release.test.yaml")))

	result := dagu.Run("test", "--init", "workflows/release.yaml")
	result.ExpectExitCode(0)
	result.ExpectStdout("workflows/release.test.yaml\n")
	dagu.ExpectFileContent("workflows/release.test.yaml", written)

	result = dagu.Run("test", "workflows/release.test.yaml")
	result.ExpectExitCode(0)
	expectLines(t, result.Stdout(), "PASS workflows/release.test.yaml: initial (", "1 case: 1 passed, 0 failed\n")

	result = dagu.Run("test", "--init", "workflows/release.yaml")
	result.ExpectExitCode(2)
	result.ExpectStdout("")
	result.ExpectStderrContains("workflows/release.test.yaml already exists")
	dagu.ExpectFileContent("workflows/release.test.yaml", written)
	for _, pattern := range leftovers {
		matches, err := filepath.Glob(dagu.ProjectPath(pattern))
		require.NoError(t, err)
		require.Empty(t, matches)
	}
}

// dagu test --update rewrites a case's stale exact facts from its run and
// keeps the stdout assertion as written, which still fails (RFC 020
// criterion 43). The comment and the case that already holds its run's
// facts stay as they are, the next run reports only the stdout assertion,
// and a second update changes nothing.
func TestWorkflowTestUpdate(t *testing.T) {
	t.Parallel()

	const mocks = `# Cases of release.yaml. The build broke after they were written.
mocks:
  build:
    exit_code: 1
    stdout: compile error
    outputs:
      image_tag: v1.2.3
  deploy: {}
cases:
`
	const holds = `  - name: a failed build stops the deploy
    expect:
      status: failed
      steps:
        build: failed
        deploy: aborted
`
	dagu := harness.NewRunner(t)
	dagu.WriteFile("workflows/release.test.yaml", mocks+`  - name: the build passes
    expect:
      status: succeeded  # before the build broke
      steps:
        build:
          status: succeeded
          stdout:
            contains: build ok
`+holds)
	const updated = mocks + `  - name: the build passes
    expect:
      status: failed  # before the build broke
      steps:
        build:
          status: failed
          stdout:
            contains: build ok
        deploy: aborted
` + holds
	const stdoutFailure = "  step build stdout: does not contain \"build ok\"\n"

	result := dagu.Run("test", "--update", "workflows/release.test.yaml")
	result.ExpectExitCode(1)
	expectLines(t, result.Stdout(),
		"FAIL workflows/release.test.yaml: the build passes (", stdoutFailure, "  expect updated\n",
		"PASS workflows/release.test.yaml: a failed build stops the deploy (", "  expect unchanged\n")
	dagu.ExpectFileContent("workflows/release.test.yaml", updated)

	result = dagu.Run("test", "workflows/release.test.yaml")
	result.ExpectExitCode(1)
	expectLines(t, result.Stdout(), "FAIL workflows/release.test.yaml: the build passes (", stdoutFailure,
		"PASS workflows/release.test.yaml: a failed build stops the deploy (", "2 cases: 1 passed, 1 failed\n")

	result = dagu.Run("test", "--update", "--format", "json", "workflows/release.test.yaml")
	result.ExpectExitCode(1)
	report := decodeReport(t, result.Stdout())
	require.Len(t, report.Cases, 2)
	require.Equal(t, "unchanged", report.Cases[0].Update)
	require.Equal(t, "unchanged", report.Cases[1].Update)
	dagu.ExpectFileContent("workflows/release.test.yaml", updated)
	for _, pattern := range leftovers {
		matches, err := filepath.Glob(dagu.ProjectPath(pattern))
		require.NoError(t, err)
		require.Empty(t, matches)
	}
}

// The example "A Value From the Host": --update writes no value that the
// run took from the host. The report masks it and names the env entry to
// set, and a case that sets the entry is rewritten.
func TestWorkflowTestUpdateHostValues(t *testing.T) {
	t.Parallel()

	const hostToken = "spec101-host-token"
	const kept = `mocks:
  sync: {}
cases:
  - name: the host token reaches the sync
    expect:
      steps:
        sync:
          called_with:
            env:
              TOKEN: test-token
  - name: the case sets the token
    env:
      TOKEN: test-token
    expect:
      steps:
        sync:
          called_with:
            env:
              TOKEN: `
	dagu := harness.NewRunner(t)
	dagu.WriteFile("workflows/sync.yaml", "env:\n  - TOKEN: ${SYNC_TOKEN}\nsteps:\n  - id: sync\n    run: ./sync.sh\n")
	dagu.WriteFile("workflows/sync.test.yaml", kept+"old-token\n")

	result := dagu.RunWithEnv([]string{"SYNC_TOKEN=" + hostToken}, "test", "--update", "workflows/sync.test.yaml")
	result.ExpectExitCode(1)
	expectLines(t, result.Stdout(),
		"FAIL workflows/sync.test.yaml: the host token reaches the sync (",
		`  step sync called_with.env.TOKEN: expected "test-token", got "*******"`+"\n",
		"  step sync called_with.env.TOKEN: kept, it holds the value of host variable SYNC_TOKEN; set TOKEN under env\n",
		"  expect unchanged\n",
		"PASS workflows/sync.test.yaml: the case sets the token (", "  expect updated\n")
	require.NotContains(t, result.Stdout(), hostToken)
	dagu.ExpectFileContent("workflows/sync.test.yaml", kept+"test-token\n")

	result = dagu.RunWithEnv([]string{"SYNC_TOKEN=" + hostToken}, "test", "--update", "--format", "json", "workflows/sync.test.yaml")
	result.ExpectExitCode(1)
	require.NotContains(t, result.Stdout(), hostToken)
	report := decodeReport(t, result.Stdout())
	require.Len(t, report.Cases, 2)
	require.Equal(t, []jsonKept{{Step: "sync", Field: "called_with.env.TOKEN", Variables: []string{"SYNC_TOKEN"}, Env: []string{"TOKEN"}}}, report.Cases[0].UpdateKept)
	dagu.ExpectFileContent("workflows/sync.test.yaml", kept+"test-token\n")
}

// --init takes one DAG file, and neither --update nor a flag that selects or
// reports cases. An invalid argument exits with status 2 and writes nothing.
func TestWorkflowTestInitArguments(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		args   []string
		stderr string
	}{
		"test file":      {[]string{"workflows/gate.test.yaml"}, "is a test file"},
		"not a DAG file": {[]string{"workflows"}, "is not a DAG file"},
		"two files":      {[]string{"workflows/release.yaml", "workflows/gate.yaml"}, "exactly one DAG file"},
		"run":            {[]string{"--run", "initial", "workflows/release.yaml"}, "--run cannot be used with --init"},
		"keep":           {[]string{"--keep", "workflows/release.yaml"}, "--keep cannot be used with --init"},
		"json":           {[]string{"--format", "json", "workflows/release.yaml"}, "--format json cannot be used with --init"},
		"update":         {[]string{"--update", "workflows/release.yaml"}, "--update cannot be used with --init"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			require.NoError(t, os.Remove(dagu.ProjectPath("workflows/release.test.yaml")))
			result := dagu.Run(append([]string{"test", "--init"}, tc.args...)...)
			result.ExpectExitCode(2)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderr)
			dagu.ExpectNoFile("workflows/release.test.yaml")
		})
	}
}

// A case reads and writes nothing in the configured data directory, and its
// case directory is removed when it ends.
func TestWorkflowTestIsolation(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.Mkdir("tmp")
	tmp := dagu.ProjectPath("tmp")
	env := []string{"DAGU_HOME=" + dagu.ProjectPath("home"), "TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp}
	// The first command sets up the configured home.
	dagu.RunWithEnv(env, "test", "workflows/release.test.yaml").ExpectExitCode(0)
	before := snapshot(t, dagu.ProjectPath("home"))

	dagu.RunWithEnv(env, "test", "workflows/release.test.yaml", "workflows/gate.test.yaml").ExpectExitCode(2)

	require.Equal(t, before, snapshot(t, dagu.ProjectPath("home")))
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestWorkflowTestSelection(t *testing.T) {
	t.Parallel()

	// Files run in the order of their report paths. A file named twice runs
	// once, and a directory whose name starts with . is skipped.
	selected := []string{
		"PASS selection/nested/two.test.yml: two (",
		"PASS selection/one.test.yaml: one (",
		"2 cases: 2 passed, 0 failed\n",
	}

	t.Run("arguments", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("test", "selection/one.test.yaml", "selection")
		result.ExpectExitCode(0)
		expectLines(t, result.Stdout(), selected...)
		result.ExpectStdoutNotContains("three")
	})

	t.Run("DAGs directory without arguments", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"DAGU_DAGS_DIR=" + dagu.ProjectPath("selection")}, "test")
		result.ExpectExitCode(0)
		expectLines(t, result.Stdout(), selected...)
	})

	t.Run("nothing selected", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.Mkdir("empty")
		result := dagu.Run("test", "empty")
		result.ExpectExitCode(0)
		result.ExpectStdout("0 cases: 0 passed, 0 failed\n")
	})
}

// A DAG file argument, or a DAG name resolved against the DAGs directory,
// runs the test files beside the DAG file, and a test file selected twice
// runs once. A DAG file without one exits with status 2 and names the
// command that writes one.
func TestWorkflowTestDAGArgument(t *testing.T) {
	t.Parallel()

	t.Run("DAG file", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("test", "workflows/release.yaml", "workflows/release.test.yaml")
		result.ExpectExitCode(0)
		expectLines(t, result.Stdout(),
			"PASS workflows/release.test.yaml: deploys the built image (",
			"PASS workflows/release.test.yaml: a failed build stops the deploy (",
			"2 cases: 2 passed, 0 failed\n",
		)
	})

	t.Run("DAG name", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		env := []string{"DAGU_DAGS_DIR=" + dagu.ProjectPath("workflows")}
		result := dagu.RunWithEnv(env, "test", "--run", "deploys", "release")
		result.ExpectExitCode(0)
		expectLines(t, result.Stdout(),
			"PASS workflows/release.test.yaml: deploys the built image (",
			"1 case: 1 passed, 0 failed\n",
		)
	})

	t.Run("no test file", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		require.NoError(t, os.Remove(dagu.ProjectPath("workflows/release.test.yaml")))
		result := dagu.Run("test", "workflows/release.yaml")
		result.ExpectExitCode(2)
		result.ExpectStdout("")
		result.ExpectStderrContains("workflows/release.yaml has no test file; write one with: dagu test --init workflows/release.yaml")
	})
}

func TestWorkflowTestTimeout(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("test", "--timeout", "1s", "workflows/slow.test.yaml")
	result.ExpectExitCode(1)
	expectLines(t, result.Stdout(),
		"FAIL workflows/slow.test.yaml: a real step outlasts the timeout (",
		"  timeout: did not finish within 1s\n",
	)
	// The stop is logged in the case's log, not in the report.
	result.ExpectStderr("")
}

// An interrupted dagu test stops the running case, removes its case
// directory, starts no further case, and exits with a non-zero status.
func TestWorkflowTestInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the harness stops a process with a signal that Windows does not deliver")
	}
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.Mkdir("tmp")
	tmp := dagu.ProjectPath("tmp")
	process := dagu.StartWithEnv([]string{"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp},
		"test", "workflows/slow.test.yaml", "workflows/uploader.test.yaml")
	// The case of slow.test.yaml runs a real step for 10 seconds once its
	// case directory exists.
	require.Eventually(t, func() bool {
		entries, err := os.ReadDir(tmp)
		return err == nil && len(entries) > 0
	}, harness.WaitTimeout(t), 50*time.Millisecond)

	process.Stop()

	require.NotZero(t, process.ExitCode())
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NotContains(t, process.Output(), "uploader")
}

// Invalid arguments exit with status 2 and run no case.
func TestWorkflowTestArguments(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"missing path":     {"workflows/missing.test.yaml"},
		"unknown DAG name": {"missing"},
		"not a test file":  {"workflows/notes.txt"},
		"zero timeout":     {"--timeout", "0s", "workflows/release.test.yaml"},
		"invalid timeout":  {"--timeout", "soon", "workflows/release.test.yaml"},
		"unknown flag":     {"--no-such-flag", "workflows/release.test.yaml"},
		"invalid run":      {"--run", "(", "workflows/release.test.yaml"},
		"unknown format":   {"--format", "yaml", "workflows/release.test.yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			// notes.txt is neither a test file nor a DAG file.
			dagu.WriteFile("workflows/notes.txt", "")
			result := dagu.Run(append([]string{"test"}, args...)...)
			result.ExpectExitCode(2)
			result.ExpectStderrNotEmpty()
			result.ExpectStdout("")
		})
	}
}

// expectLines fails unless out holds each part, each after the one before.
func expectLines(t *testing.T, out string, parts ...string) {
	t.Helper()

	rest := out
	for _, part := range parts {
		i := strings.Index(rest, part)
		require.GreaterOrEqual(t, i, 0, "missing %q after the earlier lines in:\n%s", part, out)
		rest = rest[i+len(part):]
	}
}

// pastedBlock returns the lines indented by four spaces that follow title
// in out, without that indent.
func pastedBlock(t *testing.T, out, title string) string {
	t.Helper()

	_, rest, ok := strings.Cut(out, title)
	require.True(t, ok, "missing %q in:\n%s", title, out)
	var block strings.Builder
	for line := range strings.Lines(rest) {
		text, ok := strings.CutPrefix(line, "    ")
		if !ok {
			break
		}
		block.WriteString(text)
	}
	return block.String()
}

// snapshot maps each file under root to its content, and each directory to
// "dir".
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()

	files := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			files[path] = "dir"
			return err
		}
		data, err := os.ReadFile(path) // #nosec G304 -- the path is under the test's project.
		files[path] = string(data)
		return err
	}))
	return files
}
