// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildValidationIssuesEmpty(t *testing.T) {
	issues := buildValidationIssues(nil, nil, nil)
	assert.NotNil(t, issues)
	assert.Empty(t, issues)
}

func TestBuildValidationIssuesPosition(t *testing.T) {
	loadErr := ir.ErrorList{
		errors.New("[3:5] bad key\n  1 | a"),
		errors.New("no position"),
	}
	issues := buildValidationIssues(loadErr, nil, nil)
	require.Len(t, issues, 2)

	assert.Equal(t, api.ValidationIssueSeverityError, issues[0].Severity)
	assert.Equal(t, api.ValidationIssueSourceLoad, issues[0].Source)
	assert.Equal(t, loadErr[0].Error(), issues[0].Message)
	require.NotNil(t, issues[0].Line)
	require.NotNil(t, issues[0].Column)
	assert.Equal(t, 3, *issues[0].Line)
	assert.Equal(t, 5, *issues[0].Column)

	assert.Nil(t, issues[1].Line)
	assert.Nil(t, issues[1].Column)
}

func TestBuildValidationIssuesStep(t *testing.T) {
	dag := &ir.DAG{
		Steps: []ir.Step{{Name: "a"}, {Name: "b"}},
		BuildErrors: []error{
			ir.NewValidationError("steps", "b", errors.New("bad step")),
			ir.NewValidationError("params", "x", errors.New("bad param")),
		},
		BuildWarnings: []string{"careful"},
	}
	issues := buildValidationIssues(nil, dag, nil)
	require.Len(t, issues, 3)

	assert.Equal(t, api.ValidationIssueSourceBuild, issues[0].Source)
	assert.Equal(t, dag.BuildErrors[0].Error(), issues[0].Message)
	require.NotNil(t, issues[0].StepName)
	assert.Equal(t, "b", *issues[0].StepName)
	require.NotNil(t, issues[0].StepPath)
	assert.Equal(t, "steps[1]", *issues[0].StepPath)

	assert.Nil(t, issues[1].StepName)
	assert.Nil(t, issues[1].StepPath)
	require.NotNil(t, issues[1].FieldPath)
	assert.Equal(t, "params", *issues[1].FieldPath)

	assert.Equal(t, api.ValidationIssueSeverityWarning, issues[2].Severity)
	assert.Equal(t, api.ValidationIssueSourceWarning, issues[2].Source)
	assert.Equal(t, "careful", issues[2].Message)
}

func TestBuildValidationIssuesNotice(t *testing.T) {
	issues := buildValidationIssues(nil, nil, []cmnvalue.ValueReferenceNotice{{
		Message:   "unresolved",
		FieldPath: "consts.image",
		Reason:    cmnvalue.ValueReferenceReasonUnknownStepID,
	}})
	require.Len(t, issues, 1)
	assert.Equal(t, api.ValidationIssueSeverityNotice, issues[0].Severity)
	assert.Equal(t, api.ValidationIssueSourceValueReference, issues[0].Source)
	assert.Equal(t, "unresolved", issues[0].Message)
	require.NotNil(t, issues[0].FieldPath)
	assert.Equal(t, "consts.image", *issues[0].FieldPath)
	require.NotNil(t, issues[0].Reason)
	assert.Equal(t, "unknown_step_id", *issues[0].Reason)
}

func TestErrorPaths(t *testing.T) {
	leaf := errors.New("leaf")
	tests := []struct {
		name string
		err  error
		want []errorPath
	}{
		{
			name: "plain",
			err:  leaf,
			want: []errorPath{{detail: "leaf"}},
		},
		{
			name: "nested validation errors",
			err: ir.NewValidationError("steps", nil,
				ir.NewValidationError("with", map[string]any{"a": 1}, leaf)),
			want: []errorPath{{
				fields: []string{"steps", "with"},
				values: []any{nil, map[string]any{"a": 1}},
				detail: "leaf",
			}},
		},
		{
			name: "list inside validation error",
			err: ir.NewValidationError("steps", nil, ir.ErrorList{
				ir.NewValidationError("parallel", 3, errors.New("one")),
				ir.NewValidationError("foreach", "x", errors.New("two")),
			}),
			want: []errorPath{
				{fields: []string{"steps", "parallel"}, values: []any{nil, 3}, detail: "one"},
				{fields: []string{"steps", "foreach"}, values: []any{nil, "x"}, detail: "two"},
			},
		},
		{
			name: "wrap keeps the outer text",
			err: ir.NewValidationError("depends", []string{"a"},
				fmt.Errorf("step 'b': %w", leaf)),
			want: []errorPath{{
				fields: []string{"depends"},
				values: []any{[]string{"a"}},
				detail: "step 'b': leaf",
			}},
		},
		{
			name: "join",
			err:  errors.Join(errors.New("x"), fmt.Errorf("y: %w", leaf)),
			want: []errorPath{{detail: "x"}, {detail: "y: leaf"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, errorPaths(tt.err))
		})
	}
	assert.Empty(t, errorPaths(nil))
}

// loadIssues loads source like the validate endpoint and returns its issues.
func loadIssues(t *testing.T, source string) []api.ValidationIssue {
	t.Helper()
	dag, err := spec.LoadYAML(context.Background(), []byte(source),
		spec.WithName("t"), spec.WithAllowBuildErrors(), spec.WithoutEval())
	issues := buildValidationIssues(err, dag, nil)
	for _, issue := range issues {
		require.NotEmpty(t, issue.Message)
	}
	return issues
}

// wantIssue is the expected location of an issue. An empty field means nil.
type wantIssue struct {
	step, stepPath, field string
	line, column          int
}

func assertIssue(t *testing.T, want wantIssue, got api.ValidationIssue) {
	t.Helper()
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	num := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}
	assert.Equal(t, want.step, str(got.StepName), "stepName")
	assert.Equal(t, want.stepPath, str(got.StepPath), "stepPath")
	assert.Equal(t, want.field, str(got.FieldPath), "fieldPath")
	assert.Equal(t, want.line, num(got.Line), "line")
	assert.Equal(t, want.column, num(got.Column), "column")
}

// Each row loads a real spec and checks the location of its first issue.
func TestIssueAttribution(t *testing.T) {
	tests := []struct {
		name   string
		source string
		count  int
		issue  int // index of the issue to check
		want   wantIssue
	}{
		{
			name:   "yaml syntax error",
			source: "steps:\n  - name: a\n   command: echo\n  x: [\n",
			count:  1,
			want:   wantIssue{line: 3, column: 4},
		},
		{
			name:   "yaml error inside a wrapped decode error",
			source: "steps:\n  - name: a\n    command: echo\nsteps:\n  - name: b\n    command: echo\n",
			count:  1,
			want:   wantIssue{line: 4, column: 1},
		},
		{
			name:   "duplicate step id points to the later step",
			source: "steps:\n  - name: a\n    id: x\n    command: echo\n  - name: b\n    id: x\n    command: echo\n",
			count:  1,
			want:   wantIssue{step: "b", stepPath: "steps[1]", field: "steps[1]"},
		},
		{
			name:   "duplicate step name points to the later step",
			source: "steps:\n  - name: a\n    command: echo\n  - name: a\n    command: echo\n",
			count:  1,
			want:   wantIssue{step: "a", stepPath: "steps[1]", field: "steps[1]"},
		},
		{
			name:   "double quoted step name",
			source: "type: agent\nsteps:\n  - name: a\n    command: echo\n    depends: [x]\n",
			count:  2,
			issue:  1,
			want:   wantIssue{step: "a", stepPath: "steps[0]", field: "steps[0].depends"},
		},
		{
			name:   "single quoted step name located in the raw YAML",
			source: "type: chain\nsteps:\n  - name: a\n    command: echo\n  - name: b\n    command: echo\n    depends: [a]\n",
			count:  1,
			want:   wantIssue{step: "b", stepPath: "steps[1]", field: "steps[1].depends"},
		},
		{
			name:   "missing dependency belongs to the owner step",
			source: "steps:\n  - name: a\n    command: echo\n  - name: b\n    command: echo\n    depends: zz\n",
			count:  1,
			want:   wantIssue{step: "b", stepPath: "steps[1]", field: "steps[1].depends"},
		},
		{
			name:   "longest step name wins",
			source: "steps:\n  - name: build\n    command: echo\n  - name: build app\n    command: echo\n    depends: zz\n",
			count:  1,
			want:   wantIssue{step: "build app", stepPath: "steps[1]", field: "steps[1].depends"},
		},
		{
			name:   "step in a nested group",
			source: "steps:\n  - name: a\n    command: echo\n  - - name: b\n      command: echo\n    - name: c\n      command: echo\n      depends: [qq]\n",
			count:  1,
			want:   wantIssue{step: "c", stepPath: "steps[2]", field: "steps[2].depends"},
		},
		{
			name:   "step of the map form",
			source: "steps:\n  s1:\n    command: echo\n  s2:\n    command: echo\n    depends: [qq]\n",
			count:  1,
			want:   wantIssue{step: "s2", stepPath: "steps[1]", field: "steps[1].depends"},
		},
		{
			name:   "generated step name",
			source: "steps:\n  - command: echo\n    depends: [x]\n",
			count:  1,
			want:   wantIssue{step: "cmd_1", stepPath: "steps[0]", field: "steps[0].depends"},
		},
		{
			name:   "router target that does not exist",
			source: "steps:\n  - name: r\n    type: router\n    value: x\n    routes:\n      a: [nope]\n",
			count:  1,
			want:   wantIssue{step: "r", stepPath: "steps[0]", field: "steps[0].routes"},
		},
		{
			name:   "router in a chain",
			source: "type: chain\nsteps:\n  - name: r\n    type: router\n    value: x\n    routes:\n      a: [s]\n  - name: s\n    command: echo\n",
			count:  1,
			want:   wantIssue{step: "r", stepPath: "steps[0]", field: "steps[0].type"},
		},
		{
			name: "inferred dependency cycle",
			source: "type: graph\nsteps:\n  - name: a\n    id: ida\n    command: echo ${steps.idb.outputs.x}\n" +
				"  - name: b\n    id: idb\n    command: echo ${steps.ida.outputs.x}\n",
			count: 1, // one errors entry holds both fields
			want:  wantIssue{step: "b", stepPath: "steps[1]", field: "steps[1].run[0].cmd_with_args"},
		},
		{
			name:   "unknown key of a step",
			source: "steps:\n  - name: a\n    command: echo\n  - name: b\n    runx: echo\n",
			count:  1,
			want:   wantIssue{step: "b", stepPath: "steps[1]", field: "steps[1]"},
		},
		{
			name:   "invalid with of a step",
			source: "steps:\n  - name: a\n    command: echo\n  - name: b\n    action: http.request\n    with:\n      url: 3\n      bogus: x\n",
			count:  1,
			want:   wantIssue{step: "b", stepPath: "steps[1]", field: "steps[1].with"},
		},
		{
			name: "input of a custom action",
			source: "step_types:\n  greet:\n    type: command\n    input_schema:\n      type: object\n      additionalProperties: false\n" +
				"      properties:\n        repeat:\n          type: integer\n    template:\n      command: echo hi\n" +
				"steps:\n  - name: x\n    command: echo\n  - name: g\n    type: greet\n    with:\n      repeat: abc\n",
			count: 1,
			want:  wantIssue{step: "g", stepPath: "steps[1]", field: "steps[1].with.repeat"},
		},
		{
			name: "unexpected input of a custom action",
			source: "step_types:\n  greet:\n    type: command\n    input_schema:\n      type: object\n      additionalProperties: false\n" +
				"      properties:\n        message:\n          type: string\n    template:\n      command: echo hi\n" +
				"steps:\n  - name: g\n    type: greet\n    with:\n      bogus: abc\n",
			count: 1,
			want:  wantIssue{step: "g", stepPath: "steps[0]", field: "steps[0].with.bogus"},
		},
		{
			name:   "decode error in the map form",
			source: "steps:\n  s1:\n    command: echo\n  s2:\n    runx: echo\n",
			count:  1,
			want:   wantIssue{step: "s2", stepPath: "steps[1]", field: "steps[1]"},
		},
		{
			name:   "decode error of a field in the map form",
			source: "steps:\n  s1:\n    command: echo\n  s2:\n    depends: 3\n    command: echo\n",
			count:  1,
			want:   wantIssue{step: "s2", stepPath: "steps[1]", field: "steps[1]"},
		},
		{
			name:   "invalid handler found by its value",
			source: "steps:\n  - name: a\n    command: echo\nhandler_on:\n  failure:\n    type: bogus\n",
			count:  1,
			want:   wantIssue{step: "onFailure", stepPath: "handler_on.failure", field: "handler_on.failure.type"},
		},
		{
			name:   "unknown key of a handler",
			source: "steps:\n  - name: a\n    command: echo\nhandler_on:\n  failure:\n    runx: 1\n",
			count:  1,
			want:   wantIssue{step: "onFailure", stepPath: "handler_on.failure", field: "handler_on.failure"},
		},
		{
			name: "handler by its name",
			source: "type: build\nworking_dir: .\nhandler_on:\n  success:\n    id: cleanup\n    run: echo cleanup\n" +
				"    inputs:\n      - name: source\n        path: source.txt\nsteps:\n  - name: build\n    run: echo build\n",
			count: 1,
			want:  wantIssue{step: "onSuccess", stepPath: "handler_on.success", field: "handler_on.success"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := loadIssues(t, tt.source)
			require.Len(t, issues, tt.count)
			assertIssue(t, tt.want, issues[tt.issue])
		})
	}
}

func TestIssueHandlerPaths(t *testing.T) {
	handler := func(name string) *ir.Step { return &ir.Step{Name: name, ID: "id_" + name} }
	dag := &ir.DAG{
		Steps: []ir.Step{{Name: "a"}},
		HandlerOn: ir.HandlerOn{
			Init:    handler("onInit"),
			Success: handler("onSuccess"),
			Failure: handler("onFailure"),
			Abort:   handler("onAbort"),
			Exit:    handler("onExit"),
			Wait:    handler("onWait"),
		},
	}
	for event, name := range map[string]string{
		"init": "onInit", "success": "onSuccess", "failure": "onFailure",
		"abort": "onAbort", "exit": "onExit", "wait": "onWait",
	} {
		path := "handler_on." + event
		want := wantIssue{step: name, stepPath: path, field: path}
		byName := ir.NewValidationError("handler_on", name, errors.New("bad"))
		byID := ir.NewValidationError("steps", "id_"+name, errors.New("bad"))
		byText := fmt.Errorf("step '%s': bad", name)
		byField := ir.NewValidationError("handler_on."+event+".type", nil, errors.New("bad"))
		for _, err := range []error{byName, byID, byText, byField} {
			issues := buildValidationIssues(nil, &ir.DAG{
				Steps: dag.Steps, HandlerOn: dag.HandlerOn, BuildErrors: []error{err},
			}, nil)
			require.Len(t, issues, 1)
			want := want
			if err == byField {
				want.field = path + ".type"
			}
			assertIssue(t, want, issues[0])
		}
	}
}

func TestFieldPathOf(t *testing.T) {
	tests := []struct {
		name     string
		fields   []string
		stepPath string
		want     string
	}{
		{"dag field", []string{"params"}, "", "params"},
		{"dag field with segments", []string{"consts.image"}, "", "consts.image"},
		{"step field", []string{"steps", "with"}, "steps[2]", "steps[2].with"},
		{"dotted step field", []string{"steps", "retry_policy.interval_sec"}, "steps[0]", "steps[0].retry_policy.interval_sec"},
		{"wrappers only", []string{"steps", "steps"}, "steps[1]", "steps[1]"},
		{"handler field", []string{"handlers", "type"}, "handler_on.failure", "handler_on.failure.type"},
		{"absolute path", []string{"steps[1].run[0].command"}, "steps[1]", "steps[1].run[0].command"},
		{"step field of an unknown step", []string{"steps", "with"}, "", ""},
		{"ambiguous field of an unknown step", []string{"container"}, "", ""},
		{"no field", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fieldPathOf(errorPath{fields: tt.fields, values: make([]any, len(tt.fields))}, tt.stepPath)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRawDoc(t *testing.T) {
	t.Run("array form with groups and shorthand", func(t *testing.T) {
		raw := parseRawDoc([]byte("steps:\n  - echo hi\n  - - name: b\n      command: x\n    - name: c\n      command: y\n  - name: d\n    command: z\n"))
		require.NotNil(t, raw)
		var got []string
		for _, r := range raw.steps {
			got = append(got, r.path+"="+r.name)
		}
		assert.Equal(t, []string{"steps[0]=", "steps[1]=b", "steps[2]=c", "steps[3]=d"}, got)
	})
	t.Run("map form sorts by key", func(t *testing.T) {
		raw := parseRawDoc([]byte("steps:\n  zeta:\n    command: x\n  alpha:\n    command: y\n"))
		require.NotNil(t, raw)
		require.Len(t, raw.steps, 2)
		assert.Equal(t, "alpha", raw.steps[0].name)
		assert.Equal(t, "steps[1]", raw.steps[1].path)
	})
	t.Run("only the first document", func(t *testing.T) {
		raw := parseRawDoc([]byte("steps:\n  - name: a\n    command: x\n---\nname: sub\nsteps:\n  - name: z\n    command: y\n"))
		require.NotNil(t, raw)
		require.Len(t, raw.steps, 1)
		assert.Equal(t, "a", raw.steps[0].name)
	})
	t.Run("no usable document", func(t *testing.T) {
		assert.Nil(t, parseRawDoc(nil))
		assert.Nil(t, parseRawDoc([]byte("steps: [\n")))
	})
	t.Run("equal steps do not match", func(t *testing.T) {
		raw := parseRawDoc([]byte("steps:\n  - command: x\n  - command: x\n"))
		require.NotNil(t, raw)
		path := errorPath{fields: []string{"steps"}, values: []any{map[string]any{"command": "x"}}}
		_, ok := raw.locate(path)
		assert.False(t, ok)
	})
}

// A DAG without YAML data and without steps still gives the issue.
func TestIssueWithoutYAMLData(t *testing.T) {
	dag := &ir.DAG{BuildErrors: []error{
		ir.NewValidationError("steps", nil, ir.NewValidationError("with", map[string]any{"a": 1}, errors.New("bad"))),
		fmt.Errorf("step 'ghost': bad"),
	}}
	issues := buildValidationIssues(nil, dag, nil)
	require.Len(t, issues, 2)
	assertIssue(t, wantIssue{}, issues[0])
	assertIssue(t, wantIssue{step: "ghost"}, issues[1])
}

// The token of a parser error gives the position even when the message has no prefix.
func TestErrorPositionFromToken(t *testing.T) {
	err := fmt.Errorf("document 0: %w", &yaml.SyntaxError{
		Message: "bad",
		Token:   &token.Token{Position: &token.Position{Line: 7, Column: 0}},
	})
	line, column, ok := errorPosition(err)
	require.True(t, ok)
	assert.Equal(t, 7, line)
	assert.Equal(t, 1, column)

	_, _, ok = errorPosition(errors.New("no position"))
	assert.False(t, ok)
}

func TestSchemaFieldSuffix(t *testing.T) {
	tests := []struct {
		name, detail, want string
	}{
		{"property", `invalid "g" input: validating root: validating /properties/repeat: type: abc`, "repeat"},
		{"nested properties", "validating root: validating /properties/a/properties/b: type", "a.b"},
		{"item of a list", "validating root: validating /properties/a/items: type", ""},
		{"additional property", `validating root: unexpected additional properties ["bogus"]`, "bogus"},
		{"several additional properties", `validating root: unexpected additional properties ["a" "b"]`, ""},
		{"root", "validating root: missing properties", ""},
		{"other", "something else", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, schemaFieldSuffix(tt.detail))
		})
	}
}

// A notice of a step field gets the step path of its field path.
func TestIssueNoticeStep(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   wantIssue
	}{
		{
			name:   "step field",
			source: "steps:\n  - name: a\n    command: echo\n  - name: b\n    command: echo ${steps.nope.outputs.x}\n",
			want:   wantIssue{step: "b", stepPath: "steps[1]"},
		},
		{
			name:   "handler field",
			source: "steps:\n  - name: a\n    command: echo\nhandler_on:\n  failure:\n    command: echo ${steps.nope.outputs.x}\n",
			want:   wantIssue{step: "onFailure", stepPath: "handler_on.failure"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := spec.LoadYAMLWithResult(context.Background(), []byte(tt.source),
				spec.WithName("t"), spec.WithAllowBuildErrors(), spec.WithoutEval())
			require.NoError(t, err)
			require.NotEmpty(t, result.ValueReferenceNotices)
			issues := buildValidationIssues(nil, result.DAG, result.ValueReferenceNotices)
			require.Len(t, issues, 1)
			require.NotNil(t, issues[0].FieldPath)
			assert.Contains(t, *issues[0].FieldPath, tt.want.stepPath)
			tt.want.field = *issues[0].FieldPath
			assertIssue(t, tt.want, issues[0])
		})
	}

	t.Run("dag field has no step", func(t *testing.T) {
		issues := buildValidationIssues(nil, nil, []cmnvalue.ValueReferenceNotice{{Message: "m", FieldPath: "consts.image"}})
		require.Len(t, issues, 1)
		assertIssue(t, wantIssue{field: "consts.image"}, issues[0])
	})
}
