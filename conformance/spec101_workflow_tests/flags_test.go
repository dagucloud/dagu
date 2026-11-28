// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec101_workflow_tests_test

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// jsonReport holds the fields of the JSON report that the tests read.
type jsonReport struct {
	Cases []struct {
		File          string           `json:"file"`
		Name          string           `json:"name"`
		Result        string           `json:"result"`
		DurationMS    *int64           `json:"duration_ms"`
		Failures      []map[string]any `json:"failures"`
		Errors        []map[string]any `json:"errors"`
		Fix           string           `json:"fix"`
		FileFix       string           `json:"file_fix"`
		DefaultMocked []string         `json:"default_mocked"`
		AutoApproved  []string         `json:"auto_approved"`
		Notifications []map[string]any `json:"notifications"`
		CaseDir       string           `json:"case_dir"`
		Update        string           `json:"update"`
		UpdateKept    []jsonKept       `json:"update_kept"`
	} `json:"cases"`
	InvalidFiles []struct {
		File   string           `json:"file"`
		Errors []map[string]any `json:"errors"`
	} `json:"invalid_files"`
}

// jsonKept is a kept object of the JSON report.
type jsonKept struct {
	Step      string   `json:"step"`
	Field     string   `json:"field"`
	Variables []string `json:"variables"`
	Env       []string `json:"env"`
}

// decodeReport decodes stdout as exactly one JSON object.
func decodeReport(t *testing.T, stdout string) jsonReport {
	t.Helper()

	var report jsonReport
	dec := json.NewDecoder(strings.NewReader(stdout))
	require.NoError(t, dec.Decode(&report), stdout)
	require.ErrorIs(t, dec.Decode(&struct{}{}), io.EOF, "stdout holds more than one JSON object")
	return report
}

func TestWorkflowTestJSON(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("test", "--format", "json", "workflows/failing.test.yaml", "workflows/ship.test.yaml", "workflows/smoke.test.yaml")
	result.ExpectExitCode(2)
	report := decodeReport(t, result.Stdout())

	require.Len(t, report.Cases, 3)
	failed := report.Cases[0]
	require.Equal(t, "workflows/failing.test.yaml", failed.File)
	require.Equal(t, "fail", failed.Result)
	require.NotNil(t, failed.DurationMS)
	require.Equal(t, []map[string]any{
		{"field": "status", "expected": "succeeded", "actual": "failed"},
		{"step": "deploy", "field": "status", "expected": "skipped", "actual": "aborted"},
		{"step": "build", "field": "attempts", "expected": float64(2), "actual": float64(1)},
	}, failed.Failures)

	invalid := report.Cases[1]
	require.Equal(t, "invalid", invalid.Result)
	require.Nil(t, invalid.DurationMS)
	require.Equal(t, "param build_date", invalid.Errors[0]["source"])
	require.Equal(t, "pass", report.Cases[2].Result)

	require.Len(t, report.InvalidFiles, 1)
	require.Equal(t, "workflows/smoke.test.yaml", report.InvalidFiles[0].File)
	require.Contains(t, report.InvalidFiles[0].Errors[0]["message"], "rename")
}

// The JUnit report has one suite per test file: a failed case is a failure,
// and an invalid case or test file an error.
func TestWorkflowTestJUnit(t *testing.T) {
	t.Parallel()

	type problem struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	}
	var report struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Suites   []struct {
			Name  string `xml:"name,attr"`
			Cases []struct {
				Name    string   `xml:"name,attr"`
				Time    string   `xml:"time,attr"`
				Failure *problem `xml:"failure"`
				Error   *problem `xml:"error"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("test", "--format", "junit", "workflows/failing.test.yaml", "workflows/ship.test.yaml", "workflows/smoke.test.yaml")
	result.ExpectExitCode(2)
	require.True(t, strings.HasPrefix(result.Stdout(), xml.Header), result.Stdout())
	require.NoError(t, xml.Unmarshal([]byte(result.Stdout()), &report), result.Stdout())

	require.Equal(t, 4, report.Tests)
	require.Equal(t, 1, report.Failures)
	require.Equal(t, 2, report.Errors)
	require.Len(t, report.Suites, 3)

	failing := report.Suites[0]
	require.Equal(t, "workflows/failing.test.yaml", failing.Name)
	require.Equal(t, "status: expected succeeded, got failed", failing.Cases[0].Failure.Message)
	require.Contains(t, failing.Cases[0].Failure.Text, "  step deploy: expected skipped, got aborted")

	ship := report.Suites[1]
	require.Len(t, ship.Cases, 2)
	require.Contains(t, ship.Cases[0].Error.Message, "param build_date")
	require.Equal(t, "0.000", ship.Cases[0].Time)
	require.Nil(t, ship.Cases[1].Failure)
	require.Nil(t, ship.Cases[1].Error)

	smoke := report.Suites[2]
	require.Equal(t, "workflows/smoke.test.yaml", smoke.Name)
	require.Equal(t, "workflows/smoke.test.yaml", smoke.Cases[0].Name)
	require.Contains(t, smoke.Cases[0].Error.Message, "rename")
}

// Each failed assertion and each list of a case that ran is in the JSON
// report too. Files run in the order default, gate, report.
func TestWorkflowTestJSONAssertions(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("test", "--format", "json", "workflows/report.test.yaml", "workflows/default.test.yaml", "workflows/gate.test.yaml")
	result.ExpectExitCode(2)
	report := decodeReport(t, result.Stdout())
	require.Len(t, report.Cases, 9)

	require.Equal(t, []string{"build", "deploy"}, report.Cases[0].DefaultMocked)
	require.Equal(t, []string{"release"}, report.Cases[3].AutoApproved)
	mail := map[string]any{"kind": "mail_on.failure", "to": []any{"oncall@example.com"}}
	require.Equal(t, []map[string]any{mail}, report.Cases[7].Notifications)

	failed := report.Cases[8]
	require.Equal(t, "every failed assertion is reported", failed.Name)
	require.Equal(t, []map[string]any{
		{"field": "attempts", "expected": float64(2), "actual": float64(1)},
		{"step": "count", "field": "outputs.files", "expected": "03", "actual": "3"},
		{
			"step": "count", "field": "outputs.stats",
			"expected": map[string]any{"lines": float64(41)},
			"actual":   map[string]any{"files": float64(3), "lines": float64(40)},
		},
		{"step": "summary", "field": "stdout.contains", "expected": "4 files"},
		{"step": "summary", "field": "stderr.matches", "expected": "error"},
		{"field": "notifications", "expected": []any{}, "actual": []any{mail}},
	}, failed.Failures)
}

func TestWorkflowTestJSONTimeout(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("test", "--format", "json", "--timeout", "1s", "workflows/slow.test.yaml")
	result.ExpectExitCode(1)
	report := decodeReport(t, result.Stdout())
	require.Len(t, report.Cases, 1)
	require.Equal(t, []map[string]any{{"field": "timeout", "expected": float64(1000)}}, report.Cases[0].Failures)
}

// --run selects cases by name. A test file error is still reported, since
// the file is still decoded.
func TestWorkflowTestRun(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, run, file string
		exit            int
		stdout          string
	}{
		{"selects", "stops", "release.test.yaml", 0,
			"PASS workflows/release.test.yaml: a failed build stops the deploy ("},
		{"selects none", "^none$", "release.test.yaml", 0, "0 cases: 0 passed, 0 failed\n"},
		{"file error", "none", "smoke.test.yaml", 2, "INVALID workflows/smoke.test.yaml\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("test", "--run", tc.run, "workflows/"+tc.file)
			result.ExpectExitCode(tc.exit)
			require.Contains(t, result.Stdout(), tc.stdout)
			result.ExpectStdoutNotContains("deploys the built image")
		})
	}
}

// --keep keeps the case directory, which holds the redirect file of the
// mocked step, and reports its path.
func TestWorkflowTestKeep(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			dagu.Mkdir("tmp")
			tmp := dagu.ProjectPath("tmp")
			env := []string{"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp}
			result := dagu.RunWithEnv(env, "test", "--keep", "--format", format, "workflows/uploader.test.yaml")
			result.ExpectExitCode(0)

			var dir string
			if format == "json" {
				dir = decodeReport(t, result.Stdout()).Cases[0].CaseDir
			} else {
				_, after, ok := strings.Cut(result.Stdout(), "  case directory ")
				require.True(t, ok, result.Stdout())
				dir, _, _ = strings.Cut(after, "\n")
			}
			require.True(t, filepath.IsAbs(dir), dir)
			var redirect string
			require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
				if err == nil && entry.Name() == "result.txt" {
					redirect = path
				}
				return err
			}))
			data, err := os.ReadFile(redirect) // #nosec G304 -- the path is under the kept case directory.
			require.NoError(t, err)
			require.Contains(t, string(data), "FAIL")
		})
	}
}
