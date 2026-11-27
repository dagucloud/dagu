// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --update rewrites the exact facts of each case from its run and keeps
// what holds as written: a text output takes its published text in its
// quotes, a number that matches by value stays, a json object takes the
// values of its listed keys only, a step without an assertion gains its
// status, and notifications that do not hold take the sent ones. A stream
// assertion and an output the failed step never published stay and still
// fail, in the update's report and in the next run's.
func TestTestUpdate(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "report.yaml", `
mail_on:
  failure: true
error_mail:
  from: dagu@example.com
  to: oncall@example.com
steps:
  - id: count
    run: ./count.sh
    outputs:
      - name: files
      - name: total
      - name: stats
        type: json
  - id: summary
    depends: count
    action: log.write
    with:
      message: ${steps.count.outputs.files} files changed
  - id: check
    depends: summary
    run: ./check.sh
    retry_policy:
      limit: 1
      interval_sec: 60
handler_on:
  exit:
    run: ./cleanup.sh
`)
	const mocks = `mocks:
  count:
    outputs:
      files: "4"
      total: "7"
      stats:
        files: 4
        lines: 40
  check:
    results:
      - exit_code: 1
      - stdout: second try
  handler_on.exit: {}
`
	testPath := filepath.Join(home, "dags", "report.test.yaml")
	require.NoError(t, os.WriteFile(testPath, []byte(mocks+`cases:
  # Four files changed since this case was written.
  - name: the summary reports the count
    expect:
      status: succeeded
      attempts: 1
      steps:
        count:
          outputs:
            files: "3"
            total: 7.0
            stats:
              files: 3
        summary:
          stdout:
            contains: 3 files changed
        check: succeeded
      notifications: []
  - name: a failed count publishes nothing
    mocks:
      count:
        exit_code: 1
    expect:
      status: succeeded
      steps:
        count:
          status: succeeded
          outputs:
            files: "3"
      notifications: []
`), 0o600))

	reported := testReportPath(testPath)
	failures := []string{
		"FAIL " + reported + ": the summary reports the count (",
		`  step summary stdout: does not contain "3 files changed"` + "\n",
		"FAIL " + reported + ": a failed count publishes nothing (",
		`  step count output files: expected "3", got nothing` + "\n",
	}
	stdout, err := runTestCommand(t, home, "--update", testPath)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitFailed, exit.code)
	assertInOrder(t, stdout, failures[0], failures[1], "  expect updated\n", failures[2], failures[3], "  expect updated\n")
	assertFileContent(t, testPath, mocks+`cases:
  # Four files changed since this case was written.
  - name: the summary reports the count
    expect:
      status: succeeded
      attempts: 1
      steps:
        count:
          outputs:
            files: "4"
            total: 7.0
            stats:
              files: 4
        summary:
          stdout:
            contains: 3 files changed
        check: succeeded
        handler_on.exit: succeeded
      notifications: []
  - name: a failed count publishes nothing
    mocks:
      count:
        exit_code: 1
    expect:
      status: failed
      steps:
        count:
          status: failed
          outputs:
            files: "3"
        summary: aborted
        check: aborted
        handler_on.exit: succeeded
      notifications:
        - kind: mail_on.failure
          to:
            - oncall@example.com
`)

	stdout, err = runTestCommand(t, home, testPath)
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitFailed, exit.code)
	assertInOrder(t, stdout, failures...)
	assert.Contains(t, stdout, "2 cases: 0 passed, 2 failed\n")
	assert.NotContains(t, stdout, "\n  expect ")
}

// --update rewrites each called_with value that does not hold from the
// step's call: a run text in its quotes and with its comment, the listed
// keys of a with object, and a variable, a secret as its placeholder. A run
// given as contains, and an input the call lacks, stay and still fail.
func TestTestUpdateCalledWith(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "release.yaml", calledWithDAG)
	testPath := filepath.Join(home, "dags", "release.test.yaml")
	const kept = `  - name: the tag is in the run
    expect:
      steps:
        build: succeeded
        deploy:
          called_with:
            run:
              contains: v1.2.2
        notify: succeeded
        handler_on.failure: not_started
`
	stale := func(tag, token string) string {
		return `  - name: deploy takes the built tag
    expect:
      steps:
        build: succeeded
        deploy:
          called_with:
            run: './deploy.sh "` + tag + `"'  # the last release
            env:
              IMAGE_TAG: ` + tag + `
              REGION: eu-west-1
        notify:
          called_with:
            with:
              method: POST
              headers:
                Authorization: Bearer ` + token + `
              timeout: 30
        handler_on.failure: not_started
`
	}
	require.NoError(t, os.WriteFile(testPath, []byte(calledWithMocks+"cases:\n"+stale("v1.2.2", "old")+kept), 0o600))

	reported := testReportPath(testPath)
	timeout := "  step notify called_with.with.timeout: expected 30, got nothing\n"
	contains := `  step deploy called_with.run: expected to contain "v1.2.2", got "./deploy.sh \"v1.2.3\""` + "\n"
	stdout, err := runTestCommand(t, home, "--update", testPath)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitFailed, exit.code)
	assertInOrder(t, stdout,
		"FAIL "+reported+": deploy takes the built tag (", timeout, "  expect updated\n",
		"FAIL "+reported+": the tag is in the run (", contains, "  expect unchanged\n")
	assertFileContent(t, testPath, calledWithMocks+"cases:\n"+stale("v1.2.3", "<secret:API_TOKEN>")+kept)
}

// Values of the host environment that the tests below set.
const (
	updateHostToken = "host-token-7f3a"
	updatePassToken = "pass-token-91c2"
	updateForwarded = "forwarded-5d0e"
)

// hostValuesDAG reads a host variable into a DAG env entry, which reaches a
// run text, a variable, and a with input.
const hostValuesDAG = `env:
  - TOKEN: ${TEST_UPDATE_HOST_TOKEN}
  - REGION: eu-west-1
steps:
  - id: deploy
    run: ./deploy.sh ${env.TOKEN}
  - id: notify
    depends: deploy
    action: http.request
    with:
      method: POST
      url: https://hooks.example.invalid/deploys
      headers:
        Authorization: Bearer ${TOKEN}
`

// --update writes no value that the run took from the host. A value that
// holds the value of a host variable stays as written, and so does a
// variable that Dagu forwards from the host, also one forwarded by default.
// Each kept value has a line that names the env entries to set, and the
// report masks host values. A case that sets those entries in env is
// rewritten. t.Setenv rules out t.Parallel.
func TestTestUpdateHostValues(t *testing.T) {
	t.Setenv("TEST_UPDATE_HOST_TOKEN", updateHostToken)
	t.Setenv("TEST_UPDATE_PASS_TOKEN", updatePassToken)
	t.Setenv("DAG_TEST_UPDATE_FORWARDED", updateForwarded)

	home := writeInitDAG(t, "deploy.yaml", hostValuesDAG)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("env_passthrough: [TEST_UPDATE_PASS_TOKEN]\n"), 0o600))
	testPath := filepath.Join(home, "dags", "deploy.test.yaml")
	const mocks = "mocks:\n  deploy: {}\n  notify: {}\ncases:\n"
	kept := func(region string) string {
		return `  - name: host values stay out
    expect:
      steps:
        deploy:
          called_with:
            run: ./deploy.sh old
            env:
              TOKEN: old
              TEST_UPDATE_PASS_TOKEN: old
              DAG_TEST_UPDATE_FORWARDED: old
              REGION: ` + region + `
        notify:
          called_with:
            with:
              headers:
                Authorization: Bearer old
`
	}
	pinned := func(token, forwarded string) string {
		return `  - name: the case sets the token
    env:
      TOKEN: test-token
      DAG_TEST_UPDATE_FORWARDED: test-forwarded
    expect:
      steps:
        deploy:
          called_with:
            env:
              TOKEN: ` + token + `
              DAG_TEST_UPDATE_FORWARDED: ` + forwarded + `
        notify:
          called_with:
            with:
              headers:
                Authorization: Bearer ` + token + `
`
	}
	require.NoError(t, os.WriteFile(testPath, []byte(mocks+kept("us-east-1")+pinned("old", "old")), 0o600))

	reported := testReportPath(testPath)
	stdout, err := runTestCommand(t, home, "--update", testPath)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitFailed, exit.code)
	assertInOrder(t, stdout,
		"FAIL "+reported+": host values stay out (",
		`  step deploy called_with.run: expected "./deploy.sh old", got "./deploy.sh *******"`+"\n",
		`  step deploy called_with.env.TOKEN: expected "old", got "*******"`+"\n",
		`  step deploy called_with.env.TEST_UPDATE_PASS_TOKEN: expected "old", got "*******"`+"\n",
		`  step deploy called_with.env.DAG_TEST_UPDATE_FORWARDED: expected "old", got "`+updateForwarded+`"`+"\n",
		`  step notify called_with.with.headers: expected {"Authorization":"Bearer old"}, got {"Authorization":"Bearer *******"}`+"\n",
		"  step deploy called_with.run: kept, it holds the value of host variable TEST_UPDATE_HOST_TOKEN; set TOKEN under env\n",
		"  step deploy called_with.env.TOKEN: kept, it holds the value of host variable TEST_UPDATE_HOST_TOKEN; set TOKEN under env\n",
		"  step deploy called_with.env.TEST_UPDATE_PASS_TOKEN: kept, it holds the value of host variable TEST_UPDATE_PASS_TOKEN; set TEST_UPDATE_PASS_TOKEN under env\n",
		"  step deploy called_with.env.DAG_TEST_UPDATE_FORWARDED: kept, it holds the value of host variable DAG_TEST_UPDATE_FORWARDED; set DAG_TEST_UPDATE_FORWARDED under env\n",
		"  step notify called_with.with.headers: kept, it holds the value of host variable TEST_UPDATE_HOST_TOKEN; set TOKEN under env\n",
		"  expect updated\n",
		"PASS "+reported+": the case sets the token (", "  expect updated\n")
	assert.NotContains(t, stdout, updateHostToken)
	assert.NotContains(t, stdout, updatePassToken)
	updated := mocks + kept("eu-west-1") + pinned("test-token", "test-forwarded")
	assertFileContent(t, testPath, updated)

	stdout, err = runTestCommand(t, home, "--update", "--format", "json", testPath)
	require.ErrorAs(t, err, &exit)
	assert.NotContains(t, stdout, updateHostToken)
	assert.NotContains(t, stdout, updatePassToken)
	var report jsonTestReport
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Cases, 2)
	assert.Equal(t, updateUnchanged, report.Cases[0].Update)
	assert.Equal(t, []jsonKept{
		{Step: "deploy", Field: "called_with.run", Variables: []string{"TEST_UPDATE_HOST_TOKEN"}, Env: []string{"TOKEN"}},
		{Step: "deploy", Field: "called_with.env.TOKEN", Variables: []string{"TEST_UPDATE_HOST_TOKEN"}, Env: []string{"TOKEN"}},
		{Step: "deploy", Field: "called_with.env.TEST_UPDATE_PASS_TOKEN", Variables: []string{"TEST_UPDATE_PASS_TOKEN"}, Env: []string{"TEST_UPDATE_PASS_TOKEN"}},
		{Step: "deploy", Field: "called_with.env.DAG_TEST_UPDATE_FORWARDED", Variables: []string{"DAG_TEST_UPDATE_FORWARDED"}, Env: []string{"DAG_TEST_UPDATE_FORWARDED"}},
		{Step: "notify", Field: "called_with.with.headers", Variables: []string{"TEST_UPDATE_HOST_TOKEN"}, Env: []string{"TOKEN"}},
	}, report.Cases[0].UpdateKept)
	assert.Empty(t, report.Cases[1].UpdateKept)
	assertFileContent(t, testPath, updated)
}

// A part of a host value stays out too: a string or a number that a JSON
// path selects, directly or through a DAG env entry read from the host,
// also a number whose JSON text differs from the text it resolves to, and
// the listed keys of a with object that hold one. A part shorter than four
// characters, such as the code 482, counts where a value equals it. Only
// those keys stay: another changed key of the same object is written, also
// one that merely contains a short part, such as us. So do notifications
// whose recipient a DAG env entry read from the host.
// t.Setenv rules out t.Parallel.
func TestTestUpdateHostParts(t *testing.T) {
	const user = "svc-deploy-41"
	// The pin is written in exponent form and resolves to 736491.
	const pin, resolvedPin = "7.36491e5", "736491"
	t.Setenv("TEST_UPDATE_HOST_AUTH", `{"token":"`+updateHostToken+`","user":"`+user+`","pin":`+pin+`,"code":482,"region":"us","debug":true}`)
	t.Setenv("TEST_UPDATE_HOST_MAIL", "ops-7e21@example.com")

	home := writeInitDAG(t, "report.yaml", `mail_on:
  success: true
info_mail:
  from: dagu@example.com
  to: ${MAIL_TO}
env:
  - AUTH: ${TEST_UPDATE_HOST_AUTH}
  - PIN: ${TEST_UPDATE_HOST_AUTH.pin}
  - CODE: ${TEST_UPDATE_HOST_AUTH.code}
  - MAIL_TO: ${TEST_UPDATE_HOST_MAIL}
steps:
  - id: notify
    action: http.request
    with:
      method: POST
      url: https://hooks.example.invalid/reports
      headers:
        Authorization: ${AUTH.token}
        X-Pin: ${AUTH.pin}
        X-Code: ${AUTH.code}
        X-Region: us-east-1
      body: ${AUTH}
`)
	testPath := filepath.Join(home, "dags", "report.test.yaml")
	written := func(method, region string) string {
		return `mocks:
  notify: {}
cases:
  - name: parts stay out
    expect:
      steps:
        notify:
          called_with:
            with:
              method: ` + method + `
              headers:
                Authorization: old
                X-Pin: old
                X-Code: old
                X-Region: ` + region + `
              body:
                user: old
                pin: 0
            env:
              PIN: old
              CODE: old
      notifications: []
`
	}
	require.NoError(t, os.WriteFile(testPath, []byte(written("GET", "eu-west-1")), 0o600))

	stdout, err := runTestCommand(t, home, "--update", testPath)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitFailed, exit.code)
	assertInOrder(t, stdout,
		"FAIL "+testReportPath(testPath)+": parts stay out (",
		`  step notify called_with.with.headers: expected {"Authorization":"old","X-Code":"old","X-Pin":"old","X-Region":"us-east-1"}, got {"Authorization":"*******","X-Code":"*******","X-Pin":"*******","X-Region":"us-east-1"}`+"\n",
		`  step notify called_with.with.body: expected {"pin":0,"user":"old"}, got `,
		`  step notify called_with.env.PIN: expected "old", got "*******"`+"\n",
		`  step notify called_with.env.CODE: expected "old", got "*******"`+"\n",
		"  notifications: expected none, got mail_on.success to ops-7e21@example.com\n",
		"  step notify called_with.with.headers: kept, it holds the value of host variable TEST_UPDATE_HOST_AUTH; set AUTH, CODE, PIN under env\n",
		"  step notify called_with.with.body: kept, it holds the value of host variable TEST_UPDATE_HOST_AUTH; set AUTH, CODE, PIN under env\n",
		"  step notify called_with.env.PIN: kept, it holds the value of host variable TEST_UPDATE_HOST_AUTH; set AUTH, CODE, PIN under env\n",
		"  step notify called_with.env.CODE: kept, it holds the value of host variable TEST_UPDATE_HOST_AUTH; set AUTH, CODE, PIN under env\n",
		"  notifications: kept, it holds the value of host variable TEST_UPDATE_HOST_MAIL\n",
		"  expect updated\n")
	for _, part := range []string{updateHostToken, user, pin, resolvedPin} {
		assert.NotContains(t, stdout, part)
	}
	assertFileContent(t, testPath, written("POST", "us-east-1"))
}

// --update rewrites only the cases --run selects, and keeps the expect of
// an invalid case and of a run that does not finish. The JSON report names
// each outcome, and a second update finds the rewritten case unchanged.
func TestTestUpdateKept(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "tick.yaml", `
params:
  - name: n
    default: "2"
steps:
  - id: tick
    action: log.write
    with:
      message: tick
    repeat_policy:
      repeat: while
      limit: ${params.n}
`)
	const stale = `  - name: unfinished
    params:
      n: "0"
    expect:
      status: failed
  - name: invalid
    mocks:
      nope: {}
    expect:
      status: failed
  - name: unselected
    expect:
      status: failed
`
	testPath := filepath.Join(home, "dags", "tick.test.yaml")
	require.NoError(t, os.WriteFile(testPath, []byte("cases:\n  - name: finished\n    expect:\n      status: failed\n"+stale), 0o600))

	selected := "^(finished|unfinished|invalid)$"
	stdout, err := runTestCommand(t, home, "--update", "--run", selected, "--format", "json", testPath)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitInvalid, exit.code)
	var report jsonTestReport
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	outcomes := make(map[string][2]string)
	for _, c := range report.Cases {
		outcomes[c.Name] = [2]string{c.Result, c.Update}
	}
	assert.Equal(t, map[string][2]string{
		"finished":   {resultPass, updateUpdated},
		"unfinished": {resultFail, updateKept},
		"invalid":    {resultInvalid, updateKept},
	}, outcomes)
	updated := "cases:\n  - name: finished\n    expect:\n      status: succeeded\n      steps:\n        tick:\n          status: succeeded\n          attempts: 2\n" + stale
	assertFileContent(t, testPath, updated)

	stdout, err = runTestCommand(t, home, "--update", "--run", selected, testPath)
	require.ErrorAs(t, err, &exit)
	reported := testReportPath(testPath)
	assertInOrder(t, stdout,
		"PASS "+reported+": finished (", "  expect unchanged\n",
		"FAIL "+reported+": unfinished (", "  expect kept: the run did not finish\n",
		"INVALID "+reported+": invalid\n", "  expect kept: the case is invalid\n")
	assertFileContent(t, testPath, updated)
}

// A test file edited while its cases ran is not written, and the next file
// still is. The report ends as usual, and the command exits 1 with an error
// that names the file. The case's real step edits its own test file.
func TestTestUpdateFileChanged(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "b.yaml", "steps:\n  - id: note\n    action: log.write\n    with:\n      message: hi\n")
	dags := filepath.Join(home, "dags")
	edited := filepath.Join(dags, "a.test.yaml")
	edit := test.ForOS(
		"echo '# edited' >> "+test.PosixQuote(edited),
		"Add-Content -Path "+test.PowerShellQuote(edited)+" -Value '# edited'",
	)
	require.NoError(t, os.WriteFile(filepath.Join(dags, "a.yaml"), fmt.Appendf(nil, "steps:\n  - id: edit\n    run: %q\n", edit), 0o600))
	const stale = "cases:\n  - name: c\n    expect:\n      status: failed\n"
	require.NoError(t, os.WriteFile(edited, []byte("real: [edit]\n"+stale), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dags, "b.test.yaml"), []byte(stale), 0o600))

	stdout, err := runTestCommand(t, home, "--update", "--format", "json", dags)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitFailed, exit.code)
	assert.ErrorContains(t, err, edited+" changed while its cases ran")
	var report jsonTestReport
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	assert.Len(t, report.Cases, 2)
	data, err := os.ReadFile(edited) // #nosec G304 -- the path is under the test's directory.
	require.NoError(t, err)
	assert.Contains(t, string(data), stale)
	assert.Contains(t, string(data), "# edited")
	assertFileContent(t, filepath.Join(dags, "b.test.yaml"), "cases:\n  - name: c\n    expect:\n      status: succeeded\n      steps:\n        note: succeeded\n")
}

// Notifications stay as written when one of several recipients equals a
// short part of a host value.
func TestUpdatedExpectShortRecipient(t *testing.T) {
	t.Parallel()

	tc := &testCase{
		dag:           &ir.DAG{},
		notifications: []agent.Notification{{Kind: agent.NotificationMailOnSuccess, To: []string{"a@b", "ops@example.com"}}},
		host:          hostValuesOf(map[string][]string{"HOST_MAIL": {`{"to":"a@b"}`}}),
	}
	written := dagtest.Expect{Notifications: []dagtest.Notification{}}
	merged, kept := tc.updatedExpect(written, &ir.DAGRunStatus{Status: ir.Succeeded})
	assert.Equal(t, written.Notifications, merged.Notifications)
	assert.Equal(t, []keptValue{{field: failureNotifications, vars: []string{"HOST_MAIL"}}}, kept)
}

// A reported object with a long and a short part of a host value masks
// both, whichever is masked first.
func TestHostValuesMask(t *testing.T) {
	t.Parallel()

	host := hostValuesOf(map[string][]string{"HOST_AUTH": {`{"pin":736491,"code":482}`}})
	masked := host.mask(masking.NewValueMasker(host.values), map[string]any{"pin": int64(736491), "code": int64(482), "region": "us"})
	assert.JSONEq(t, `{"pin":"*******","code":"*******","region":"us"}`, string(masked))
}

// An output assertion takes the published value as YAML writes it exactly,
// and keeps its value where the published one does not fit: an object keeps
// its own keys, and a number that would read back as another number stays.
func TestUpdatedOutput(t *testing.T) {
	t.Parallel()

	stats := map[string]any{"a": 1, "b": 2}
	nested := map[string]any{"a": map[string]any{"x": 1}}
	tests := []struct {
		name     string
		expected any
		json     bool
		text     string
		want     any
	}{
		{"Text", "3", false, "4", "4"},
		{"MatchingNumber", 7.0, false, "7", 7.0},
		{"BeyondInt64", 1, false, "9223372036854775809", uint64(9223372036854775809)},
		{"BeyondUint64", 1, false, "18446744073709551617", 1},
		{"Decimal", 1, false, "0.1", 0.1},
		{"LongDecimal", 1.5, false, "3.14159265358979323846", 1.5},
		{"NotJSON", 3, false, "abc", "abc"},
		{"ListedKeys", stats, true, `{"a": 5, "c": 3}`, map[string]any{"a": int64(5), "b": 2}},
		{"NotAnObject", nested, true, `{"a": 3}`, nested},
		{"ObjectNotJSON", stats, false, "abc", stats},
		{"JSONOutputString", "x", true, `{"n": 1}`, map[string]any{"n": int64(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			o := dagtest.OutputExpect{Name: "out", Value: tt.expected, JSON: tt.json}
			assert.Equal(t, tt.want, updatedOutput(o, tt.text))
		})
	}
}

// assertInOrder fails unless out holds each part, each after the one
// before.
func assertInOrder(t *testing.T, out string, parts ...string) {
	t.Helper()

	rest := out
	for _, part := range parts {
		_, after, ok := strings.Cut(rest, part)
		require.True(t, ok, "missing %q after the earlier parts in:\n%s", part, out)
		rest = after
	}
}
