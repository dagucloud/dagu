// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFileInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file string
		want []string
	}{
		{
			file: "unknown.test.yaml",
			want: []string{
				"schedule: is not a field of a test file",
				"mocks.check.retries: is not a field of a test file",
				"mocks.check.review.comment: is not a field of a test file",
				"mocks.other.results[0].precondition: is not a field of a test file",
				"cases[0].timeout: is not a field of a test file",
				"cases[0].expect.duration: is not a field of a test file",
				"cases[0].expect.steps.check.exit_code: is not a field of a test file",
				"cases[0].expect.steps.check.stdout.equals: is not a field of a test file",
				"cases[0].expect.notifications[0].subject: is not a field of a test file",
			},
		},
		{
			file: "types.test.yaml",
			want: []string{
				"params.list: must be a scalar",
				"params.count: is not a valid !!int",
				"env: must be a map",
				"real: must be a list",
				"mocks.check.exit_code: must be an integer",
				"mocks.check.stdout: must be a string",
				"mocks.check.results: must be a list",
				"mocks.check.review: must be a map",
				"cases[0]: must be a map",
				"cases[1].scheduled_at: is not a valid !!timestamp",
				"cases[1].expect.attempts: must be an integer",
				"cases[1].expect.steps.check: must be a status token or a map",
			},
		},
		{file: "no_cases.test.yaml", want: []string{"cases: is required"}},
		{file: "empty_cases.test.yaml", want: []string{"cases: must list at least one case"}},
		{
			file: "case_fields.test.yaml",
			want: []string{
				"cases[0].name: is required and must not be empty",
				"cases[2].expect: is required",
				`cases[2].name: "twice" names another case of the file`,
				"cases[3].name: is required and must not be empty",
			},
		},
		{
			file: "duplicate.test.yaml",
			want: []string{
				"mocks.check: appears more than once",
				"mocks.twice.<<: appears more than once; merge a list of maps instead",
			},
		},
		{
			file: "smoke.test.yaml",
			want: []string{"reads as a DAG, with steps and no cases; rename it so its name does not end in .test.yaml or .test.yml"},
		},
		{file: "list.test.yaml", want: []string{"the top level must be a map"}},
		{file: "syntax.test.yaml", want: []string{"not valid YAML: yaml: line 1: did not find expected node content"}},
		{file: "two_docs.test.yaml", want: []string{"holds more than one YAML document"}},
		{file: "alias_loop.test.yaml", want: []string{`not valid YAML: anchor "loop" holds an alias of itself`}},
		{file: "alias_bomb.test.yaml", want: []string{"not valid YAML: its aliases expand it too far"}},
		{
			file: "orphan.test.yaml",
			want: []string{"no DAG file " + testFilePath("invalid/orphan.yaml") + " or " + testFilePath("invalid/orphan.yml") + " beside the test file; set dag"},
		},
		{file: "names_test_file.test.yaml", want: []string{"dag: names a test file, not a DAG"}},
		{file: "missing_dag.test.yaml", want: []string{"dag: DAG file " + testFilePath("invalid/nowhere.yaml") + " does not exist"}},
		{
			file: "broken.test.yaml",
			want: []string{"the DAG fails to load: failed to process document 0: failed to build DAG: field 'depends': step a depends on non-existent step missing (value: missing)"},
		},
		{
			file: "compile.test.yaml",
			want: []string{"the DAG is a type: build DAG, whose path outputs a test cannot mock (Spec 033)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()

			_, err := dagtest.LoadFile(context.Background(), testFilePath("invalid/"+tt.file))
			var invalid *dagtest.InvalidFileError
			require.ErrorAs(t, err, &invalid)
			var got []string
			for _, finding := range invalid.Errors {
				got = append(got, finding.String())
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoadFileDAGPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "YAMLBeforeYML", file: "infer/both.test.yaml", want: "infer/both.yaml"},
		{name: "YMLWithoutYAML", file: "infer/short.test.yml", want: "infer/short.yml"},
		{name: "RelativeToTestFile", file: "infer/elsewhere.test.yaml", want: "invalid/noop.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file := loadTestFile(t, tt.file)
			assert.Equal(t, testFilePath(tt.want), file.DAGPath)
		})
	}
}

// Each case of release.test.yaml changes one thing in a file whose own
// fields handle every source of release.yaml.
func TestLoadFileCaseErrors(t *testing.T) {
	t.Parallel()

	want := map[string][]string{
		"valid": nil,
		"unknown keys": {
			"mocks.deplooy: names no step or handler of the DAG",
			"mocks.handler_on.success: names no step or handler of the DAG",
			"mocks.onFailure: names no step or handler of the DAG",
		},
		"one step twice": {"mocks.build image: names the same step as mocks.build"},
		"inline fields and results": {
			"mocks.child.results: must hold at least one result",
			"mocks.untitled: sets results together with inline result fields",
		},
		"missing outputs": {"mocks.build.outputs: leaves out declared outputs image_tag, manifest"},
		"wrong outputs": {
			"mocks.build.outputs.extra: is not an output the step declares",
			"mocks.build.outputs.image_tag: must be a string, as the step declares it with type string",
		},
		"outputs per result": {
			"mocks.build.results[0].outputs.image_tag: must be a string, as the step declares it with type string",
			"mocks.build.results[1].outputs: leaves out declared outputs manifest",
		},
		"outputs without id": {
			"mocks.untitled.outputs: the step has no id, so no reference can read its outputs; give the step an id",
		},
		"runtime outputs": {"mocks.child.outputs.bad-name: is not a valid output name"},
		// Field errors come first, sorted by label, then the unhandled
		// sources of the fail-closed rule.
		"real steps": {
			"mocks.build: sets a result field on a step that runs for real; set only precondition and review",
			`real[0]: "fanout" is a foreach step, which cannot run for real; mock it`,
			`real[2]: "deplooy" names no step or handler of the DAG`,
			`real[4]: "gate" names the same step as real[3]`,
			"secret API_TOKEN: a real step may read it; give it a value under secrets",
			"step fanout, body step one: command has side effects; mock the whole foreach step",
		},
		"human task result": {
			"mocks.sign_off: sets a result field on a human.task step, which has no action to replace",
		},
		"fields without a source": {
			"mocks.route.precondition: the step has no precondition that runs a command",
			"mocks.route.results[0].condition: the step has no repeat condition that runs a command",
			"mocks.route.review: the step does not wait for a person",
		},
		"bad values": {
			`mocks.gate.precondition: "maybe" must be met or not_met`,
			`mocks.poll.results[0].condition: "sometimes" must be met or not_met`,
			`mocks.ship.review.decision: "later" must be approve or reject`,
			"precondition: the DAG has no precondition that runs a command",
			`trigger: "subdag" is not a trigger type a case can set; use manual, scheduler, webhook, retry, catchup, restart, backfill, or file`,
		},
		"review values": {
			"mocks.ship.review.values: leaves out required inputs ticket",
			"mocks.ship.review.values.links: must be a scalar",
			"mocks.sign_off.review.decision: a human.task cannot be rejected; approve it with values",
		},
		"default mock outputs": {
			"default_mock.results[0].outputs: the default mock sets no outputs; mock the step under mocks to set them",
		},
		"bad expect": {
			"expect.attempts: must not be negative",
			`expect.notifications[0].kind: "mail" must be one of mail_on.failure, mail_on.success, mail_on.wait, mail_on_error`,
			"expect.notifications[1].step: is valid only with kind mail_on_error",
			`expect.notifications[2].step: "nope" names no step or handler of the DAG`,
			`expect.status: "done" must be one of succeeded, failed, aborted, partially_succeeded, rejected`,
			"expect.steps.build image: names the same step as expect.steps.build",
			"expect.steps.build.outputs.nope: is not an output the step declares",
			`expect.steps.build.status: "skippedd" must be one of succeeded, failed, aborted, partially_succeeded, rejected, skipped, not_started`,
			"expect.steps.deplooy: names no step or handler of the DAG",
			"expect.steps.gate: holds no assertion",
			"expect.steps.route.attempts: must not be negative",
			"expect.steps.route.stdout.matches: is not a valid RE2 expression: error parsing regexp: missing closing ): `(`",
		},
		"empty expect": {"expect: holds no assertion"},
		"bad called_with": {
			"expect.steps.build.called_with.with: the step runs a command, which takes no with; assert run",
			"expect.steps.child.called_with.env: holds no assertion",
			"expect.steps.child.called_with.with: holds no assertion",
			"expect.steps.fanout.called_with: a foreach step calls only its body steps, which have no step key",
			"expect.steps.gate.called_with.run: must set contains, matches, or both",
			"expect.steps.poll.called_with.run.matches: is not a valid RE2 expression: error parsing regexp: missing closing ): `(`",
			"expect.steps.route.called_with.run: the step runs action router.route, which takes no run; assert with",
			"expect.steps.ship.called_with: holds no assertion",
			"expect.steps.sign_off.called_with: a human.task step has no action to call",
		},
	}

	file := loadTestFile(t, "release.test.yaml")
	require.Len(t, file.Cases, len(want))
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, want[c.Name], findingStrings(c.Errors))
		})
	}
}

// Loading the tested DAG runs no eval, also when a worker_selector reads the
// param, so the eval still needs a value from the test. The eval would
// create the marker file.
func TestLoadFileRunsNoEval(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "eval-ran")
	t.Setenv("DAGU_TEST_EVAL_MARKER", marker)

	c := loadCase(t, "selector.test.yaml", "the eval waits for a test value")
	assert.NoFileExists(t, marker)
	assert.Equal(t, []string{"param region: eval runs a command; give it a value under params"}, findingStrings(c.Errors))
}

// A secrets key is checked against the DAG in every case of the file.
func TestLoadFileUnknownSecret(t *testing.T) {
	t.Parallel()

	c := loadCase(t, "secrets.test.yaml", "a secrets key names no secret")
	assert.Equal(t, []string{"secrets.NOPE: names no DAG secret or SSH credential ref"}, findingStrings(c.Errors))
}

func TestLoadFileMerge(t *testing.T) {
	t.Parallel()

	c := loadCase(t, "merge.test.yaml", "merged")
	require.Empty(t, c.Errors)

	// Params and env merge by name in the order the names first appear, as
	// the scalars' text.
	assert.Equal(t, []string{"env=prod", "stamp=2026-11-23", "extra=0x10"}, c.Params)
	assert.Equal(t, []string{"REGION=us-east-1", "TIER=silver"}, c.Env)
	assert.Equal(t, map[string]string{"API_TOKEN": "file-token"}, c.Secrets)
	assert.Equal(t, "2026-11-30T09:00:00Z", c.ScheduledAt)
	assert.Equal(t, ir.TriggerTypeRestart, c.Trigger)
	assert.Nil(t, c.Precondition)

	assert.Equal(t, map[string]dagtest.Script{
		"build image": {ReplacesAction: true, Results: []dagtest.Result{{ExitCode: 1, Stderr: "boom"}}},
		"gate":        {ReplacesAction: true, Precondition: new(true)},
		"poll": {ReplacesAction: true, Results: []dagtest.Result{
			{ExitCode: 1, Condition: new(true)},
			{Condition: new(false)},
		}},
		"route": {Real: true},
		// An approval input is its text as written.
		"ship": {ReplacesAction: true, Review: &dagtest.Review{Values: map[string]any{"ticket": "0042"}}},
		// A human.task has no action to replace.
		"sign_off": {Review: &dagtest.Review{Values: map[string]any{"reason": "looks good"}}},
		"fanout":   {ReplacesAction: true},
		"child":    {ReplacesAction: true, Results: []dagtest.Result{{Outputs: map[string]any{"revision": 7}}}},
		"untitled": {Real: true},
	}, c.Steps)
	assert.Equal(t, map[ir.HandlerType]dagtest.Script{ir.HandlerOnFailure: {ReplacesAction: true}}, c.Handlers)

	require.Len(t, c.Expect.Steps, 3)
	build := c.Expect.Steps[0]
	require.NotNil(t, build.Stderr)
	assert.Equal(t, "^bo+m$", build.Stderr.Matches.String())
	build.Stderr.Matches = nil
	assert.Equal(t, dagtest.Expect{
		Status:   "failed",
		Attempts: new(1),
		Steps: []dagtest.StepExpect{
			{
				Key: "build image", Step: "build image", Status: "failed", Attempts: new(1),
				// manifest is declared with type: json.
				Outputs: []dagtest.OutputExpect{
					{Name: "image_tag", Value: "v1"},
					{Name: "manifest", Value: map[string]any{"files": 3}, JSON: true},
				},
				Stderr: &dagtest.StreamExpect{Contains: new("boom")},
			},
			{Key: "route", Step: "route", Status: "skipped"},
			{Key: "handler_on.failure", Handler: ir.HandlerOnFailure, Status: "succeeded"},
		},
		Notifications: []dagtest.Notification{{Kind: "mail_on_error", Step: "build image", To: []string{"oncall@example.com"}}},
	}, c.Expect)
}

// The default mock applies to each step and handler with a source that the
// case does not name, with only the fields that handle its sources.
func TestLoadFileDefaultMock(t *testing.T) {
	t.Parallel()

	file := loadTestFile(t, "defaults.test.yaml")
	require.Len(t, file.Cases, 2)
	emptyOutputs := map[string]any{"image_tag": "", "manifest": nil}
	succeeds := []dagtest.Result{{}}

	full := file.Cases[0]
	assert.Empty(t, full.Errors)
	assert.Equal(t, map[string]dagtest.Script{
		"build": {DefaultMock: true, ReplacesAction: true, Results: []dagtest.Result{{Outputs: emptyOutputs}}},
		"tick":  {DefaultMock: true, ReplacesAction: true, Results: succeeds},
		"gate":  {DefaultMock: true, Precondition: new(true)},
		"child": {DefaultMock: true, ReplacesAction: true, Results: succeeds},
		"ship": {
			DefaultMock: true, ReplacesAction: true, Results: succeeds,
			Review: &dagtest.Review{Values: map[string]any{"ticket": "T-1"}},
		},
	}, full.Steps)
	assert.Equal(t, map[ir.HandlerType]dagtest.Script{
		ir.HandlerOnExit: {DefaultMock: true, ReplacesAction: true, Results: succeeds},
	}, full.Handlers)
	assert.Equal(t, []string{"build", "tick", "gate", "child", "ship", "handler_on.exit"}, full.DefaultMocked)

	// {} keeps side-effect-free actions and leaves conditions and waits
	// unhandled.
	empty := file.Cases[1]
	assert.Equal(t, []string{
		"step tick: repeat policy has no limit; mock it with a result or list it under real",
		"step gate: precondition runs a command; set precondition in its mock or list it under real",
		"step ship: approval waits for required values; set review in its mock",
	}, findingStrings(empty.Errors))
	assert.Equal(t, map[string]dagtest.Script{
		"build": {DefaultMock: true, ReplacesAction: true, Results: []dagtest.Result{{Outputs: emptyOutputs}}},
		"tick":  {DefaultMock: true},
		"gate":  {DefaultMock: true},
		"child": {DefaultMock: true, ReplacesAction: true},
		"ship":  {DefaultMock: true, ReplacesAction: true},
	}, empty.Steps)
}

// The fix holds each step's current mock, by the key it is written under,
// with the fields its sources need: an inline mock becomes one result to
// take a stopping condition, a successful result publishes the known
// outputs of a step with an id, and a form value the form still rejects is
// marked. The default mock is the current mock where it applies.
func TestLoadFileFix(t *testing.T) {
	t.Parallel()

	file := loadTestFile(t, "fixes.test.yaml")
	require.Len(t, file.Cases, 2)
	review := `  form:
    review:
      decision: approve
      values:
        code: "" # replace with a value the form accepts
        tier: gold
        urgent: false
        count: 0
`
	assert.Equal(t, `mocks:
  build image:
    results:
      - stdout: built
        outputs:
          tag: v1
        condition: not_met
`+review+`  fanout: {}
  gate:
    precondition: met
  watch:
    results:
      - outputs:
          seen: ""
        condition: met
  publish:
    precondition: met
`, file.Cases[0].Fix)
	assert.Equal(t, `mocks:
  build:
    results:
      - exit_code: 1
        condition: not_met
      - outputs:
          tag: ""
        condition: not_met
`+review+`  gate:
    precondition: met
  watch:
    results:
      - exit_code: 1
        condition: met
      - outputs:
          seen: ""
        condition: met
  publish:
    results:
      - exit_code: 1
      - exit_code: 0
    precondition: met
`, file.Cases[1].Fix)
	assert.Empty(t, file.Cases[1].FileFix)
}

// A handler_on.<event> key names the handler of that event, and a step whose
// name equals the handler's runtime name takes only its own mock.
func TestLoadFileHandlerKeys(t *testing.T) {
	t.Parallel()

	c := loadCase(t, "handlers.test.yaml", "a handler key names the handler, not the step of its runtime name")
	require.Empty(t, c.Errors)
	assert.Equal(t, map[string]dagtest.Script{
		"onFailure": {ReplacesAction: true, Results: []dagtest.Result{{ExitCode: 1}}},
	}, c.Steps)
	assert.Equal(t, map[ir.HandlerType]dagtest.Script{
		ir.HandlerOnFailure: {ReplacesAction: true, Results: []dagtest.Result{{Stdout: "paged"}}},
	}, c.Handlers)
}

func testFilePath(name string) string {
	return filepath.Join("testdata", "testfiles", filepath.FromSlash(name))
}

func loadTestFile(t *testing.T, name string) *dagtest.File {
	t.Helper()

	file, err := dagtest.LoadFile(context.Background(), testFilePath(name))
	require.NoError(t, err)
	return file
}

func loadCase(t *testing.T, file, name string) dagtest.Case {
	t.Helper()

	for _, c := range loadTestFile(t, file).Cases {
		if c.Name == name {
			return c
		}
	}
	require.FailNow(t, "case not found", name)
	return dagtest.Case{}
}

func findingStrings(findings []dagtest.Finding) []string {
	var out []string
	for _, finding := range findings {
		out = append(out, finding.String())
	}
	return out
}
