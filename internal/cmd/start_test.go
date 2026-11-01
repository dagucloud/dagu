// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestStartCommand(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	dagStart := th.DAG(t, `max_active_runs: 1
steps:
  - name: "1"
    run: "true"
`)

	dagStartWithParams := th.DAG(t, `params: "p1 p2"
steps:
  - name: "1"
    run: "echo \"params is $1 and $2\""
`)
	dagStartWithSingleParam := th.DAG(t, `params: "p1"
steps:
  - name: "1"
    run: "echo \"params is $1\""
`)

	dagStartWithDAGRunID := th.DAG(t, `steps:
  - name: "1"
    run: "true"
`)

	tests := []test.CmdTest{
		{
			Name:        "StartDAG",
			Args:        []string{"start", dagStart.Location},
			ExpectedOut: []string{"Step started"},
		},
		{
			Name:        "StartDAGWithDefaultParams",
			Args:        []string{"start", dagStartWithParams.Location},
			ExpectedOut: []string{`params="[1=p1 2=p2]"`},
		},
		{
			Name:        "StartDAGWithParams",
			Args:        []string{"start", `--params="p3 p4"`, dagStartWithParams.Location},
			ExpectedOut: []string{`params="[1=p3 2=p4]"`},
		},
		{
			Name:        "StartDAGWithParamsAfterDash",
			Args:        []string{"start", dagStartWithParams.Location, "--", "p5", "p6"},
			ExpectedOut: []string{`params="[1=p5 2=p6`},
		},
		{
			Name:        "StartDAGWithSpacedParamAfterDash",
			Args:        []string{"start", dagStartWithSingleParam.Location, "--", "Something here"},
			ExpectedOut: []string{`params="[1=Something here]"`},
		},
		{
			Name:        "StartDAGWithRequestID",
			Args:        []string{"start", dagStartWithDAGRunID.Location, "--run-id", "CfmC9GPywTC24bXbY1yEU7eQANNvpdxAPJXdSKTSaCVC"},
			ExpectedOut: []string{"CfmC9GPywTC24bXbY1yEU7eQANNvpdxAPJXdSKTSaCVC"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			th.RunCommand(t, cmd.Start(), tc)
		})
	}
}

func TestStartCommandResolvesRelativeNestedDAGPath(t *testing.T) {
	th := test.SetupCommand(t)
	relativePath := filepath.Join("nested", "relative.yaml")
	th.CreateDAGFile(t, relativePath, `
steps:
  - name: run
    run: "true"
`)
	t.Chdir(th.Config.Paths.DAGsDir)

	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Name:        "StartRelativeNestedDAG",
		Args:        []string{"start", relativePath},
		ExpectedOut: []string{"Step started"},
	})
}

func TestStartCommand_BuiltExecutablePreservesExplicitEnv(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t, test.WithBuiltExecutable())

	dag := th.DAG(t, `name: built-start-explicit-env
env:
  - EXPORTED_SECRET: ${CMD_START_EXPLICIT_ENV}
steps:
  - name: "capture"
    run: printf '%s|%s' "$EXPORTED_SECRET" "${CMD_START_EXPLICIT_ENV:-}"
    output: RESULT
`)

	test.RunBuiltCLI(t, th.Helper, []string{"CMD_START_EXPLICIT_ENV=from-host"}, "start", dag.Location)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status)
	require.Equal(t, "from-host|", test.StatusOutputValue(t, &status, "RESULT"))
}

func TestCmdStart_BackwardCompatibility(t *testing.T) {
	t.Run("ShouldRejectParametersAfterWithoutSeparator", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagContent := `
params: KEY1=default1 KEY2=default2
steps:
  - name: step1
    run: echo $KEY1 $KEY2
`
		dagFile := th.CreateDAGFile(t, "test-params.yaml", dagContent)

		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dagFile, "KEY1=value1", "KEY2=value2"},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "use '--' before parameters")
	})

	t.Run("ShouldAcceptParamsFlag", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagContent := `
params: KEY=default
steps:
  - name: step1
    run: echo $KEY
`
		dagFile := th.CreateDAGFile(t, "test-params-flag.yaml", dagContent)

		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dagFile, "--params", "KEY=value"},
		})
		require.NoError(t, err)

		dag, err := spec.Load(th.Context, dagFile)
		require.NoError(t, err)

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status)
		require.Equal(t, "KEY=value", status.Params)
	})
}

func TestCmdStart_PositionalParamValidation(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	dagFile := th.CreateDAGFile(t, "test-positional-params.yaml", `
params: "p1 p2"
steps:
  - name: step1
    run: echo $1 $2
`)
	dagNoParamsFile := th.CreateDAGFile(t, "test-no-params.yaml", `
steps:
  - name: step1
    run: echo $1
`)

	t.Run("AllowsTooFewAfterDash", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dagFile, "--", "only-one"},
		})
		require.NoError(t, err)
	})

	t.Run("RejectsTooManyAfterDash", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dagFile, "--", "one", "two", "three"},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "too many positional params: expected at most 2, got 3")
	})

	t.Run("AllowsTooFewWithParamsFlag", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params", "only-one", dagFile},
		})
		require.NoError(t, err)
	})

	t.Run("AllowsNamedOnlyWithPositionalDefaults", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params", "KEY1=value1 KEY2=value2", dagFile},
		})
		require.NoError(t, err)
	})

	t.Run("AllowsJSONParamsWithoutPositionalValidation", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params", `{"KEY":"value"}`, dagFile},
		})
		require.NoError(t, err)
	})

	t.Run("AllowsJSONAfterDashWithoutPositionalValidation", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dagFile, "--", `{"KEY":"value"}`},
		})
		require.NoError(t, err)

		dag, err := spec.Load(th.Context, dagFile)
		require.NoError(t, err)

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status)
		require.Contains(t, status.Params, "KEY=value")
	})

	t.Run("AllowsNamedPairsWhenNoParamsDeclared", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dagNoParamsFile, "--", "key1=value1", "key2=value2"},
		})
		require.NoError(t, err)
	})

	t.Run("AllowsPositionalWhenNoParamsDeclared", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dagNoParamsFile, "--", "success"},
		})
		require.NoError(t, err)
	})
}

// Inherited stdin must remain available to the caller, such as a shell loop.
// These cases replace process stdin and must remain sequential.
func TestRunPreservesStdin(t *testing.T) {
	for _, commandName := range []string{"start", "enqueue"} {
		t.Run(commandName, func(t *testing.T) {
			th := test.SetupCommand(t)
			dag := th.DAG(t, `params: VALUE=default
steps:
  - name: print
    run: echo $VALUE
`)
			const input = "VALUE=from-stdin\n"
			pipeCommandStdin(t, input)
			command := cmd.Start()
			if commandName == "enqueue" {
				command = cmd.Enqueue()
			}
			th.RunCommand(t, command, test.CmdTest{
				Args: []string{commandName, dag.Location},
			})

			status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
			require.NoError(t, err)
			require.Equal(t, "VALUE=default", status.Params)
			remaining, err := io.ReadAll(os.Stdin)
			require.NoError(t, err)
			require.Equal(t, input, string(remaining))
		})
	}
}

// Closed stdin must only fail commands that select it as their parameter source.
// These cases replace process stdin and must remain sequential.
func TestRunClosedStdin(t *testing.T) {
	stdin, err := os.Open(os.DevNull)
	require.NoError(t, err)
	require.NoError(t, stdin.Close())
	original := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = original })

	for _, commandName := range []string{"start", "enqueue"} {
		t.Run(commandName, func(t *testing.T) {
			for _, tt := range []struct {
				name   string
				flags  []string
				dash   []string
				params string
				fails  bool
			}{
				{name: "Selected", flags: []string{"--params-stdin"}, fails: true},
				{name: "Inherited", params: "VALUE=default"},
				{name: "Flag", flags: []string{"--params-stdin", "--params=VALUE=flag"}, params: "VALUE=flag"},
				{name: "EmptyFlag", flags: []string{"--params-stdin", "--params="}, params: "VALUE=default"},
				{name: "Dash", flags: []string{"--params-stdin"}, dash: []string{"--", "VALUE=dash"}, params: "VALUE=dash"},
				{name: "EmptyDash", flags: []string{"--params-stdin"}, dash: []string{"--"}, params: "VALUE=default"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					th := test.SetupCommand(t)
					dag := th.DAG(t, "params: VALUE=default\nsteps:\n  - name: print\n    run: echo $VALUE\n")
					args := append([]string{commandName, "--run-id=closed-stdin"}, tt.flags...)
					args = append(args, dag.Location)
					args = append(args, tt.dash...)
					command := cmd.Start()
					if commandName == "enqueue" {
						command = cmd.Enqueue()
					}
					err := th.RunCommandWithError(t, command, test.CmdTest{Args: args})
					if tt.fails {
						require.ErrorContains(t, err, "params from stdin")
						_, err = th.DAGRunRepository.FindAttempt(th.Context, ir.NewDAGRunRef(dag.Name, "closed-stdin"))
						require.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
						return
					}
					require.NoError(t, err)
					status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
					require.NoError(t, err)
					require.Equal(t, tt.params, status.Params)
				})
			}
		})
	}
}

// TestCmdStart_StdinParams replaces the process-global os.Stdin, so the test
// and its subtests must stay sequential to avoid feeding other commands.
func TestCmdStart_StdinParams(t *testing.T) {
	positionalDAG := `params: "p1 p2"
steps:
  - name: "1"
    run: "echo \"params is $1 and $2\""
`
	namedDAG := `params: KEY1=default1 KEY2=default2
steps:
  - name: "1"
    run: "echo $KEY1 $KEY2"
`

	t.Run("PipedPositionalParams", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, positionalDAG)
		pipeCommandStdin(t, "s1 s2\n")

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        []string{"start", "--params-stdin", dag.Location},
			ExpectedOut: []string{`params="[1=s1 2=s2]`},
		})
		assertLatestParams(t, th, dag.Location, "1=s1 2=s2")
	})

	for _, tt := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "Quoted", input: `"hello world"`, want: "1=hello world"},
		{name: "EmptyValue", input: `""`, want: "1="},
		{name: "QuotedSpaces", input: `" hello world "`, want: "1= hello world "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			th := test.SetupCommand(t)
			dag := th.DAG(t, "params: default\nsteps:\n  - name: print\n    run: echo ok\n")
			pipeCommandStdin(t, tt.input)
			th.RunCommand(t, cmd.Start(), test.CmdTest{
				Args: []string{"start", "--params-stdin", dag.Location},
			})
			assertLatestParams(t, th, dag.Location, tt.want)
		})
	}

	t.Run("PipedNamedParamsAcrossLines", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, namedDAG)
		pipeCommandStdin(t, "KEY1=\"hello world\"\nKEY2=\"\"\n")

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params-stdin", dag.Location},
		})
		assertLatestParams(t, th, dag.Location, "KEY1=hello world KEY2=")
	})

	t.Run("PipedJSONParams", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, `params: KEY=default
steps:
  - name: "1"
    run: "echo $KEY"
`)
		pipeCommandStdin(t, `{"KEY":"v1"}`)

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params-stdin", dag.Location},
		})
		assertLatestParams(t, th, dag.Location, "KEY=v1")
	})

	t.Run("EmptyStdinBehavesLikeNoParams", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, namedDAG)
		pipeCommandStdin(t, "")

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params-stdin", dag.Location},
		})
		assertLatestParams(t, th, dag.Location, "KEY1=default1 KEY2=default2")
	})

	t.Run("WhitespaceStdinBehavesLikeNoParams", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, namedDAG)
		pipeCommandStdin(t, "  \n\t\n")

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params-stdin", dag.Location},
		})
		assertLatestParams(t, th, dag.Location, "KEY1=default1 KEY2=default2")
	})

	t.Run("ParamsFlagTakesPrecedenceOverStdin", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, positionalDAG)
		pipeCommandStdin(t, "s1 s2\n")

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params-stdin", `--params="c1 c2"`, dag.Location},
		})
		assertLatestParams(t, th, dag.Location, "1=c1 2=c2")
	})

	t.Run("DashArgsTakePrecedenceOverStdin", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, positionalDAG)
		pipeCommandStdin(t, "s1 s2\n")

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params-stdin", dag.Location, "--", "d1", "d2"},
		})
		assertLatestParams(t, th, dag.Location, "1=d1 2=d2")
	})

	t.Run("RejectsTooManyPositionalFromStdin", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, positionalDAG)
		pipeCommandStdin(t, "one two three\n")

		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--params-stdin", dag.Location},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "too many positional params: expected at most 2, got 3")
	})
}

// pipeCommandStdin replaces process stdin with a pipe holding input until the
// test ends. The caller must not be parallel.
func pipeCommandStdin(t *testing.T, input string) {
	t.Helper()

	stdin, writer, err := os.Pipe()
	require.NoError(t, err)
	originalStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = originalStdin
		require.NoError(t, stdin.Close())
	})
	_, err = writer.WriteString(input)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
}

func assertLatestParams(t *testing.T, th test.Command, dagPath, want string) {
	t.Helper()

	dag, err := spec.Load(th.Context, dagPath)
	require.NoError(t, err)
	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status)
	require.Equal(t, want, status.Params)
}

func TestCmdStart_FromRunID(t *testing.T) {
	t.Run("RejectsStdinParams", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--from-run-id=source", "--params-stdin", "dag.yaml"},
		})
		require.ErrorContains(t, err, "parameters cannot be provided when using --from-run-id")
	})

	t.Run("ReschedulesWithStoredParameters", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dag := th.DAG(t, `params: "alpha beta"
steps:
  - name: "echo"
    run: "echo $1 $2"
`)

		// Kick off an initial run so we have history to clone.
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})

		ctx := context.Background()
		originalStatus, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, originalStatus.Status)

		newRunID := "rescheduled_run"
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{
				"start",
				fmt.Sprintf("--from-run-id=%s", originalStatus.DAGRunID),
				fmt.Sprintf("--run-id=%s", newRunID),
				dag.Name,
			},
		})

		require.Eventually(t, func() bool {
			status, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, newRunID)
			return err == nil && status != nil && status.Status == ir.Succeeded
		}, 5*time.Second, 100*time.Millisecond)

		newStatus, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, newRunID)
		require.NoError(t, err)
		require.NotNil(t, newStatus)
		require.Equal(t, originalStatus.Params, newStatus.Params)
		require.Equal(t, originalStatus.ParamsList, newStatus.ParamsList)
	})

}

func TestCmdStart_Only(t *testing.T) {
	const chainDAG = `steps:
  - id: first
    run: echo first
  - name: second step
    id: second
    run: echo second
  - id: third
    run: echo third
`
	// consume echoes the value produce published through a legacy output
	// variable, so the carried value shows up in consume's own output.
	const outputsDAG = `steps:
  - id: produce
    run: echo from-source
    output: VALUE
  - id: consume
    depends: produce
    run: echo got-${VALUE}
    output: RESULT
`

	t.Run("SkipsOthers", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, chainDAG)

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=third", dag.Location},
		})

		status := readRunStatus(t, th, dag.Name, "only")
		require.Equal(t, ir.Succeeded, status.Status)
		require.Equal(t, ir.TriggerTypeManual, status.TriggerType)
		require.Equal(t, []ir.NodeStatus{ir.NodeSkipped, ir.NodeSkipped, ir.NodeSucceeded}, nodeStatuses(status))
	})

	t.Run("ByID", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, chainDAG)

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=second", dag.Location},
		})

		status := readRunStatus(t, th, dag.Name, "only")
		require.Equal(t, []ir.NodeStatus{ir.NodeSkipped, ir.NodeSucceeded, ir.NodeSkipped}, nodeStatuses(status))
	})

	t.Run("OutputsFrom", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, outputsDAG)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=source", dag.Location},
		})

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--outputs-from=source", dag.Location},
		})

		status := readRunStatus(t, th, dag.Name, "only")
		require.Equal(t, []ir.NodeStatus{ir.NodeSkipped, ir.NodeSucceeded}, nodeStatuses(status))
		require.Equal(t, "RESULT=got-from-source", outputVariable(t, status.Nodes[1], "RESULT"))
	})

	// --output-from names the run that gives the outputs of one skipped step,
	// and the run records that run with the times of the source step.
	t.Run("OutputFrom", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, outputsDAG)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=source", dag.Location},
		})

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--output-from=produce=source", dag.Location},
		})

		status := readRunStatus(t, th, dag.Name, "only")
		require.Equal(t, []ir.NodeStatus{ir.NodeSkipped, ir.NodeSucceeded}, nodeStatuses(status))
		require.Equal(t, "RESULT=got-from-source", outputVariable(t, status.Nodes[1], "RESULT"))
		source := readRunStatus(t, th, dag.Name, "source")
		require.Len(t, status.ReusedInputs, 1)
		reused := status.ReusedInputs[0]
		require.Equal(t, "produce", reused.Step)
		require.Equal(t, "source", reused.RunID)
		require.Equal(t, ir.ReusedInputViaRun, reused.Via)
		require.NotEmpty(t, reused.StartedAt)
		require.Equal(t, source.Nodes[0].StartedAt, reused.StartedAt)
		require.Equal(t, source.Nodes[0].FinishedAt, reused.FinishedAt)
	})

	// The run of --output-from wins over --outputs-from for its step.
	t.Run("OutputFromWinsOverOutputsFrom", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - id: produce
    run: echo from-${DAG_RUN_ID}
    output: VALUE
  - id: consume
    depends: produce
    run: echo got-${VALUE}
    output: RESULT
`)
		for _, id := range []string{"first", "second"} {
			th.RunCommand(t, cmd.Start(), test.CmdTest{
				Args: []string{"start", "--run-id=" + id, dag.Location},
			})
		}

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--outputs-from=first", "--output-from=produce=second", dag.Location},
		})

		status := readRunStatus(t, th, dag.Name, "only")
		require.Equal(t, "RESULT=got-from-second", outputVariable(t, status.Nodes[1], "RESULT"))
		require.Len(t, status.ReusedInputs, 1)
		require.Equal(t, "second", status.ReusedInputs[0].RunID)
	})

	t.Run("OutputFromRunOfAnotherDAG", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, outputsDAG)
		other := th.DAG(t, "steps:\n  - id: other\n    run: echo other\n")
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=elsewhere", other.Location},
		})

		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--output-from=produce=elsewhere", dag.Location},
		})
		require.ErrorContains(t, err, "step produce cannot be reused from run elsewhere: the run does not exist for DAG "+dag.Name)
		requireNoRun(t, th, dag.Name, "only")
	})

	t.Run("OutputFromMissingRun", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, outputsDAG)

		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--output-from=produce=missing", dag.Location},
		})
		require.ErrorContains(t, err, "step produce cannot be reused from run missing: the run does not exist for DAG "+dag.Name)
		requireNoRun(t, th, dag.Name, "only")
	})

	t.Run("OutputFromFailedStep", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - id: produce
    run: exit 1
    output: VALUE
  - id: consume
    depends: produce
    run: echo got-${VALUE}
`)
		_ = th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=source", dag.Location},
		})

		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--output-from=produce=source", dag.Location},
		})
		require.ErrorContains(t, err, "step produce cannot be reused from run source: the step did not succeed there")
		requireNoRun(t, th, dag.Name, "only")
	})

	t.Run("OutputFromSelectedStep", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, outputsDAG)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=source", dag.Location},
		})

		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--output-from=consume=source", dag.Location},
		})
		require.ErrorContains(t, err, `names step "consume", which is selected to run`)
		requireNoRun(t, th, dag.Name, "only")
	})

	// The deprecated --output stands in for the skipped producer without any
	// earlier run, and records no source run.
	t.Run("SetsOutput", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, outputsDAG)

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--output=produce.VALUE=given", dag.Location},
		})

		status := readRunStatus(t, th, dag.Name, "only")
		require.Equal(t, []ir.NodeStatus{ir.NodeSkipped, ir.NodeSucceeded}, nodeStatuses(status))
		require.Equal(t, "RESULT=got-given", outputVariable(t, status.Nodes[1], "RESULT"))
		require.Empty(t, status.ReusedInputs)
	})

	t.Run("OutputOverridesSource", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, outputsDAG)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=source", dag.Location},
		})

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--outputs-from=source", "--output=produce.VALUE=given", dag.Location},
		})

		status := readRunStatus(t, th, dag.Name, "only")
		require.Equal(t, "RESULT=got-given", outputVariable(t, status.Nodes[1], "RESULT"))
	})

	// A failed producer publishes no outputs, so none are carried into the
	// selected step.
	t.Run("FailedSource", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - id: produce
    run: |
      echo from-source
      exit 1
    output: VALUE
  - id: consume
    depends: produce
    run: echo got-${VALUE}
    output: RESULT
`)
		_ = th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=source", dag.Location},
		})

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--outputs-from=source", dag.Location},
		})

		status := readRunStatus(t, th, dag.Name, "only")
		require.Equal(t, ir.NodeSucceeded, status.Nodes[1].Status)
		require.NotContains(t, outputVariable(t, status.Nodes[1], "RESULT"), "from-source")
	})

	rejects := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "UnknownStep", args: []string{"--only=missing"}, wantErr: `unknown step "missing"`},
		{name: "NeedsOnly", args: []string{"--outputs-from=source"}, wantErr: "--outputs-from requires --only"},
		{name: "FromRunID", args: []string{"--only=first", "--from-run-id=source"}, wantErr: "--only cannot be combined with --from-run-id"},
		{name: "SubDAGRun", args: []string{"--only=first", "--run-id=child", "--parent=parent:run", "--root=parent:run"}, wantErr: "--only cannot be combined with --parent"},
		{name: "OutputFromNeedsOnly", args: []string{"--output-from=first=source"}, wantErr: "--output-from requires --only"},
		{name: "OutputFromWithoutRun", args: []string{"--only=third", "--output-from=first"}, wantErr: `invalid --output-from "first": expected <step>=<run-id>`},
		{name: "OutputFromWithoutStep", args: []string{"--only=third", "--output-from==source"}, wantErr: `invalid --output-from "=source": expected <step>=<run-id>`},
		{name: "OutputFromTwice", args: []string{"--only=third", "--output-from=first=a", "--output-from=first=b"}, wantErr: `--output-from names step "first" more than once`},
		{name: "OutputNeedsOnly", args: []string{"--output=first.value=x"}, wantErr: "--output requires --only"},
		{name: "OutputWithoutValue", args: []string{"--only=third", "--output=first.value"}, wantErr: `invalid --output "first.value": expected <step>.<name>=<value>`},
		{name: "OutputWithoutName", args: []string{"--only=third", "--output=first=x"}, wantErr: `invalid --output "first": expected <step>.<name>=<value>`},
		{name: "OutputTwice", args: []string{"--only=third", "--output=first.value=a", "--output=first.value=b"}, wantErr: `--output sets "first.value" more than once`},
		{name: "OutputOfSelectedStep", args: []string{"--only=third", "--output=third.value=x"}, wantErr: `cannot set outputs of step "third": it is selected to run`},
	}
	for _, tt := range rejects {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			th := test.SetupCommand(t)
			dag := th.DAG(t, chainDAG)

			err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
				Args: append(append([]string{"start"}, tt.args...), dag.Location),
			})
			require.ErrorContains(t, err, tt.wantErr)
		})
	}

	t.Run("ActiveSource", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)
		dag := th.DAG(t, outputsDAG)
		attempt, err := th.DAGRunRepository.CreateAttempt(th.Context, dag.DAG, time.Now(), "source", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		status := ir.InitialStatus(dag.DAG)
		status.DAGRunID = "source"
		status.AttemptID = attempt.ID()
		status.Status = ir.Queued
		writeStatus(t, th.Context, attempt, status)

		err = th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=only", "--only=consume", "--outputs-from=source", dag.Location},
		})
		require.ErrorContains(t, err, "outputs are reused only from a finished run")
	})
}

func readRunStatus(t *testing.T, th test.Command, dagName, dagRunID string) *ir.DAGRunStatus {
	t.Helper()
	attempt, err := th.DAGRunRepository.FindAttempt(th.Context, ir.NewDAGRunRef(dagName, dagRunID))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	return status
}

// requireNoRun makes sure that a rejected start left no run behind.
func requireNoRun(t *testing.T, th test.Command, dagName, dagRunID string) {
	t.Helper()
	_, err := th.DAGRunRepository.FindAttempt(th.Context, ir.NewDAGRunRef(dagName, dagRunID))
	require.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
}

func nodeStatuses(status *ir.DAGRunStatus) []ir.NodeStatus {
	statuses := make([]ir.NodeStatus, 0, len(status.Nodes))
	for _, node := range status.Nodes {
		statuses = append(statuses, node.Status)
	}
	return statuses
}

func outputVariable(t *testing.T, node *ir.Node, key string) string {
	t.Helper()
	require.NotNil(t, node.OutputVariables)
	value, ok := node.OutputVariables.Load(key)
	require.True(t, ok, "output variable %s not recorded", key)
	return value.(string)
}

func TestCmdStart_DuplicateRunIDDoesNotOverwriteExistingAttempt(t *testing.T) {
	th := test.SetupCommand(t)

	dag := th.DAG(t, `name: duplicate-start-dag
steps:
  - name: "1"
    run: "true"
`)

	runID := "existing-run"
	attempt, err := th.DAGRunRepository.CreateAttempt(th.Context, dag.DAG, time.Now(), runID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)

	status := ir.InitialStatus(dag.DAG)
	status.DAGRunID = runID
	status.AttemptID = attempt.ID()
	writeStatus(t, th.Context, attempt, status)

	err = th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", runID, dag.Location},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "already exists")

	latestAttempt, err := th.DAGRunRepository.FindAttempt(th.Context, ir.NewDAGRunRef(dag.Name, runID))
	require.NoError(t, err)
	require.Equal(t, attempt.ID(), latestAttempt.ID())

	latestStatus, err := latestAttempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, ir.NotStarted, latestStatus.Status)
	require.Empty(t, latestStatus.Error)
}

func TestCmdStart_AcceptsLegacyProcArtifactsDuringContextInit(t *testing.T) {
	th := test.SetupCommand(t)

	dag := th.DAG(t, `name: start-after-legacy-proc
steps:
  - name: "1"
    run: "true"
`)

	writeLegacyCommandProcFile(
		t,
		th.Config.Paths.ProcDir,
		"legacy-group",
		"legacy-dag",
		"legacy-run",
		time.Now().UTC().Add(-time.Minute),
		time.Now().UTC().Add(-10*time.Second),
	)

	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", dag.Location},
	})
	require.NoError(t, err)
}

func writeLegacyCommandProcFile(
	t *testing.T,
	procDir, groupName, dagName, dagRunID string,
	createdAt, heartbeatAt time.Time,
) string {
	t.Helper()

	path := filepath.Join(
		procDir,
		groupName,
		dagName,
		fmt.Sprintf("proc_%s_%s.proc", createdAt.UTC().Format("20060102_150405Z"), dagRunID),
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(heartbeatAt.UTC().Unix())) //nolint:gosec
	require.NoError(t, os.WriteFile(path, buf, 0o600))
	require.NoError(t, os.Chtimes(path, heartbeatAt, heartbeatAt))

	return path
}
