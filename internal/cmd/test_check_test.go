// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The facts of a run count the executions of every run attempt, list every
// step in DAG order and then every handler in Spec 101's order, including
// ones that never started, and pass check. The first attempt fails sync,
// and the run's retry policy retries it.
func TestExpectFromRun(t *testing.T) {
	t.Parallel()

	tc, status, err := runCase(t, `
retry_policy:
  limit: 2
  interval_sec: 3600
mail_on:
  success: true
info_mail:
  from: dagu@example.com
  to: ops@example.com
handler_on:
  exit:
    run: echo exit
  failure:
    run: echo failure
steps:
  - name: sync data
    id: sync
    run: ./sync.sh
  - name: report
    depends: sync
    run: echo report
`, caseOptions{mocks: map[string]stepmock.Step{"sync data": {Results: []stepmock.Result{{ExitCode: 1}, {}}}}})
	require.NoError(t, err)

	expect := tc.expectFromRun(status)
	assert.Equal(t, dagtest.Expect{
		Status:   "succeeded",
		Attempts: new(2),
		Steps: []dagtest.StepExpect{
			{Key: "sync", Step: "sync data", Status: "succeeded", Attempts: new(2)},
			{Key: "report", Step: "report", Status: "succeeded", Attempts: new(1)},
			{Key: "handler_on.failure", Handler: ir.HandlerOnFailure, Status: "not_started", Attempts: new(0)},
			{Key: "handler_on.exit", Handler: ir.HandlerOnExit, Status: "succeeded", Attempts: new(2)},
		},
		Notifications: []dagtest.Notification{{Kind: "mail_on.success", To: []string{"ops@example.com"}}},
	}, expect)
	assert.Empty(t, tc.check(expect, status))
}

// A text output matches a string exactly and any other value by the JSON
// rule against its text; a type: json output always matches by the JSON
// rule, which ignores unlisted keys and compares arrays and numbers by
// value.
func TestCheckOutputs(t *testing.T) {
	t.Parallel()

	published := `{"files":"3","stats":"{\"files\":3,\"lines\":40,\"tags\":[\"a\",\"b\"]}","ratio":1.50,"note":"not json"}`
	status := &ir.DAGRunStatus{Status: ir.Succeeded, Nodes: []*ir.Node{{
		Step:             ir.Step{Name: "count"},
		Status:           ir.NodeSucceeded,
		DoneCount:        1,
		StepOutputsValue: &published,
	}}}
	tc := &testCase{ended: []*ir.DAGRunStatus{status}}
	stats := `{"files":3,"lines":40,"tags":["a","b"]}`

	for _, tt := range []struct {
		name   string
		output dagtest.OutputExpect
		// failure is the report line, or empty when the output matches.
		failure string
	}{
		{"TextExact", dagtest.OutputExpect{Name: "files", Value: "3"}, ""},
		{"TextDiffers", dagtest.OutputExpect{Name: "files", Value: "03"}, `step count output files: expected "03", got "3"`},
		{"TextAsJSON", dagtest.OutputExpect{Name: "files", Value: 3}, ""},
		{"Subset", dagtest.OutputExpect{Name: "stats", Value: map[string]any{"files": 3}, JSON: true}, ""},
		{"SubsetDiffers", dagtest.OutputExpect{Name: "stats", Value: map[string]any{"lines": 41}, JSON: true},
			`step count output stats: expected {"lines":41}, got ` + stats},
		{"ArrayExact", dagtest.OutputExpect{Name: "stats", Value: map[string]any{"tags": []any{"a"}}, JSON: true},
			`step count output stats: expected {"tags":["a"]}, got ` + stats},
		{"NumberByValue", dagtest.OutputExpect{Name: "ratio", Value: 1.5}, ""},
		{"TextNotJSON", dagtest.OutputExpect{Name: "note", Value: map[string]any{"a": 1}},
			`step count output note: expected {"a":1}, got "not json"`},
		{"NotPublished", dagtest.OutputExpect{Name: "missing", Value: "x"}, `step count output missing: expected "x", got nothing`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			failures := tc.check(dagtest.Expect{Steps: []dagtest.StepExpect{
				{Key: "count", Step: "count", Outputs: []dagtest.OutputExpect{tt.output}},
			}}, status)
			if tt.failure == "" {
				assert.Empty(t, failures)
				return
			}
			assert.Equal(t, []string{tt.failure}, failureLines(failures))
		})
	}
}

// Notifications pair without regard to order. An item without step or to
// may take any notification of its kind, so a stricter item still finds
// its own.
func TestCheckNotifications(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Steps: []ir.Step{{Name: "load data", ID: "load"}}}
	failure := agent.Notification{Kind: agent.NotificationMailOnFailure, To: []string{"a@example.com", "b@example.com"}}
	stepError := agent.Notification{Kind: agent.NotificationMailOnError, Step: "load data", To: []string{"a@example.com"}}
	sentLine := "mail_on_error step load to a@example.com; mail_on.failure to a@example.com, b@example.com"

	for _, tt := range []struct {
		name     string
		sent     []agent.Notification
		expected []dagtest.Notification
		// failure is the report line, or empty when the list holds.
		failure string
	}{
		{"AnyOrder", []agent.Notification{stepError, failure}, []dagtest.Notification{
			{Kind: "mail_on.failure", To: []string{"b@example.com", "a@example.com"}},
			{Kind: "mail_on_error", Step: "load data"},
		}, ""},
		{"LooseItemFirst", []agent.Notification{
			{Kind: agent.NotificationMailOnFailure, To: []string{"a@example.com"}},
			{Kind: agent.NotificationMailOnFailure, To: []string{"b@example.com"}},
		}, []dagtest.Notification{
			{Kind: "mail_on.failure"},
			{Kind: "mail_on.failure", To: []string{"a@example.com"}},
		}, ""},
		{"None", []agent.Notification{stepError, failure}, []dagtest.Notification{},
			"notifications: expected none, got " + sentLine},
		{"OtherRecipients", []agent.Notification{stepError, failure}, []dagtest.Notification{
			{Kind: "mail_on_error", Step: "load data"},
			{Kind: "mail_on.failure", To: []string{"a@example.com"}},
		}, "notifications: expected mail_on_error step load; mail_on.failure to a@example.com, got " + sentLine},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := &testCase{dag: dag, notifications: tt.sent}
			failures := tc.check(dagtest.Expect{Notifications: tt.expected}, &ir.DAGRunStatus{Status: ir.Failed})
			if tt.failure == "" {
				assert.Empty(t, failures)
				return
			}
			assert.Equal(t, []string{tt.failure}, failureLines(failures))
		})
	}
}

// calledWithDAG passes an upstream output, an env entry, and a secret into a
// command step and an action step. Its failure handler never runs in a run
// that succeeds.
const calledWithDAG = `secrets:
  - name: API_TOKEN
    provider: env
    key: DAGU_TEST_UNSET_CALLED_WITH_TOKEN
env:
  - REGION: eu-west-1
steps:
  - id: build
    run: ./build.sh
    outputs:
      - name: image_tag
  - id: deploy
    depends: build
    run: ./deploy.sh "${steps.build.outputs.image_tag}"
    env:
      - IMAGE_TAG: ${steps.build.outputs.image_tag}
  - id: notify
    depends: deploy
    action: http.request
    with:
      method: POST
      url: https://hooks.example.invalid/deploys
      headers:
        Authorization: Bearer ${API_TOKEN}
      body: deployed ${steps.build.outputs.image_tag}
handler_on:
  failure:
    run: ./rollback.sh
`

// calledWithMocks handle every source of calledWithDAG.
const calledWithMocks = `mocks:
  build:
    outputs:
      image_tag: v1.2.3
  deploy: {}
  notify: {}
  handler_on.failure: {}
`

// called_with checks what each mocked step would have been called with:
// the run text with the upstream output in it, the listed with inputs only,
// and the listed variables, a secret as its placeholder. Each mismatch is
// reported with its actual value, the secret masked, and a step that never
// ran fails its assertion.
func TestCheckCalledWith(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "release.yaml", calledWithDAG)
	testPath := filepath.Join(home, "dags", "release.test.yaml")
	require.NoError(t, os.WriteFile(testPath, []byte(calledWithMocks+`cases:
  - name: deploy takes the built tag
    expect:
      steps:
        deploy:
          called_with:
            run: ./deploy.sh "v1.2.3"
            env:
              IMAGE_TAG: v1.2.3
              REGION: eu-west-1
        notify:
          called_with:
            with:
              method: POST
              headers:
                Authorization: Bearer <secret:API_TOKEN>
  - name: the run contains and matches the tag
    expect:
      steps:
        deploy:
          called_with:
            run:
              contains: v1.2.3
              matches: 'deploy\.sh "v\d+\.\d+\.\d+"$'
  - name: every mismatch is reported
    mocks:
      build:
        outputs:
          image_tag: v1.2.4
    expect:
      steps:
        deploy:
          called_with:
            run:
              contains: v1.2.3
            env:
              IMAGE_TAG: v1.2.3
              DAGU_TEST_UNSET_CALLED_WITH_VAR: x
        notify:
          called_with:
            with:
              body: deployed v1.2.3
              headers:
                Authorization: Bearer other
              timeout: 30
        handler_on.failure:
          called_with:
            run: ./rollback.sh
`), 0o600))

	reported := testReportPath(testPath)
	stdout, err := runTestCommand(t, home, testPath)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitFailed, exit.code)
	assertInOrder(t, stdout,
		"PASS "+reported+": deploy takes the built tag (",
		"PASS "+reported+": the run contains and matches the tag (",
		"FAIL "+reported+": every mismatch is reported (",
		`  step deploy called_with.run: expected to contain "v1.2.3", got "./deploy.sh \"v1.2.4\""`+"\n"+
			`  step deploy called_with.env.IMAGE_TAG: expected "v1.2.3", got "v1.2.4"`+"\n"+
			`  step deploy called_with.env.DAGU_TEST_UNSET_CALLED_WITH_VAR: expected "x", got nothing`+"\n"+
			`  step notify called_with.with.body: expected "deployed v1.2.3", got "deployed v1.2.4"`+"\n"+
			`  step notify called_with.with.headers: expected {"Authorization":"Bearer other"}, got {"Authorization":"Bearer *******"}`+"\n"+
			`  step notify called_with.with.timeout: expected 30, got nothing`+"\n"+
			`  step handler_on.failure called_with: expected a call, got none`+"\n",
		"3 cases: 2 passed, 1 failed\n")

	stdout, err = runTestCommand(t, home, "--format", "json", "--run", "every mismatch", testPath)
	require.ErrorAs(t, err, &exit)
	var report struct {
		Cases []struct {
			Failures []map[string]any `json:"failures"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Cases, 1)
	failures := report.Cases[0].Failures
	require.Len(t, failures, 7)
	assert.Equal(t, map[string]any{
		"step": "deploy", "field": "called_with.run.contains", "expected": "v1.2.3", "actual": `./deploy.sh "v1.2.4"`,
	}, failures[0])
	assert.Equal(t, map[string]any{
		"step": "notify", "field": "called_with.with.headers",
		"expected": map[string]any{"Authorization": "Bearer other"},
		"actual":   map[string]any{"Authorization": "Bearer *******"},
	}, failures[4])
	assert.Equal(t, map[string]any{"step": "notify", "field": "called_with.with.timeout", "expected": float64(30)}, failures[5])
	assert.Equal(t, map[string]any{"step": "handler_on.failure", "field": "called_with"}, failures[6])
}

// A real step is checked against what it ran with, as a mocked one is.
func TestCheckCalledWithRealStep(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "greet.yaml", `params:
  - name: who
    default: world
steps:
  - id: greet
    run: echo "hello ${params.who}"
    env:
      - GREETING: hello
`)
	testPath := filepath.Join(home, "dags", "greet.test.yaml")
	require.NoError(t, os.WriteFile(testPath, []byte(`real: [greet]
cases:
  - name: greets the param
    params:
      who: dagu
    expect:
      steps:
        greet:
          status: succeeded
          called_with:
            run: echo "hello dagu"
            env:
              GREETING: hello
`), 0o600))

	stdout, err := runTestCommand(t, home, testPath)
	require.NoError(t, err, stdout)
	assert.Contains(t, stdout, "PASS "+testReportPath(testPath)+": greets the param (")
}
