// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec061_python_script holds black-box conformance tests for
// Spec 061: Python Script Action (python-script@v1).
//
// Like node-script@v1 (spec 060), python-script@v1 is a remote official
// action: referencing it clones https://github.com/dagucloud/python-script
// at the given tag and provisions its own pinned uv/Python toolchain (via
// the project's aqua-based tool manager) into the isolated $HOME each test
// run gets, on first use. That first invocation genuinely reaches the
// network and can take on the order of 30 seconds; this package raises the
// harness's per-command timeout for its live tests accordingly, and keeps
// the number of separate dagu invocations small by packing multiple
// independent scenarios into one multi-step DAG per test.
package spec061_python_script_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestPythonScriptLive(t *testing.T) {
	// No subtest below uses t.Parallel(): each calls t.Setenv to raise the
	// harness's per-command timeout past the ~30s a cold python-script@v1
	// invocation (first-time git clone plus uv/Python provisioning) can
	// take, and t.Setenv itself forbids t.Parallel() in the same test.
	t.Run("script runs against real Python, with input/env/params/requirements and dagu's own value resolution", func(t *testing.T) {
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

		var inputPrint struct {
			OK     bool `json:"ok"`
			Result struct {
				Greeting string `json:"greeting"`
				Doubled  int    `json:"doubled"`
			} `json:"result"`
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 1)), &inputPrint))
		require.True(t, inputPrint.OK)
		require.Equal(t, "Hello, World", inputPrint.Result.Greeting)
		require.Equal(t, 6, inputPrint.Result.Doubled)
		require.Equal(t, "logging a line\n", inputPrint.Stdout)
		require.Equal(t, "erroring a line\n", inputPrint.Stderr)

		var envAccess struct {
			Result struct {
				FromEnv string `json:"fromEnv"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 2)), &envAccess))
		require.Equal(t, "from-env", envAccess.Result.FromEnv, "env inside the script is the real process environment, inherited from the DAG's own env:")

		// params mirrors the action's own received with: config, not the
		// parent workflow's params: block.
		var paramsMirror struct {
			Result struct {
				HasScript bool           `json:"hasScript"`
				Input     map[string]any `json:"input"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 3)), &paramsMirror))
		require.True(t, paramsMirror.Result.HasScript)
		require.EqualValues(t, map[string]any{"x": float64(1)}, paramsMirror.Result.Input)

		// with.script's text goes through dagu's own value resolution
		// (like an ordinary run: script body) before Python ever sees it:
		// a literal "$MY_VAR" token is replaced.
		var dollarSub struct {
			Result struct {
				DollarVar string `json:"dollarVar"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 4)), &dollarSub))
		require.Equal(t, "from-env", dollarSub.Result.DollarVar)

		// with.requirements installs the listed packages (via uv run
		// --with) before the script runs, and top-level await works in
		// the script body.
		var requirementsAwait struct {
			OK     bool `json:"ok"`
			Result struct {
				Major        int `json:"major"`
				ServiceCount int `json:"serviceCount"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 5)), &requirementsAwait))
		require.True(t, requirementsAwait.OK)
		require.Equal(t, 2, requirementsAwait.Result.Major)
		require.Equal(t, 3, requirementsAwait.Result.ServiceCount)
	})

	t.Run("a raised exception, a syntax error, a timeout, or an unresolvable requirement reports ok:false and fails the step", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "error_scenarios.yaml")
		result.ExpectNonZeroExitCode()

		var raised struct {
			OK     bool `json:"ok"`
			Result any  `json:"result"`
			Error  struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 0)), &raised))
		require.False(t, raised.OK)
		require.Nil(t, raised.Result)
		require.Equal(t, "ValueError", raised.Error.Name)
		require.Equal(t, "boom", raised.Error.Message)

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
			OK    bool `json:"ok"`
			Error struct {
				Name string `json:"name"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 2)), &timedOut))
		require.False(t, timedOut.OK, "with.timeoutSeconds should have cut the 5-second sleep short at 1 second")
		require.Equal(t, "TimeoutError", timedOut.Error.Name)

		// An unresolvable with.requirements entry fails before the script
		// ever runs (uv itself cannot solve the dependency), reported as a
		// synthetic RuntimeError rather than a Python exception the script
		// raised, with uv's own solver output as the error message.
		var badRequirement struct {
			OK    bool `json:"ok"`
			Error struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 3)), &badRequirement))
		require.False(t, badRequirement.OK)
		require.Equal(t, "RuntimeError", badRequirement.Error.Name)

		// with.script missing is caught by the action's own inputs schema
		// before it ever provisions or invokes Python, so this step's
		// error appears directly in dagu's own output, with no stdout log
		// of its own to read.
		require.Contains(t, result.Stdout(), `missing properties: ["script"]`)
	})

	// Like node-script@v1, a later step reads a result field as
	// ${<step id>.outputs.<path>} -- a bare-step-id reference, not
	// ${steps.<step id>.outputs.<name>} -- confirming this is a property
	// of remote action:-type steps generally, not specific to one action.
	t.Run("a later step reads a nested result field as ${<step id>.outputs.<path>}, but not via steps.<id>.outputs.<name>", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "downstream_reference.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("bad substitution")

		require.Equal(t, "bare tag is v1.2.3\n", stepStdout(t, result.Stdout(), 1))
	})
}

// TestPythonScriptValidation proves that dagu validate never resolves a
// remote action reference (which would require network access): a
// python-script@v1 step passes validate regardless of its with: content,
// and even with.script -- required by the action's own inputs schema -- is
// enforced only once the step actually runs and that schema is fetched.
// The one thing validate does check locally is the action reference's own
// syntax.
func TestPythonScriptValidation(t *testing.T) {
	t.Parallel()

	t.Run("a python-script@v1 step with no with.script passes validate", func(t *testing.T) {
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
		result.ExpectStderrContains(`unknown action "python-script"`)
	})
}
