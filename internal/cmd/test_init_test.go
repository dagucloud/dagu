// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --init writes a test file whose case expects the facts of a run with its
// mocks: a param for each required param without a default and each eval
// that runs a command, the DAG-level precondition, step attempts above one,
// handlers, and notifications. dagu test then passes the case.
func TestTestInit(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "rich.yaml", `
params:
  - name: region
    required: true
    enum: [eu, us]
  - name: env
    default: prod
  - name: stamp
    eval: $(date +%s)
preconditions:
  - condition: test -f /nonexistent
    negate: true
mail_on:
  success: true
info_mail:
  from: dagu@example.com
  to: ops@example.com
steps:
  - id: poll
    run: ./poll.sh
    repeat_policy:
      repeat: while
      limit: 3
      interval_sec: 60
  - name: note
    action: log.write
    with:
      message: hi ${params.region}
handler_on:
  exit:
    run: ./cleanup.sh
`)
	dagPath := filepath.Join(home, "dags", "rich.yaml")
	testPath := filepath.Join(home, "dags", "rich.test.yaml")

	stdout, err := runTestCommand(t, home, "--init", dagPath)
	require.NoError(t, err)
	assert.Equal(t, testReportPath(testPath)+"\n", stdout)
	assertFileContent(t, testPath, `params:
  region: eu
  stamp: ""
mocks:
  poll: {}
  handler_on.exit: {}
cases:
  - name: initial
    precondition: met
    expect:
      status: succeeded
      steps:
        poll:
          status: succeeded
          attempts: 3
        note: succeeded
        handler_on.exit: succeeded
      notifications:
        - kind: mail_on.success
          to:
            - ops@example.com
`)

	stdout, err = runTestCommand(t, home, testPath)
	require.NoError(t, err)
	assert.Contains(t, stdout, "PASS "+testReportPath(testPath)+": initial (")
}

// A written case that does not pass expects status: succeeded, and its
// report follows the path: an invalid case exits with status 2, and a run
// that does not finish with status 1 and a line saying no run observed the
// written expect.
func TestTestInitNotPassing(t *testing.T) {
	t.Parallel()

	const unfinished = "  the run did not finish, so the written expect is status: succeeded, not the run's result\n"
	tests := []struct {
		name   string
		dag    string
		code   int
		result string
		// report starts the lines that follow the case's result line.
		report string
		file   string
	}{
		{
			name: "Invalid",
			dag: `
steps:
  - id: ask
    action: human.task
    with:
      prompt: Enter a code
      form:
        type: object
        required: [code]
        properties:
          code:
            type: string
            pattern: "^[A-Z]{3}$"
`,
			code:   testExitInvalid,
			result: "INVALID",
			report: "  mocks.ask.review.values: the task's form rejects them",
			file: `mocks:
  ask:
    review:
      decision: approve
      values:
        code: "" # replace with a value the form accepts
cases:
  - name: initial
    expect:
      status: succeeded
`,
		},
		{
			name: "RunFails",
			dag: `
params:
  - name: n
    default: "0"
steps:
  - id: tick
    action: log.write
    with:
      message: tick
    repeat_policy:
      repeat: while
      limit: ${params.n}
`,
			code:   testExitFailed,
			result: "FAIL",
			report: "  step tick: repeat limit 0 leaves the step continuous; mock it with results\n" + unfinished,
			file: `cases:
  - name: initial
    expect:
      status: succeeded
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			home := writeInitDAG(t, "case.yaml", tt.dag)
			testPath := filepath.Join(home, "dags", "case.test.yaml")
			stdout, err := runTestCommand(t, home, "--init", filepath.Join(home, "dags", "case.yaml"))
			var exit *exitStatusError
			require.ErrorAs(t, err, &exit)
			assert.Equal(t, tt.code, exit.code)
			reported := testReportPath(testPath)
			path, rest, _ := strings.Cut(stdout, "\n")
			assert.Equal(t, reported, path)
			resultLine, report, _ := strings.Cut(rest, "\n")
			assert.True(t, strings.HasPrefix(resultLine, tt.result+" "+reported+": initial"), resultLine)
			assert.True(t, strings.HasPrefix(report, tt.report), report)
			if tt.code == testExitInvalid {
				assert.NotContains(t, report, unfinished)
			}
			assertFileContent(t, testPath, tt.file)
		})
	}
}

// writeInitDAG writes a DAG file into the DAGs directory of a new home and
// returns the home.
func writeInitDAG(t *testing.T, name, yaml string) string {
	t.Helper()

	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("debug: false\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "dags"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, "dags", name), []byte(yaml), 0o600))
	return home
}

// runTestCommand runs dagu test with args under the configuration of home
// and returns its stdout.
func runTestCommand(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()

	command := Test()
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(io.Discard)
	command.SetArgs(append(args, "--dagu-home", home, "--config", filepath.Join(home, "config.yaml")))
	err := command.ExecuteContext(context.Background())
	return stdout.String(), err
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path) // #nosec G304 -- the path is under the test's directory.
	require.NoError(t, err)
	assert.Equal(t, want, string(data))
}
