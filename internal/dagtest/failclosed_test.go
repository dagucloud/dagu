// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
)

// replacing is a mock with one result, which replaces any action.
var replacing = dagtest.MockFields{Results: []dagtest.ResultFields{{}}}

func TestCheckFailClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dag      string
		base     string
		handling dagtest.Handling
		want     []string
	}{
		{
			name: "UnhandledActions",
			dag:  "actions.yaml",
			want: []string{
				"step build binary: command has side effects; mock it or list it under real",
				"step deploy: action ssh.run has side effects; mock it or list it under real",
				"step notify: action http.request has side effects; mock it or list it under real",
				"step announce: action mail.send has side effects; mock it or list it under real",
				"step summarize: action chat.completion has side effects; mock it or list it under real",
				"handler_on.failure: action mail.send has side effects; mock it or list it under real",
			},
		},
		{
			// {} replaces an action that has side effects, and a step may be
			// named by id.
			name: "EmptyMocksAndReal",
			dag:  "actions.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{
					"build":              {},
					"deploy":             {},
					"notify":             replacing,
					"handler_on.failure": {},
				},
				Real: []string{"announce", "summarize"},
			},
		},
		{
			// A mock with only step fields keeps a real action, so it does
			// not handle one with side effects unless the step is real.
			name: "StepFieldsReplaceUnlessReal",
			dag:  "actions.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{
					"build":              {Precondition: true},
					"deploy":             {Review: true},
					"notify":             {},
					"announce":           {},
					"summarize":          {},
					"route":              {Precondition: true},
					"handler_on.failure": {},
				},
				Real: []string{"build"},
			},
		},
		{
			name: "InlineRouterIsSideEffectFree",
			dag:  "inline_router.yaml",
		},
		{
			name: "ResultFieldsOnRealStep",
			dag:  "actions.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{
					"build":              replacing,
					"deploy":             {},
					"notify":             {},
					"announce":           {},
					"summarize":          {},
					"handler_on.failure": {},
				},
				Real: []string{"build"},
			},
			want: []string{
				"step build binary: mock sets a result field on a step under real; keep only precondition and review in its mock, or take it out of real",
			},
		},
		{
			name: "UnhandledConditions",
			dag:  "conditions.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{"poll": {}, "watch": {}},
			},
			want: []string{
				"param build_date: eval runs a command; give it a value under params",
				"preconditions: a precondition runs a command; set precondition in the case",
				"step gate: precondition runs a command; set precondition in its mock or list it under real",
				"step compare: precondition runs a command; set precondition in its mock or list it under real",
				"step poll: repeat condition runs a command; set condition in every result of its mock or list it under real",
			},
		},
		{
			name: "ScriptedConditions",
			dag:  "conditions.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{
					"gate":    {Precondition: true},
					"compare": {Precondition: true},
					"poll": {Results: []dagtest.ResultFields{
						{Condition: true}, {Condition: true},
					}},
					"watch": {},
				},
				Precondition: true,
				Params:       []string{"build_date"},
			},
		},
		{
			name: "ConditionMissingFromOneResult",
			dag:  "conditions.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{
					"poll": {Results: []dagtest.ResultFields{
						{Condition: true}, {},
					}},
					"watch": {},
				},
				Real:         []string{"gate", "compare"},
				Precondition: true,
				Params:       []string{"build_date"},
			},
			want: []string{
				"step poll: repeat condition runs a command; set condition in every result of its mock or list it under real",
			},
		},
		{
			name: "RealRunsConditions",
			dag:  "conditions.yaml",
			handling: dagtest.Handling{
				Real:         []string{"gate", "compare", "poll", "watch"},
				Precondition: true,
				Params:       []string{"build_date"},
			},
		},
		{
			// An eval without command substitution runs no command, so it
			// evaluates as in a real run and needs nothing from the case.
			name: "EvalsWithoutSubstitution",
			dag:  "evals.yaml",
			want: []string{
				"param stamp: eval runs a command; give it a value under params",
				"step weekday: precondition runs a command; set precondition in its mock or list it under real",
			},
		},
		{
			name: "HandledEvals",
			dag:  "evals.yaml",
			handling: dagtest.Handling{
				Mocks:  map[string]dagtest.MockFields{"weekday": {Precondition: true}},
				Params: []string{"stamp"},
			},
		},
		{
			name: "DAGLevelEvalWithSubstitution",
			dag:  "dag_eval.yaml",
			want: []string{
				"preconditions: a precondition runs a command; set precondition in the case",
			},
		},
		{
			name: "UnhandledOutputFiles",
			dag:  "outputs.yaml",
			want: []string{
				"step report: object-form output reads a file; mock it or list it under real",
				"step build: command has side effects; mock it or list it under real",
				"step build: object-form output reads a file; mock it or list it under real",
			},
		},
		{
			// Only a mock that replaces the action keeps the step from
			// reading its output file.
			name: "OutputFilesNeedReplacedAction",
			dag:  "outputs.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{
					"report": {Precondition: true},
					"build":  {},
				},
			},
			want: []string{
				"step report: object-form output reads a file; mock it or list it under real",
			},
		},
		{
			name: "HandledOutputFiles",
			dag:  "outputs.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{"report": replacing},
				Real:  []string{"build"},
			},
		},
		{
			name: "UnmockedForeachBody",
			dag:  "foreach.yaml",
			want: []string{
				"step upload_all: precondition of body step check runs a command; mock the whole foreach step",
				"step upload_all: action s3.upload of body step upload has side effects; mock the whole foreach step",
			},
		},
		{
			name: "RealForeachBody",
			dag:  "foreach.yaml",
			handling: dagtest.Handling{
				Real: []string{"upload_all"},
			},
			want: []string{
				"step upload_all: precondition of body step check runs a command; mock the whole foreach step",
				"step upload_all: action s3.upload of body step upload has side effects; mock the whole foreach step",
			},
		},
		{
			name: "MockedForeach",
			dag:  "foreach.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{"upload_all": {}},
			},
		},
		{
			name: "UnhandledWaits",
			dag:  "waits.yaml",
			want: []string{
				"step sign_off: human.task waits for a person; set review in its mock",
				"step release: command has side effects; mock it or list it under real",
				"step release: approval waits for a person; set review in its mock",
			},
		},
		{
			name: "RealWaitStillNeedsReview",
			dag:  "waits.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{"sign_off": {Review: true}},
				Real:  []string{"sign_off", "release"},
			},
			want: []string{
				"step release: approval waits for a person; set review in its mock",
			},
		},
		{
			name: "ReviewedWaits",
			dag:  "waits.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{
					"sign_off": {Review: true},
					"release":  {Review: true},
				},
				Real: []string{"release"},
			},
		},
		{
			// A limit that resolves at run time may resolve to zero, which
			// makes the step continuous, so only a literal limit bounds it.
			name: "UnmockedContinuousStep",
			dag:  "continuous.yaml",
			want: []string{
				"step heartbeat: repeat policy has no fixed limit; mock it with results",
				"step probe: command has side effects; mock it or list it under real",
				"step rounds_log: repeat policy has no fixed limit; mock it with results",
			},
		},
		{
			// {} keeps the real action of a side-effect-free step, so the
			// step still repeats without end.
			name: "EmptyMockOnContinuousStep",
			dag:  "continuous.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{"heartbeat": {}, "probe": {}, "rounds_log": {}},
			},
			want: []string{
				"step heartbeat: repeat policy has no fixed limit; mock it with results",
				"step rounds_log: repeat policy has no fixed limit; mock it with results",
			},
		},
		{
			name: "MockedContinuousStep",
			dag:  "continuous.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{
					"heartbeat":  replacing,
					"probe":      replacing,
					"rounds_log": replacing,
				},
			},
		},
		{
			// One key would otherwise name both the step and the handler, so
			// the handler's mock would also cover the step.
			name: "StepNamedAsHandler",
			dag:  "handler_name.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{"handler_on.failure": {}},
			},
			want: []string{
				"step handler_on.failure: name is the key of a lifecycle handler; rename the step",
			},
		},
		{
			name: "BaseConfigHandler",
			dag:  "base_handler/dag.yaml",
			base: "base_handler/base.yaml",
			want: []string{
				"handler_on.failure: action mail.send has side effects; mock it or list it under real",
			},
		},
		{
			name: "MockedBaseConfigHandler",
			dag:  "base_handler/dag.yaml",
			base: "base_handler/base.yaml",
			handling: dagtest.Handling{
				Mocks: map[string]dagtest.MockFields{"handler_on.failure": {}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag := loadDAG(t, tt.dag, tt.base)
			var got []string
			for _, finding := range dagtest.CheckFailClosed(dag, tt.handling) {
				got = append(got, finding.String())
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// loadDAG loads a DAG from testdata as dagu loads it, without running the
// eval of any param.
func loadDAG(t *testing.T, name, base string) *ir.DAG {
	t.Helper()

	opts := []spec.LoadOption{spec.DeferParamEvals()}
	if base != "" {
		opts = append(opts, spec.WithBaseConfig(filepath.Join("testdata", base)))
	}
	dag, err := spec.Load(context.Background(), filepath.Join("testdata", name), opts...)
	require.NoError(t, err)
	return dag
}
