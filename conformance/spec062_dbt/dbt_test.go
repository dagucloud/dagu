// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec062_dbt holds black-box conformance tests for Spec 062: DBT
// Action (dbt@v1).
//
// Like node-script@v1 (spec 060) and python-script@v1 (spec 061), dbt@v1 is
// a remote official action: referencing it clones
// https://github.com/dagucloud/dbt at the given tag and provisions its own
// pinned uv/Python toolchain (via the project's aqua-based tool manager)
// into the isolated $HOME each test run gets, on first use, then runs the
// real dbt CLI it installs through uv against a real (DuckDB-backed) dbt
// project. That first invocation genuinely reaches the network and can take
// on the order of 30-45 seconds (DuckDB's own wheel alone is tens of
// megabytes); this package raises the harness's per-command timeout for its
// live tests accordingly, and keeps the number of separate dagu invocations
// small by packing multiple independent scenarios into one multi-step DAG
// per test. The dbt project and profile fixtures live under
// testdata/dbt_project and testdata/dbt_profiles; profiles.yml resolves its
// DuckDB file path from a DBT_DB_PATH environment variable (defaulting to
// an in-memory database), and the DAG fixtures resolve dbt@v1's own
// with.projectDir/with.profilesDir/with.targetPath/with.logPath fields from
// host environment variables the test supplies via RunWithEnv, since static
// fixture YAML cannot embed the isolated project's own runtime path.
package spec062_dbt_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// dbtProjectEnv returns the host environment entries the fixtures resolve
// their with.projectDir/with.profilesDir/with.targetPath/with.logPath
// fields from, rooted at the isolated project dagu.NewRunner seeded from
// this package's testdata. The testdata/dbt_profiles profile targets an
// in-memory DuckDB database, which is enough for any single dbt invocation
// on its own but does not persist data for a later, separate invocation to
// see.
func dbtProjectEnv(dagu *harness.Runner) []string {
	return []string{
		"HOST_PROJECT_DIR=" + dagu.ProjectPath("dbt_project"),
		"HOST_PROFILES_DIR=" + dagu.ProjectPath("dbt_profiles"),
		"HOST_TARGET_PATH=" + dagu.ProjectPath("artifacts/dbt-target"),
		"HOST_LOG_PATH=" + dagu.ProjectPath("artifacts/dbt-logs"),
	}
}

// dbtPersistentProfileEnv is like dbtProjectEnv, but points profilesDir at a
// profile this test writes itself, targeting a real DuckDB file under the
// isolated project rather than an in-memory database, so that the seed
// step's data is still there when the later freshness step queries it in
// its own, separate dbt invocation.
//
// The DuckDB file's path is only known at test run time (it is inside
// t.TempDir()), so it cannot live in a static profiles.yml fixture. It is
// deliberately NOT passed via the dbt action's own with.env field: as of
// this writing, giving with.env a value that needs Dagu's own reference
// resolution (for example, a literal path from a Go test rather than a
// hardcoded constant) fails the action's input schema the same way
// documented for node-script@v1's with.env (Spec 060) -- the field is
// normalized into Dagu's own list-of-single-key-mappings env: shape
// whenever value resolution touches it, which collides with the action's
// own plain-object input schema. Writing profiles.yml directly sidesteps
// with.env entirely.
func dbtPersistentProfileEnv(dagu *harness.Runner) []string {
	dbPath := dagu.ProjectPath("artifacts/dev.duckdb")
	dagu.WriteFile("dbt_profiles_persistent/profiles.yml", fmt.Sprintf(`conformance_project:
  target: dev
  outputs:
    dev:
      type: duckdb
      path: %q
      threads: 1
`, dbPath))

	env := dbtProjectEnv(dagu)
	for i, entry := range env {
		if strings.HasPrefix(entry, "HOST_PROFILES_DIR=") {
			env[i] = "HOST_PROFILES_DIR=" + dagu.ProjectPath("dbt_profiles_persistent")
		}
	}
	return env
}

func TestDbtLive(t *testing.T) {
	// No subtest below uses t.Parallel(): each calls t.Setenv to raise the
	// harness's per-command timeout past the ~45s a cold dbt@v1 invocation
	// (first-time git clone plus uv/Python/dbt/DuckDB provisioning) can
	// take, and t.Setenv itself forbids t.Parallel() in the same test.
	t.Run("dbt runs against a real DuckDB-backed project, with structured flags, env, and conditional artifact paths", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(dbtPersistentProfileEnv(dagu), "start", "happy_path.yaml")
		result.ExpectExitCode(0)

		var seed struct {
			OK       bool `json:"ok"`
			ExitCode int  `json:"exitCode"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 0)), &seed))
		require.True(t, seed.OK)
		require.Zero(t, seed.ExitCode)

		// with.select/with.vars/with.threads/with.fullRefresh/with.args are
		// structured inputs appended, in that order, to the real dbt argv
		// the action reports back as its own "command" output field.
		var build struct {
			OK             bool     `json:"ok"`
			ExitCode       int      `json:"exitCode"`
			Command        []string `json:"command"`
			ManifestPath   string   `json:"manifestPath"`
			RunResultsPath string   `json:"runResultsPath"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 1)), &build))
		require.True(t, build.OK)
		require.Zero(t, build.ExitCode)
		require.NotEmpty(t, build.ManifestPath)
		require.NotEmpty(t, build.RunResultsPath)
		expectedTail := []string{
			"--select", "my_model",
			"--vars", `{"demo":1}`,
			"--threads", "2",
			"--full-refresh",
			"--no-partial-parse",
		}
		require.GreaterOrEqual(t, len(build.Command), len(expectedTail))
		require.Equal(t, expectedTail, build.Command[len(build.Command)-len(expectedTail):])

		var docsGen struct {
			OK          bool   `json:"ok"`
			CatalogPath string `json:"catalogPath"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 2)), &docsGen))
		require.True(t, docsGen.OK)
		require.NotEmpty(t, docsGen.CatalogPath, "dbt docs generate should have produced catalog.json under targetPath")

		var freshness struct {
			OK          bool   `json:"ok"`
			SourcesPath string `json:"sourcesPath"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 3)), &freshness))
		require.True(t, freshness.OK, "dbt source freshness should pass against the seed step's own raw_events table, loaded into the DuckDB file with.env.DBT_DB_PATH names")
		require.NotEmpty(t, freshness.SourcesPath)
	})

	t.Run("a bad project dir, a missing adapter, an unresolvable requirement, or a timeout reports ok:false via exitCode/stderr, not a synthetic error object", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(dbtProjectEnv(dagu), "start", "error_scenarios.yaml")
		result.ExpectNonZeroExitCode()

		// with.projectDir missing is caught by the action's own inputs
		// schema before it ever invokes uv or dbt, so this step's error
		// appears directly in dagu's own output, with no stdout log of its
		// own to read (unlike the four scenarios below, each of which is a
		// real dbt/uv process that ran and exited non-zero).
		require.Contains(t, result.Stdout(), `missing properties: ["projectDir"]`)

		var badProjectDir struct {
			OK       bool   `json:"ok"`
			ExitCode int    `json:"exitCode"`
			Stderr   string `json:"stderr"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 0)), &badProjectDir))
		require.False(t, badProjectDir.OK)
		require.Contains(t, badProjectDir.Stderr, "does not exist")

		var noAdapter struct {
			OK       bool   `json:"ok"`
			ExitCode int    `json:"exitCode"`
			Stdout   string `json:"stdout"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 1)), &noAdapter))
		require.False(t, noAdapter.OK, "with.requirements defaults to [dbt-core], which has no DuckDB adapter installed")
		require.Contains(t, noAdapter.Stdout, "Could not find adapter type duckdb")

		var badRequirement struct {
			OK     bool   `json:"ok"`
			Stderr string `json:"stderr"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 2)), &badRequirement))
		require.False(t, badRequirement.OK, "an unresolvable with.requirements entry fails uv run --with itself, before dbt ever starts")
		require.Contains(t, badRequirement.Stderr, "No solution found")

		var timedOut struct {
			OK       bool   `json:"ok"`
			ExitCode int    `json:"exitCode"`
			Stderr   string `json:"stderr"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout(), 3)), &timedOut))
		require.False(t, timedOut.OK, "with.timeoutSeconds should have cut the run short")
		require.Equal(t, 124, timedOut.ExitCode)
		require.Contains(t, timedOut.Stderr, "dbt command timed out after 5s")
	})

	// Like node-script@v1 and python-script@v1, a later step reads a result
	// field as ${<step id>.outputs.<path>} -- a bare-step-id reference, not
	// ${steps.<step id>.outputs.<name>} -- confirming this is a property of
	// remote action:-type steps generally, not specific to one action.
	t.Run("a later step reads a result field as ${<step id>.outputs.<name>}, but not via steps.<id>.outputs.<name>", func(t *testing.T) {
		t.Setenv("DAGU_CONFORMANCE_COMMAND_TIMEOUT", "3m")

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(dbtProjectEnv(dagu), "start", "downstream_reference.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("bad substitution")

		require.Equal(t, "bare exit code is 0\n", stepStdout(t, result.Stdout(), 1))
	})
}

// TestDbtValidation proves that dagu validate never resolves a remote action
// reference (which would require network access): a dbt@v1 step passes
// validate regardless of its with: content, and even with.projectDir --
// required by the action's own inputs schema -- is enforced only once the
// step actually runs and that schema is fetched. The one thing validate does
// check locally is the action reference's own syntax.
func TestDbtValidation(t *testing.T) {
	t.Parallel()

	t.Run("a dbt@v1 step with no with.projectDir passes validate", func(t *testing.T) {
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
		result.ExpectStderrContains(`unknown action "dbt"`)
	})
}
