// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec060_node_script holds black-box conformance tests for
// Spec 060: Node Script Action (node-script@v1).
//
// Unlike every other built-in action this conformance suite covers,
// node-script@v1 is a remote official action: referencing it clones
// https://github.com/dagucloud/node-script at the given tag and
// provisions its own pinned Node.js runtime (via the project's aqua-based
// tool manager) into the isolated $HOME each test run gets, on first use.
// That first invocation genuinely reaches the network and can take on the
// order of 30 seconds; this package raises the harness's per-command
// timeout for its live tests accordingly, and keeps the number of
// separate dagu invocations small by packing multiple independent
// scenarios into one multi-step DAG per test.
package spec060_node_script_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestNodeScriptLive(t *testing.T) {
	// No subtest below uses t.Parallel(): each calls t.Setenv to raise the
	// harness's per-command timeout past the ~30s a cold node-script@v1
	// invocation (first-time git clone plus Node.js provisioning) can take,
	// and t.Setenv itself forbids t.Parallel() in the same test.
	t.Run("script runs against real Node.js, with input/env/console and dagu's own value resolution", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "happy_path.yaml")
		result.ExpectExitCode(0)

		var basic struct {
			OK     bool `json:"ok"`
			Result int  `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 0)), &basic))
		require.True(t, basic.OK)
		require.Equal(t, 2, basic.Result)

		var inputConsole struct {
			OK     bool `json:"ok"`
			Result struct {
				Greeting string `json:"greeting"`
				Doubled  int    `json:"doubled"`
			} `json:"result"`
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 1)), &inputConsole))
		require.True(t, inputConsole.OK)
		require.Equal(t, "Hello, World", inputConsole.Result.Greeting)
		require.Equal(t, 6, inputConsole.Result.Doubled)
		require.Equal(t, "logging a line", inputConsole.Stdout)
		require.Equal(t, "erroring a line", inputConsole.Stderr)

		var envAccess struct {
			Result struct {
				FromEnv string `json:"fromEnv"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 2)), &envAccess))
		require.Equal(t, "from-env", envAccess.Result.FromEnv, "env inside the script is the real process environment, inherited from the DAG's own env:")

		// params mirrors the action's own received with: config (input and
		// script), not the parent workflow's params: block -- there is no
		// parent-level param named "x" or "script" here, so this is purely
		// the action's own configuration reflected back.
		var paramsMirror struct {
			Result struct {
				Params struct {
					Input  map[string]any `json:"input"`
					Script string         `json:"script"`
				} `json:"params"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 3)), &paramsMirror))
		require.EqualValues(t, map[string]any{"x": float64(1)}, paramsMirror.Result.Params.Input)
		require.Contains(t, paramsMirror.Result.Params.Script, "return { params: params };")

		// with.script's text goes through dagu's own value resolution
		// (like an ordinary run: script body, unlike template.render's
		// with.template) before Node ever sees it: a literal "$MY_VAR"
		// token is replaced, while a JS template literal's ${...} --
		// which is not a dagu reference -- passes through untouched.
		var dollarSub struct {
			Result struct {
				TemplateLiteral string `json:"templateLiteral"`
				DollarVar       string `json:"dollarVar"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 4)), &dollarSub))
		require.Equal(t, "computed 2", dollarSub.Result.TemplateLiteral)
		require.Equal(t, "from-env", dollarSub.Result.DollarVar)

		// Dynamic import() works inside the script, and a script that
		// returns undefined (rather than executing a bare return) reports
		// result: null.
		var dynamicImport struct {
			OK     bool   `json:"ok"`
			Result any    `json:"result"`
			Stdout string `json:"stdout"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 5)), &dynamicImport))
		require.True(t, dynamicImport.OK)
		require.Nil(t, dynamicImport.Result)
		require.Contains(t, dynamicImport.Stdout, "platform: ")
	})

	t.Run("a thrown error, a syntax error, or exceeding with.timeoutSeconds reports ok:false and fails the step", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "error_scenarios.yaml")
		result.ExpectNonZeroExitCode()

		var thrown struct {
			OK     bool `json:"ok"`
			Result any  `json:"result"`
			Error  struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 0)), &thrown))
		require.False(t, thrown.OK)
		require.Nil(t, thrown.Result)
		require.Equal(t, "Error", thrown.Error.Name)
		require.Equal(t, "boom", thrown.Error.Message)

		var syntaxErr struct {
			OK    bool `json:"ok"`
			Error struct {
				Name string `json:"name"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 1)), &syntaxErr))
		require.False(t, syntaxErr.OK)
		require.Equal(t, "SyntaxError", syntaxErr.Error.Name)

		var timedOut struct {
			OK bool `json:"ok"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 2)), &timedOut))
		require.False(t, timedOut.OK, "with.timeoutSeconds should have cut the 5-second sleep short at 1 second")

		// with.script missing is caught by the action's own inputs schema
		// before it ever provisions or invokes Node, so this step's error
		// appears directly in dagu's own output, with no stdout log of its
		// own to read.
		require.Contains(t, result.Stdout(), `missing properties: ["script"]`)
	})

	// The action's own published usage example has a later step read a
	// result field as ${<step id>.outputs.<path>} -- a bare step-id
	// reference, not ${steps.<step id>.outputs.<name>} -- and drills into
	// a nested field of the returned object. This is a different,
	// separately-supported reference form from the strict
	// ${steps.*.outputs.*} form spec 012 documents for declared step
	// outputs, and it does work for an action:-type (remote action) step.
	t.Run("a later step reads a nested result field as ${<step id>.outputs.<path>}", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "downstream_reference.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "release tag is v1.2.3\n", stepStdout(t, result.Stdout(), 1))
	})

	// The action's docs describe with.env as an object of extra
	// environment variables merged into the script's process.env. As
	// observed, this does not currently work: any with: field literally
	// named "env" is normalized the same way the DAG/step-level env: field
	// is (a list of single-key mappings), which does not match what the
	// action's own input schema expects, so the step fails validating its
	// own input before Node ever runs -- regardless of which of the two
	// forms (a plain mapping or a list of single-key mappings) with.env is
	// authored as.
	t.Run("with.env does not currently reach the script, regardless of how it is authored", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "with_env_field.yaml")
		result.ExpectNonZeroExitCode()
		require.Contains(t, result.Stdout(), `validating /properties/env`)
	})
}

// TestNodeScriptValidation proves that dagu validate never resolves a
// remote action reference (which would require network access): a
// node-script@v1 step passes validate regardless of its with: content,
// and even with.script -- required by the action's own inputs schema --
// is enforced only once the step actually runs and that schema is fetched.
// The one thing validate does check locally is the action reference's own
// syntax.
func TestNodeScriptValidation(t *testing.T) {
	t.Parallel()

	t.Run("a node-script@v1 step with no with.script passes validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "error_scenarios.yaml")
		result.ExpectExitCode(0)
	})

	t.Run("an action reference missing its required @version suffix fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "no_version_suffix.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`unknown action "node-script"`)
	})
}
