// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec057_redis holds black-box conformance tests for
// Spec 057: Redis Actions.
package spec057_redis_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestRedisLive(t *testing.T) {
	dockerClient := requireDockerDaemon(t)
	port := startRedisContainer(t, dockerClient)
	redisURL := fmt.Sprintf("REDIS_URL=redis://127.0.0.1:%d", port)

	env := func(prefix string) []string {
		return []string{redisURL, "REDIS_PREFIX=" + prefix}
	}

	t.Run("a value written by redis.set is read back by redis.get", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "set_get_roundtrip.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"hello redis\"\n", lastStepStdout(t, result.Stdout()))
	})

	// redis.get on a key that does not exist succeeds, but the executor
	// never calls its result writer for a nil top-level result -- so no
	// output is produced at all, not even with.null_value's configured
	// placeholder (which only applies to a nil found inside a non-nil
	// array or map result; see the HMGET test below).
	t.Run("redis.get on a nonexistent key succeeds but writes no output", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "get_missing_key.yaml")
		result.ExpectExitCode(0)
		require.True(t, hasNoStepStdout(result.Stdout()), "expected no stdout log line for a nil single-command result:\n%s", result.Stdout())
	})

	t.Run("with.null_value substitutes for a nil element inside a non-nil array result", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "hmget_partial_null.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "Alice\nN/A\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a DAG-level redis: block supplies connection defaults a step's with: omits", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "dag_level_defaults.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"OK\"\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a step's own with.url overrides an unreachable DAG-level default", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "step_level_override.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"OK\"\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("with.pipeline runs every queued command and collects their results into one array", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "pipeline_basic.yaml")
		result.ExpectExitCode(0)

		var results []string
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &results))
		require.Equal(t, []string{"OK", "OK", "v1", "v2"}, results)
	})

	// with.pipeline takes priority over the command the action name (or an
	// explicit with.command) would otherwise select: the metrics command
	// recorded is "PIPELINE", and the queued pipeline commands run instead
	// of a GET.
	t.Run("with.pipeline runs instead of the action-derived command when both are present", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "pipeline_command_overrides_action.yaml")
		result.ExpectExitCode(0)

		var results []string
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &results))
		require.Equal(t, []string{"OK"}, results)
	})

	// Inside a pipeline, a GET on a missing key surfaces as an empty
	// string in the results array, not JSON null -- unlike the same GET
	// run as a single command, which suppresses all output (see above).
	t.Run("a nil result inside a pipeline is an empty string, not null", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "pipeline_with_nil.yaml")
		result.ExpectExitCode(0)

		var results []string
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &results))
		require.Equal(t, []string{"OK", "", "hello"}, results)
	})

	t.Run("a failing command anywhere in with.pipeline fails the whole step", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "pipeline_command_error_fails_step.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("pipeline execution failed")
	})

	t.Run("with.script runs a Lua script against the connected Redis instance", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "script_basic.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"scripted-value\"\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("with.lock is released after the step completes", func(t *testing.T) {
		t.Parallel()

		prefix := uniqueSuffix(t) + "-"
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(prefix), "start", "lock_acquired_and_released.yaml")
		result.ExpectExitCode(0)

		require.False(t, redisKeyExists(t, port, "dagu:lock:"+prefix+"mylock"), "expected the lock key to be deleted once the step finished")
	})

	t.Run("with.lock fails fast when the lock is already held by another process", func(t *testing.T) {
		t.Parallel()

		prefix := uniqueSuffix(t) + "-"
		setRedisKeyExternally(t, port, "dagu:lock:"+prefix+"mylock", "someone-else", 30*time.Second)

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(prefix), "start", "lock_held_by_other.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("lock is held by another process")
	})

	t.Run("with.output_format csv renders a list result as one comma-separated line", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "list_csv_format.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "a,b,c\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("with.output_format jsonl renders a list result as one JSON value per line", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env(uniqueSuffix(t)+"-"), "start", "list_jsonl_format.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"a\"\n\"b\"\n\"c\"\n", lastStepStdout(t, result.Stdout()))
	})
}

// TestRedisValidation proves that a mismatched with.command, an invalid
// with.mode/with.output_format value, and an out-of-range with.port/with.db
// are all rejected by the registered JSON Schema (or, for with.command, by
// the action normalizer) at DAG-build time -- while sentinel/cluster mode's
// required fields and tls_cert/tls_key pairing are cross-field checks the
// schema cannot express, so they surface only when the step runs.
func TestRedisValidation(t *testing.T) {
	t.Parallel()

	t.Run("with.command that disagrees with the redis.<command> action name fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "command_mismatch.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`command must be "GET" for this action`)
	})

	t.Run("an unrecognized with.mode fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "invalid_mode.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("enum")
	})

	t.Run("an out-of-range with.port fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "port_out_of_range.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("maximum")
	})

	t.Run("an out-of-range with.db fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "db_out_of_range.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("maximum")
	})

	t.Run("an unrecognized with.output_format fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "invalid_output_format.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("enum")
	})

	t.Run("sentinel mode without sentinel_master/sentinel_addrs fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "sentinel_missing_fields.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "sentinel_missing_fields.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("sentinel_master is required for sentinel mode")
	})

	t.Run("cluster mode without cluster_addrs fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "cluster_missing_fields.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "cluster_missing_fields.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("cluster_addrs is required for cluster mode")
	})

	t.Run("with.tls_cert without with.tls_key fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "tls_cert_without_key.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "tls_cert_without_key.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("both tls_cert and tls_key must be provided together")
	})
}
