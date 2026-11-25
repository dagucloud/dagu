// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A DAG named like a test file is an invalid test file, and the report
// suggests a name under which it runs as a DAG.
func TestTestReadsAsDAG(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "smoke.test.yaml", "steps:\n  - run: ./smoke.sh\n")
	testPath := filepath.Join(home, "dags", "smoke.test.yaml")

	stdout, err := runTestCommand(t, home, testPath)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitInvalid, exit.code)
	assert.Contains(t, stdout, "INVALID "+testReportPath(testPath)+"\n"+
		"  reads as a DAG, with steps and no cases, but Dagu treats it as a test file and does not run it as a DAG; rename it, for example to smoke-test.yaml\n")
}

// selectedDAG is a DAG whose one step runs for real in a case.
const selectedDAG = `
steps:
  - id: note
    action: log.write
    with:
      message: hi
`

// A DAG file argument, or a DAG name resolved under the DAGs directory,
// runs the test files beside the DAG file: <name>.test.yaml and
// <name>.test.yml, whichever exist. A test file that two arguments select
// runs once.
func TestTestDAGArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// files holds the test files to write, by path under the DAGs
		// directory, each with one passing case named after its path.
		files []string
		// args are the arguments, each one under the DAGs directory when
		// absolute is set.
		args     []string
		absolute bool
		cases    []string
		summary  string
	}{
		{
			name:     "TestYAML",
			files:    []string{"release.test.yaml"},
			args:     []string{"release.yaml"},
			absolute: true,
			cases:    []string{"release.test.yaml"},
			summary:  "1 case: 1 passed, 0 failed\n",
		},
		{
			name:     "BothExtensions",
			files:    []string{"release.test.yaml", "release.test.yml"},
			args:     []string{"release.yaml", "release.test.yml"},
			absolute: true,
			cases:    []string{"release.test.yaml", "release.test.yml"},
			summary:  "2 cases: 2 passed, 0 failed\n",
		},
		{
			name:    "Name",
			files:   []string{"release.test.yaml", "release.test.yml"},
			args:    []string{"release"},
			cases:   []string{"release.test.yaml", "release.test.yml"},
			summary: "2 cases: 2 passed, 0 failed\n",
		},
		{
			name:    "NestedName",
			files:   []string{"etl/daily.test.yaml"},
			args:    []string{"etl/daily"},
			cases:   []string{"etl/daily.test.yaml"},
			summary: "1 case: 1 passed, 0 failed\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			home := writeInitDAG(t, "release.yaml", selectedDAG)
			dags := filepath.Join(home, "dags")
			writeTestFile(t, filepath.Join(dags, "etl", "daily.yaml"), selectedDAG)
			for _, file := range tt.files {
				writeTestFile(t, filepath.Join(dags, file), passingCase(file))
			}
			args := tt.args
			if tt.absolute {
				args = nil
				for _, arg := range tt.args {
					args = append(args, filepath.Join(dags, arg))
				}
			}

			stdout, err := runTestCommand(t, home, args...)
			require.NoError(t, err)
			var lines []string
			for _, c := range tt.cases {
				lines = append(lines, "PASS "+testReportPath(filepath.Join(dags, c))+": "+c+" (")
			}
			assertInOrder(t, stdout, append(lines, tt.summary)...)
		})
	}
}

// A DAG file or DAG name without a test file beside its DAG file, and an
// argument that is neither a path nor a DAG name, exit with status 2 and
// run nothing. A missing test file's error names the command that writes
// one.
func TestTestDAGArgumentInvalid(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "release.yaml", selectedDAG)
	dags := filepath.Join(home, "dags")
	dagPath := filepath.Join(dags, "release.yaml")
	for arg, want := range map[string]string{
		dagPath:   dagPath + " has no test file; write one with: dagu test --init " + dagPath,
		"release": "release has no test file; write one with: dagu test --init " + dagPath,
		"missing": "missing does not exist, and the DAGs directory " + dags + " has no DAG of that name",
	} {
		stdout, err := runTestCommand(t, home, arg)
		var exit *exitStatusError
		require.ErrorAs(t, err, &exit, arg)
		assert.Equal(t, testExitInvalid, exit.code, arg)
		assert.EqualError(t, err, want)
		assert.Empty(t, stdout, arg)
	}
}

// An argument that is an existing path is a path, even when a DAG of the
// same name exists. The test changes the working directory, so it does not
// run in parallel.
func TestTestPathWinsOverName(t *testing.T) {
	home := writeInitDAG(t, "release.yaml", selectedDAG)
	dags := filepath.Join(home, "dags")
	writeTestFile(t, filepath.Join(dags, "release.test.yaml"), passingCase("named"))
	writeTestFile(t, filepath.Join(dags, "nightly.yaml"), selectedDAG)
	writeTestFile(t, filepath.Join(dags, "nightly.test.yaml"), passingCase("named"))
	work := t.TempDir()
	writeTestFile(t, filepath.Join(work, "release.yaml"), selectedDAG)
	writeTestFile(t, filepath.Join(work, "release.test.yaml"), passingCase("file"))
	writeTestFile(t, filepath.Join(work, "nightly", "smoke.yaml"), selectedDAG)
	writeTestFile(t, filepath.Join(work, "nightly", "smoke.test.yaml"), passingCase("directory"))
	t.Chdir(work)

	for arg, want := range map[string]string{
		"release.yaml": "PASS release.test.yaml: file (",
		"nightly":      "PASS nightly/smoke.test.yaml: directory (",
	} {
		stdout, err := runTestCommand(t, home, arg)
		require.NoError(t, err, arg)
		assert.Contains(t, stdout, want, arg)
		assert.NotContains(t, stdout, "named", arg)
	}
}

// --run and --update apply to the test files that a DAG argument selects as
// they do to a test file argument.
func TestTestDAGArgumentFlags(t *testing.T) {
	t.Parallel()

	t.Run("Run", func(t *testing.T) {
		t.Parallel()

		home := writeInitDAG(t, "release.yaml", selectedDAG)
		testPath := filepath.Join(home, "dags", "release.test.yaml")
		writeTestFile(t, testPath, passingCase("first")+"  - name: second\n    expect:\n      status: succeeded\n")

		stdout, err := runTestCommand(t, home, "--run", "second", "release")
		require.NoError(t, err)
		assert.Contains(t, stdout, "PASS "+testReportPath(testPath)+": second (")
		assert.NotContains(t, stdout, "first")
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		home := writeInitDAG(t, "release.yaml", selectedDAG)
		testPath := filepath.Join(home, "dags", "release.test.yaml")
		writeTestFile(t, testPath, "cases:\n  - name: stale\n    expect:\n      status: failed\n")

		stdout, err := runTestCommand(t, home, "--update", filepath.Join(home, "dags", "release.yaml"))
		require.NoError(t, err)
		assertInOrder(t, stdout, "PASS "+testReportPath(testPath)+": stale (", "  expect updated\n")
		assertFileContent(t, testPath, "cases:\n  - name: stale\n    expect:\n      status: succeeded\n      steps:\n        note: succeeded\n")
	})
}

// passingCase returns a test file with one case named name that passes.
func passingCase(name string) string {
	return "cases:\n  - name: " + name + "\n    expect:\n      status: succeeded\n"
}

// writeTestFile writes content to path, creating its directory.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
