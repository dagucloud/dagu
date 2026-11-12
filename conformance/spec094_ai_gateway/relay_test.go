// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec094_ai_gateway_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/aitest"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 094, "The gateway", end to end: a coordinator with mutual TLS, an agent worker
// with the capability ai_relay.v1 and a fake provider. The fake CLI calls
// ANTHROPIC_BASE_URL with ANTHROPIC_API_KEY, as the real CLI does. A subtest runs one
// scenario. Each subtest starts an agent worker, which takes one run and exits.
//
// The test needs the user ID 0, like the tests of Spec 092 with an agent worker. Run it
// as root, for example in a container with no-new-privileges.
func TestRelayOnAnAgentWorker(t *testing.T) {
	st := aitest.StartStack(t, "relay_coordinator_config.yaml")
	provider := startFakeUpstream(t)
	st.CreateEndpoint(provider.URL(), aitest.RealKey, "relay-run")

	// run starts a worker and a run of the DAG, and waits for the final status.
	runIt := func(t *testing.T, dag, id string) aitest.RunDetails {
		t.Helper()
		worker := st.StartAgentWorker("agent-" + id)
		st.Enqueue(dag+".yaml", id)
		details := st.WaitRun(dag, id)
		st.ExpectWorkerExit(worker)
		return details
	}

	// The key reaches the provider and no other place. The first event reaches the
	// CLI before the provider ends its answer.
	t.Run("StreamsWithTheRealKey", func(t *testing.T) {
		st.Scenario("stream", nil)
		worker := st.StartAgentWorker("agent-stream")
		st.Enqueue("relay-run.yaml", "stream")
		st.WaitFile("first_chunk")
		firstAt := aitest.UnixNanoFile(t, filepath.Join(st.Dir, "first_chunk"))

		// The provider has not finished: the first chunk came through the relay at once.
		releasedAt := time.Now()
		details, ok := st.Details("relay-run", "stream")
		require.True(t, ok)
		assert.False(t, details.Finished(), "the run is still open while the provider streams")
		assert.True(t, firstAt.Before(releasedAt), "the CLI had its first chunk before the provider ended")
		requests := provider.all()
		require.Len(t, requests, 1)
		t.Logf("first chunk after %s; the provider has not ended", firstAt.Sub(requests[0].Started))
		provider.releaseNow()

		final := st.WaitRun("relay-run", "stream")
		st.ExpectWorkerExit(worker)
		require.Equal(t, "succeeded", final.DAGRunDetails.StatusLabel, final.ErrorText())

		// The provider got the real key and the headers of the allowed list.
		require.Equal(t, []string{aitest.RealKey}, provider.keys())
		assert.Equal(t, "2023-06-01", requests[0].Header.Get("Anthropic-Version"))
		assert.Equal(t, "/v1/messages", requests[0].Path)
		assert.Equal(t, "identity", requests[0].Header.Get("Accept-Encoding"))

		var cliEnv map[string]string
		require.NoError(t, json.Unmarshal([]byte(st.Read("cli_env.json")), &cliEnv))
		placeholder := cliEnv["ANTHROPIC_API_KEY"]
		assert.NotEmpty(t, placeholder)
		assert.NotEqual(t, aitest.RealKey, placeholder, "the CLI has the relay secret, not the key")
		assert.True(t, strings.HasPrefix(cliEnv["ANTHROPIC_BASE_URL"], "http://127.0.0.1:"), cliEnv["ANTHROPIC_BASE_URL"])
		for _, key := range []string{"NO_PROXY", "no_proxy"} {
			assert.Contains(t, strings.Split(cliEnv[key], ","), "127.0.0.1", key)
			assert.Contains(t, strings.Split(cliEnv[key], ","), "localhost", key)
		}
		envText := st.Read("cli_env.json") + st.Read("cli_argv.json")
		assert.NotContains(t, envText, aitest.RealKey, "the CLI never sees the key")

		var stream struct {
			Status int `json:"status"`
			Chunks int `json:"chunks"`
		}
		require.NoError(t, json.Unmarshal([]byte(st.Read("stream.json")), &stream))
		assert.Equal(t, 200, stream.Status)
		assert.GreaterOrEqual(t, stream.Chunks, 2, "the events arrive as separate chunks")

		// The key is in no log, no output, no file of any home, and no record.
		st.AssertKeyNowhere(aitest.RealKey, placeholder)

		// One audit line for each request, with no key (Spec 094, "Audit").
		out := st.Services.Output()
		assert.Contains(t, out, "action=ai_relay_request")
		assert.Contains(t, out, "route=messages")
		assert.Contains(t, out, "target=test-endpoint")
		assert.Contains(t, out, "key_fingerprint="+aitest.Fingerprint(aitest.RealKey))
		assert.Contains(t, out, "dag=relay-run")
		assert.Contains(t, out, "step=code")
	})

	t.Run("RefusesWhatIsNotAllowed", func(t *testing.T) {
		st.Scenario("hostile", nil)
		before := len(provider.all())
		details := runIt(t, "relay-run", "hostile")
		require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())
		var results map[string]any
		require.NoError(t, json.Unmarshal([]byte(st.Read("hostile.json")), &results))
		assert.EqualValues(t, 200, results["allowed"])
		for name, want := range map[string]int{
			"other_path": 404, "admin_path": 404, "openai_path": 404,
			"other_method": 405, "delete_method": 405, "other_query": 404,
			"no_secret": 401, "wrong_secret": 401,
			"host_header": 403, "absolute_form": 403, "absolute_form_relay": 403, "connect": 403,
		} {
			assert.EqualValues(t, want, results[name], name)
		}
		assert.Len(t, provider.all(), before+1, "only the allowed request reaches the provider")
	})

	t.Run("StepStopCancelsTheProviderCall", func(t *testing.T) {
		st.Scenario("block", nil)
		worker := st.StartAgentWorker("agent-block")
		st.Enqueue("relay-run.yaml", "block")
		select {
		case <-provider.started:
		case <-time.After(60 * time.Second):
			t.Fatalf("the provider got no call\n%s", st.Services.Output())
		}
		st.WaitFile("first_chunk")
		status, body := st.Server.Call(http.MethodPost, "/dag-runs/relay-run/block/stop", nil)
		require.Equal(t, http.StatusOK, status, body)
		select {
		case <-provider.cancelled:
		case <-time.After(60 * time.Second):
			t.Fatalf("the call to the provider kept running after the stop\n%s", st.Services.Output())
		}
		final := st.WaitRun("relay-run", "block")
		assert.NotEqual(t, "succeeded", final.DAGRunDetails.StatusLabel)
		st.ExpectWorkerExit(worker)
	})
}

// A large answer and an answer of many small events cross the gateway whole, in
// order and in bounded memory (Spec 094, "Stream, cancel and backpressure"). A large
// request body crosses it in chunks.
func TestRelayLargeAndManyChunks(t *testing.T) {
	st := aitest.StartStack(t, "relay_coordinator_config.yaml")
	provider := startFakeUpstream(t)
	st.CreateEndpoint(provider.URL(), aitest.RealKey, "relay-run")
	runIt := func(t *testing.T, id string) aitest.RunDetails {
		t.Helper()
		worker := st.StartAgentWorker("agent-" + id)
		st.Enqueue("relay-run.yaml", id)
		details := st.WaitRun("relay-run", id)
		st.ExpectWorkerExit(worker)
		return details
	}

	t.Run("LargeAnswer", func(t *testing.T) {
		const mib = 64
		st.Scenario("large", map[string]string{"large_mib": "64"})
		details := runIt(t, "large")
		require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())
		var large struct {
			Status  int     `json:"status"`
			Bytes   int     `json:"bytes"`
			SHA256  string  `json:"sha256"`
			Chunks  int     `json:"chunks"`
			Seconds float64 `json:"seconds"`
		}
		require.NoError(t, json.Unmarshal([]byte(st.Read("large.json")), &large))
		wantBytes, wantDigest := expectedLarge(mib)
		assert.Equal(t, 200, large.Status)
		assert.Equal(t, wantBytes, large.Bytes)
		assert.Equal(t, wantDigest, large.SHA256, "each byte arrives, in order")
		t.Logf("large answer: %d MiB in %.2f s, %d chunks at the CLI", mib, large.Seconds, large.Chunks)
	})

	t.Run("ManyChunks", func(t *testing.T) {
		st.Scenario("many", nil)
		details := runIt(t, "many")
		require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())
		var many struct {
			Status int    `json:"status"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
			Chunks int    `json:"chunks"`
		}
		require.NoError(t, json.Unmarshal([]byte(st.Read("many.json")), &many))
		wantBytes, wantDigest := expectedMany(20000)
		assert.Equal(t, 200, many.Status)
		assert.Equal(t, wantBytes, many.Bytes)
		assert.Equal(t, wantDigest, many.SHA256)
		t.Logf("20000 events: %d chunks at the CLI", many.Chunks)
	})

	t.Run("LargeRequestBody", func(t *testing.T) {
		st.Scenario("body", map[string]string{"body_mib": "5"})
		details := runIt(t, "body")
		require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())
		var body struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		}
		require.NoError(t, json.Unmarshal([]byte(st.Read("body.json")), &body))
		assert.Equal(t, 200, body.Status)
		var echoed struct {
			Bytes int `json:"bytes"`
		}
		require.NoError(t, json.Unmarshal([]byte(body.Body), &echoed))
		assert.Greater(t, echoed.Bytes, 5*1024*1024)
	})
}

// Spec 094, "Identity checks" and "Start checks": a DAG that is not in the allow list
// fails with endpoint_forbidden, and an unknown name with endpoint_unknown, before
// the CLI starts. No request goes to the provider. A deleted endpoint fails the next
// request, and a rotated key applies to the next run.
func TestRelayRefusalsAndKeyChanges(t *testing.T) {
	st := aitest.StartStack(t, "relay_coordinator_config.yaml")
	provider := startFakeUpstream(t)
	st.CreateEndpoint(provider.URL(), aitest.RealKey, "relay-run")
	runIt := func(t *testing.T, dag, id string) aitest.RunDetails {
		t.Helper()
		worker := st.StartAgentWorker("agent-" + id)
		st.Enqueue(dag+".yaml", id)
		details := st.WaitRun(dag, id)
		st.ExpectWorkerExit(worker)
		return details
	}

	t.Run("ForbiddenDAG", func(t *testing.T) {
		st.Scenario("ok", nil)
		details := runIt(t, "relay-forbidden", "forbidden")
		assert.Equal(t, "failed", details.DAGRunDetails.StatusLabel)
		assert.Contains(t, details.ErrorText(), `endpoint "test-endpoint": endpoint_forbidden`)
		assert.Contains(t, details.ErrorText(), `the DAG "relay-forbidden" is not in the allow list`)
		assert.Zero(t, st.RanCount(), "the CLI did not start")
		assert.Empty(t, provider.all(), "no request went to the provider")
	})

	t.Run("UnknownEndpoint", func(t *testing.T) {
		st.Scenario("ok", nil)
		details := runIt(t, "relay-unknown", "unknown")
		assert.Equal(t, "failed", details.DAGRunDetails.StatusLabel)
		assert.Contains(t, details.ErrorText(), `endpoint "nope": endpoint_unknown`)
		assert.Zero(t, st.RanCount(), "the CLI did not start")
		assert.Empty(t, provider.all())
	})

	t.Run("RotationAppliesToTheNextRun", func(t *testing.T) {
		const second = "sk-ant-api03-SECOND-KEY-9876543210zyxwvutsrqponm"
		st.Scenario("ok", nil)
		require.Equal(t, "succeeded", runIt(t, "relay-run", "rotate-1").DAGRunDetails.StatusLabel)
		status, body := st.Server.Call(http.MethodPut, "/ai/endpoints/test-endpoint/key", map[string]string{"key": second})
		require.Equal(t, http.StatusOK, status, body)
		st.Scenario("ok", nil)
		require.Equal(t, "succeeded", runIt(t, "relay-run", "rotate-2").DAGRunDetails.StatusLabel)
		assert.Equal(t, []string{aitest.RealKey, second}, provider.keys())
		st.AssertKeyNowhere(aitest.RealKey)
		st.AssertKeyNowhere(second)
	})

	t.Run("UpstreamStatusPasses", func(t *testing.T) {
		st.Scenario("status", nil)
		require.Equal(t, "succeeded", runIt(t, "relay-run", "status").DAGRunDetails.StatusLabel)
		var results []struct {
			Status     int     `json:"status"`
			RetryAfter *string `json:"retry_after"`
			SetCookie  *string `json:"set_cookie"`
		}
		require.NoError(t, json.Unmarshal([]byte(st.Read("status.json")), &results))
		require.Len(t, results, 2)
		assert.Equal(t, 401, results[0].Status, "a 401 of the provider is a 401 for the client")
		assert.Equal(t, 429, results[1].Status)
		require.NotNil(t, results[1].RetryAfter)
		assert.Equal(t, "17", *results[1].RetryAfter, "retry-after reaches the client")
		assert.Nil(t, results[1].SetCookie, "set-cookie does not")
	})

	t.Run("RedirectIsAnUpstreamError", func(t *testing.T) {
		st.Scenario("redirect", nil)
		require.Equal(t, "succeeded", runIt(t, "relay-run", "redirect").DAGRunDetails.StatusLabel)
		var result struct {
			Status   int     `json:"status"`
			Body     string  `json:"body"`
			Location *string `json:"location"`
		}
		require.NoError(t, json.Unmarshal([]byte(st.Read("redirect.json")), &result))
		assert.Equal(t, 502, result.Status)
		assert.Contains(t, result.Body, "upstream_error")
		assert.Nil(t, result.Location)
		assert.Zero(t, provider.spyHits(), "the coordinator calls the host of the base URL only")
	})

	t.Run("DeletedEndpointFailsTheNextRequest", func(t *testing.T) {
		st.Scenario("delete", nil)
		worker := st.StartAgentWorker("agent-delete")
		st.Enqueue("relay-run.yaml", "delete")
		st.WaitFile("first_done")
		status, body := st.Server.Call(http.MethodDelete, "/ai/endpoints/test-endpoint", nil)
		require.Equal(t, http.StatusNoContent, status, body)
		st.Write("go", "1")
		final := st.WaitRun("relay-run", "delete")
		st.ExpectWorkerExit(worker)
		require.Equal(t, "succeeded", final.DAGRunDetails.StatusLabel, final.ErrorText())
		var first, secondResult struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		}
		require.NoError(t, json.Unmarshal([]byte(st.Read("first.json")), &first))
		require.NoError(t, json.Unmarshal([]byte(st.Read("second.json")), &secondResult))
		assert.Equal(t, 200, first.Status)
		assert.Equal(t, 404, secondResult.Status)
		assert.Contains(t, secondResult.Body, "endpoint_unknown")
	})
}

// Spec 093, "Configuration", and Spec 094, "Limits and timeouts": a body over
// ai.relay.max_request_bytes gives request_too_large, and an answer over
// ai.relay.max_response_bytes ends the stream.
func TestRelayLimits(t *testing.T) {
	st := aitest.StartStack(t, "relay_limits_config.yaml")
	provider := startFakeUpstream(t)
	st.CreateEndpoint(provider.URL(), aitest.RealKey, "relay-run")
	st.Scenario("limits", nil)
	worker := st.StartAgentWorker("agent-limits")
	st.Enqueue("relay-run.yaml", "limits")
	details := st.WaitRun("relay-run", "limits")
	st.ExpectWorkerExit(worker)
	require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())

	var results struct {
		BigRequest struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"big_request"`
		BigResponse struct {
			Status int    `json:"status"`
			Bytes  int    `json:"bytes"`
			Error  string `json:"error"`
		} `json:"big_response"`
	}
	require.NoError(t, json.Unmarshal([]byte(st.Read("limits.json")), &results))
	assert.Equal(t, 413, results.BigRequest.Status)
	assert.Contains(t, results.BigRequest.Body, "request_too_large")
	assert.Equal(t, 200, results.BigResponse.Status, "the head was sent before the cut")
	assert.NotEmpty(t, results.BigResponse.Error, "the client sees a broken stream, not a clean end")
	assert.LessOrEqual(t, results.BigResponse.Bytes, 4096)
	for _, request := range provider.all() {
		assert.Less(t, request.Bytes, 2048, "the provider got no request over the limit")
	}
}

// Spec 094, "Local mode": a run with no coordinator and no worker calls the gateway in
// its own process, with no gRPC stream. The process reads the endpoint store of its
// data directory. The checks of the start apply as for a worker.
func TestRelayLocalMode(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(t)
	provider := startFakeUpstream(t)
	srv := aitest.StartServer(t, runner, nil)
	// The local runs use the data directory of the server: it holds the endpoints.
	dagu := aitest.NewLocalProject(t, runner, srv.Home)

	status, body := srv.Call(http.MethodPost, "/ai/endpoints", map[string]any{
		"name": "test-endpoint", "preset": "custom", "protocol": "anthropic", "baseUrl": provider.URL(),
		"auth": "x-api-key", "scope": "global",
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": []string{"agent-local"}}, "key": aitest.RealKey,
	})
	require.Equal(t, http.StatusCreated, status, body)

	t.Run("RunsThroughTheRelay", func(t *testing.T) {
		dagu.Scenario(t, "stream")
		provider.releaseNow()
		result := dagu.Start("agent-local.yaml")
		result.ExpectExitCode(0)
		assert.Equal(t, []string{aitest.RealKey}, provider.keys(), "the provider gets the real key")

		var cliEnv map[string]string
		require.NoError(t, json.Unmarshal([]byte(dagu.Read(t, "cli_env.json")), &cliEnv))
		assert.NotEqual(t, aitest.RealKey, cliEnv["ANTHROPIC_API_KEY"])
		assert.NotContains(t, dagu.Read(t, "cli_env.json"), aitest.RealKey)
		assert.True(t, strings.HasPrefix(cliEnv["ANTHROPIC_BASE_URL"], "http://127.0.0.1:"))
		assert.Contains(t, strings.Split(cliEnv["NO_PROXY"], ","), "127.0.0.1")
		srv.AssertNoKey(aitest.RealKey)
		assert.NotContains(t, result.Stdout()+result.Stderr(), aitest.RealKey)
		assert.NotContains(t, result.Stdout()+result.Stderr(), cliEnv["ANTHROPIC_API_KEY"], "the relay secret is masked")
	})

	t.Run("ForbiddenDAG", func(t *testing.T) {
		dagu.Scenario(t, "ok")
		before := dagu.RanCount()
		result := dagu.Start("agent-local-forbidden.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`endpoint "test-endpoint": endpoint_forbidden`, `the DAG "agent-local-forbidden" is not in the allow list`)
		assert.Equal(t, before, dagu.RanCount(), "the CLI did not start")
	})

	t.Run("RelayRefusesOtherPaths", func(t *testing.T) {
		dagu.Scenario(t, "hostile")
		result := dagu.Start("agent-local.yaml")
		result.ExpectExitCode(0)
		var results map[string]any
		require.NoError(t, json.Unmarshal([]byte(dagu.Read(t, "hostile.json")), &results))
		assert.EqualValues(t, 200, results["allowed"])
		assert.EqualValues(t, 404, results["other_path"])
		assert.EqualValues(t, 401, results["wrong_secret"])
		assert.EqualValues(t, 403, results["connect"])
	})

	t.Run("DeletedEndpointFailsTheStart", func(t *testing.T) {
		status, body := srv.Call(http.MethodDelete, "/ai/endpoints/test-endpoint", nil)
		require.Equal(t, http.StatusNoContent, status, body)
		dagu.Scenario(t, "ok")
		before := dagu.RanCount()
		result := dagu.Start("agent-local.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`endpoint "test-endpoint": endpoint_unknown`)
		assert.Equal(t, before, dagu.RanCount(), "the CLI did not start")
	})
}
