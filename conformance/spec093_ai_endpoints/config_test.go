// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec093_ai_endpoints_test

import (
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// Spec 093, "Configuration": the block ai is closed and strict. An unknown key, a
// duplicate key, a value of the wrong type, a value out of range and a
// contradiction of two keys stop Dagu at start with the exit code 1. The error
// names the key, the expected type or range, and the value. No value is changed to
// make it fit.
func TestAIConfigIsClosedAndStrict(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message []string
	}{
		{"config_ai_unknown_key.yaml", []string{`unknown key "ai.models_timeout"`, "valid keys are models_refresh_hours, models_timeout_sec, relay, upstream"}},
		{"config_ai_unknown_relay_key.yaml", []string{`unknown key "ai.relay.max_stream"`, "in the block ai.relay", "valid keys are connect_timeout_sec, idle_timeout_sec"}},
		{"config_ai_api_key.yaml", []string{`unknown key "ai.api_key"`}},
		{"config_ai_wrong_type.yaml", []string{`key "ai.models_timeout_sec"`, "an integer from 1 to 300", `got "30"`}},
		{"config_ai_out_of_range.yaml", []string{`key "ai.relay.max_streams_per_run"`, "an integer from 1 to 256", "got 0"}},
		{"config_ai_above_range.yaml", []string{`key "ai.relay.max_header_bytes"`, "an integer from 1024 to 1048576", "got 1048577"}},
		{"config_ai_duplicate_key.yaml", []string{"max_streams"}},
		{"config_ai_proxy_scheme.yaml", []string{`key "ai.upstream.proxy_url"`, "an http or https URL with no user information", "socks5://proxy.example.com:1080"}},
		{"config_ai_proxy_user.yaml", []string{`key "ai.upstream.proxy_url"`, "user:xxxxx@proxy.example.com"}},
		{"config_ai_contradiction_streams.yaml", []string{
			`keys "ai.relay.max_streams_per_run" and "ai.relay.max_streams" contradict each other`,
			`"ai.relay.max_streams_per_run" is 10`, `"ai.relay.max_streams", which is 9`}},
		{"config_ai_contradiction_header_timeout.yaml", []string{
			`keys "ai.relay.response_header_timeout_sec" and "ai.relay.total_timeout_sec" contradict each other`}},
		{"config_ai_contradiction_idle_timeout.yaml", []string{
			`keys "ai.relay.idle_timeout_sec" and "ai.relay.total_timeout_sec" contradict each other`}},
		// A default can be the other side of the pair: the default of max_streams_per_run is 8.
		{"config_ai_contradiction_default.yaml", []string{
			`"ai.relay.max_streams_per_run" is 8`, `"ai.relay.max_streams", which is 4`}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("config", "--config", dagu.ProjectPath(tc.fixture))
			result.ExpectExitCode(1)
			result.ExpectStderrContains(tc.message...)
			if strings.Contains(tc.fixture, "proxy_user") {
				result.ExpectStderrNotContains("hunter2")
			}
		})
	}
}

// A process that reads the block stops at start for an invalid value, before it
// serves. The server writes the error and exits with the code 1.
func TestServerStopsForAnInvalidAIBlock(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	proc := dagu.StartWithEnv([]string{"DAGU_HOME=" + t.TempDir()},
		"server", "--host=127.0.0.1", "--port=0", "--config", dagu.ProjectPath("config_ai_contradiction_streams.yaml"))
	select {
	case <-proc.Done():
	case <-time.After(harness.WaitTimeout(t)):
		require.FailNow(t, "the server did not stop", "%s", proc.Output())
	}
	require.Equal(t, 1, proc.ExitCode(), proc.Output())
	require.Contains(t, proc.Output(), `keys "ai.relay.max_streams_per_run" and "ai.relay.max_streams" contradict each other`)
}

// Spec 093, "Configuration": no environment variable sets a key of the block. The
// file is the only source.
func TestAIConfigHasNoEnvironmentBinding(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{
		"DAGU_AI_MODELS_REFRESH_HOURS=3", "DAGU_AI_RELAY_MAX_STREAMS=3",
		"DAGU_AI_UPSTREAM_PROXY_URL=http://env-proxy.example.com", "HTTPS_PROXY=http://env-proxy.example.com",
	}, "config", "--config", dagu.ProjectPath("config_ai_default.yaml"))
	result.ExpectExitCode(0)
	out := strings.Join(strings.Fields(result.Stdout()), " ")
	for _, want := range []string{
		"ai.models_refresh_hours: 24 default",
		"ai.upstream.proxy_url: (empty) default",
		"ai.relay.max_streams: 128 default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("an environment variable changed the block ai: %q is not in\n%s", want, result.Stdout())
		}
	}
}

// Spec 093, "Configuration": dagu config prints each key of the block with the value
// that applies and its source, the file or the default.
func TestConfigCommandPrintsTheEffectiveAIBlock(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("config", "--config", dagu.ProjectPath("config_ai_ok.yaml"))
	result.ExpectExitCode(0)
	out := strings.Join(strings.Fields(result.Stdout()), " ")
	for _, want := range []string{
		"ai.models_refresh_hours: 12 file",
		"ai.models_timeout_sec: 10 file",
		"ai.upstream.proxy_url: http://proxy.example.com:3128 file",
		"ai.relay.max_request_bytes: 2048 file",
		"ai.relay.max_response_bytes: 4096 file",
		"ai.relay.max_header_bytes: 2048 file",
		"ai.relay.max_streams_per_run: 2 file",
		"ai.relay.max_streams: 4 file",
		"ai.relay.connect_timeout_sec: 3 file",
		"ai.relay.response_header_timeout_sec: 20 file",
		"ai.relay.idle_timeout_sec: 30 file",
		"ai.relay.total_timeout_sec: 60 file",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dagu config does not print %q\n%s", want, result.Stdout())
		}
	}

	defaults := dagu.Run("config", "--config", dagu.ProjectPath("config_ai_default.yaml"))
	defaults.ExpectExitCode(0)
	out = strings.Join(strings.Fields(defaults.Stdout()), " ")
	for _, want := range []string{
		"ai.models_refresh_hours: 24 default",
		"ai.models_timeout_sec: 30 default",
		"ai.upstream.proxy_url: (empty) default",
		"ai.relay.max_request_bytes: 16777216 default",
		"ai.relay.max_response_bytes: 268435456 default",
		"ai.relay.max_header_bytes: 32768 default",
		"ai.relay.max_streams_per_run: 8 default",
		"ai.relay.max_streams: 128 default",
		"ai.relay.connect_timeout_sec: 10 default",
		"ai.relay.response_header_timeout_sec: 600 default",
		"ai.relay.idle_timeout_sec: 300 default",
		"ai.relay.total_timeout_sec: 3600 default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dagu config does not print %q\n%s", want, defaults.Stdout())
		}
	}
}
