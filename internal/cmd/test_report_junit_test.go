// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"encoding/xml"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed case is a failure and an invalid case or file an error, each
// with the case's lines of the text report. Suites follow the run order of
// their files.
func TestJUnitReport(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	report := &testReport{out: &out, junit: newJUnitTestReport()}
	report.writeInvalidFile("smoke.test.yaml", []dagtest.Finding{{Source: "cases[0]", Message: "unknown field"}})
	report.writeCase(caseResult{
		file: "release.test.yaml", name: "a failed build stops the deploy", duration: 61 * time.Millisecond,
		failures: []caseFailure{
			{field: failureStatus, expected: "succeeded", actual: "failed"},
			{step: "deploy", field: failureStatus, expected: "skipped", actual: "aborted"},
		},
	})
	report.writeCase(caseResult{
		file: "release.test.yaml", name: `deploys <image> & "tag"`, duration: 80 * time.Millisecond,
		defaultMocked: []string{"build"},
	})
	report.writeCase(caseResult{
		file: "ship.test.yaml", name: "ships",
		errors: []dagtest.Finding{{Source: "param build_date", Message: "reads the host clock"}},
		fix:    "params:\n  build_date: 2026-01-01\n",
	})
	require.NoError(t, report.writeSummary())

	assert.Equal(t, xml.Header+`<testsuites tests="4" failures="1" errors="2" time="0.141">
  <testsuite name="smoke.test.yaml" tests="1" failures="0" errors="1" time="0.000">
    <testcase name="smoke.test.yaml" classname="smoke.test.yaml" time="0.000">
      <error message="cases[0]: unknown field">  cases[0]: unknown field&#xA;</error>
    </testcase>
  </testsuite>
  <testsuite name="release.test.yaml" tests="2" failures="1" errors="0" time="0.141">
    <testcase name="a failed build stops the deploy" classname="release.test.yaml" time="0.061">
      <failure message="status: expected succeeded, got failed">  status: expected succeeded, got failed&#xA;  step deploy: expected skipped, got aborted&#xA;</failure>
    </testcase>
    <testcase name="deploys &lt;image&gt; &amp; &#34;tag&#34;" classname="release.test.yaml" time="0.080">
      <system-out>  default mock: build&#xA;</system-out>
    </testcase>
  </testsuite>
  <testsuite name="ship.test.yaml" tests="1" failures="0" errors="1" time="0.000">
    <testcase name="ships" classname="ship.test.yaml" time="0.000">
      <error message="param build_date: reads the host clock">  param build_date: reads the host clock&#xA;  fix, ready to paste:&#xA;    params:&#xA;      build_date: 2026-01-01&#xA;</error>
    </testcase>
  </testsuite>
</testsuites>
`, out.String())

	var doc junitTestSuites
	require.NoError(t, xml.Unmarshal(out.Bytes(), &doc))
}

func TestTestFormatJUnit(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "release.yaml", selectedDAG)
	testPath := filepath.Join(home, "dags", "release.test.yaml")
	writeTestFile(t, testPath, passingCase("first"))

	stdout, err := runTestCommand(t, home, "--format", "junit", "release")
	require.NoError(t, err)
	var doc junitTestSuites
	require.NoError(t, xml.Unmarshal([]byte(stdout), &doc), stdout)
	require.Len(t, doc.Suites, 1)
	assert.Equal(t, testReportPath(testPath), doc.Suites[0].Name)
	assert.Equal(t, 1, doc.Tests)
	assert.Equal(t, "first", doc.Suites[0].Cases[0].Name)

	for _, tc := range []struct {
		name string
		args []string
		err  string
	}{
		{"unknown format", []string{"--format", "yaml", "release"}, `--format "yaml": use text, json, or junit`},
		{"init", []string{"--init", "--format", "junit", filepath.Join(home, "dags", "release.yaml")}, "--format junit cannot be used with --init"},
	} {
		_, err := runTestCommand(t, home, tc.args...)
		var exit *exitStatusError
		require.ErrorAs(t, err, &exit, tc.name)
		assert.Equal(t, testExitInvalid, exit.code, tc.name)
		assert.ErrorContains(t, err, tc.err, tc.name)
	}
}
