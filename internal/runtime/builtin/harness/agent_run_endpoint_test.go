// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// theRealKey is the key that only the fake gateway knows. The harness never gets
// it, so it can never reach the CLI, a file or a log.
const theRealKey = "sk-ant-api03-THE-REAL-KEY-0123456789"

// fakeGateway stands for the coordinator or for the local gateway.
type fakeGateway struct {
	mu       sync.Mutex
	checkErr error
	relayErr error
	steps    []aigateway.StepRef
	targets  []aigateway.Target
	provider string
	// model is the model of the last start check, and modelChecks counts the checks
	// that carried a model.
	model       string
	modelChecks int
	requests    []aigateway.HTTPRequest
	// relayTargets are the targets of the requests that the relay carried.
	relayTargets []aigateway.Target
}

func (g *fakeGateway) Check(ctx context.Context, step aigateway.StepRef, target aigateway.Target, provider string) (aigateway.CheckResult, error) {
	return g.checkWith(step, target, provider, "")
}

// CheckModel makes the gateway a aigateway.ModelClient.
func (g *fakeGateway) CheckModel(_ context.Context, step aigateway.StepRef, target aigateway.Target, provider, model string) (aigateway.CheckResult, error) {
	return g.checkWith(step, target, provider, model)
}

func (g *fakeGateway) checkWith(step aigateway.StepRef, target aigateway.Target, provider, model string) (aigateway.CheckResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.model = model
	if model != "" {
		g.modelChecks++
	}
	g.steps = append(g.steps, step)
	g.targets = append(g.targets, target)
	g.provider = provider
	if g.checkErr != nil {
		return aigateway.CheckResult{}, g.checkErr
	}
	return aigateway.CheckResult{Protocol: aiendpoint.ProtocolAnthropic}, nil
}

func (g *fakeGateway) Relay(ctx context.Context, _ aigateway.StepRef, target aigateway.Target, req aigateway.HTTPRequest, body io.Reader, sink aigateway.ResponseSink) error {
	_, _ = io.Copy(io.Discard, body)
	g.mu.Lock()
	g.requests = append(g.requests, req)
	g.relayTargets = append(g.relayTargets, target)
	err := g.relayErr
	g.mu.Unlock()
	if err != nil {
		return err
	}
	if err := sink.Head(ctx, http.StatusOK, http.Header{"Content-Type": {"application/json"}}); err != nil {
		return err
	}
	return sink.Write(ctx, []byte(`{"ok":true}`))
}

func withGateway(g aigateway.Client) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context { return aigateway.WithClient(ctx, g) }
}

func agentWithEndpoint() ir.AgentRun {
	return ir.AgentRun{Prompt: "x", Endpoint: "cle1"}
}

// waitEnvFile waits until the fake CLI has written the whole environment: the file
// exists before its content does.
func waitEnvFile(t *testing.T, dir string, n int) map[string]string {
	t.Helper()
	var env map[string]string
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "env."+string(rune('0'+n))))
		if err != nil || !strings.HasSuffix(string(data), "\n") {
			return false
		}
		env = map[string]string{}
		for line := range strings.SplitSeq(string(data), "\n") {
			if key, value, ok := strings.Cut(line, "="); ok {
				env[key] = value
			}
		}
		_, ok := env["NO_PROXY"]
		return ok && env["ANTHROPIC_BASE_URL"] != ""
	}, 10*time.Second, 20*time.Millisecond)
	return env
}

// A step that names an endpoint, in a process with no gateway, fails before the
// CLI starts with the reason relay_unavailable.
func TestAgentRunEndpointNeedsAGateway(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, agentWithEndpoint(), nil, streamSuccess)
	err := run.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `endpoint "cle1"`)
	assert.Contains(t, err.Error(), "relay_unavailable")
	var failure *executor.AgentError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, ir.AgentKindError, failure.Kind)
	assert.Equal(t, "relay_unavailable", failure.Reason)
	assert.Equal(t, 1, run.agent.ExitCode())
	_, statErr := os.Stat(filepath.Join(run.dir, "argv.1"))
	assert.True(t, os.IsNotExist(statErr), "the CLI did not start")
}

// The start check fails the step with the reason and the name of the endpoint,
// before the CLI starts. No request goes to a provider.
func TestAgentRunEndpointStartCheckFailures(t *testing.T) {
	t.Parallel()

	for _, reason := range []aigateway.Reason{
		aigateway.ReasonEndpointUnknown, aigateway.ReasonEndpointForbidden,
		aigateway.ReasonEndpointProtocolMismatch, aigateway.ReasonEndpointKeyMissing,
	} {
		t.Run(string(reason), func(t *testing.T) {
			t.Parallel()
			gateway := &fakeGateway{checkErr: aigateway.Fail(reason, `the DAG "fix-bug" is not in the allow list`)}
			run := newFakeClaudeRun(t, agentWithEndpoint(), nil, streamSuccess)
			run.wrap = withGateway(gateway)
			err := run.run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), `step "review": endpoint "cle1": `+string(reason)+`: the DAG "fix-bug" is not in the allow list`)
			var failure *executor.AgentError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, ir.AgentKindError, failure.Kind)
			assert.Equal(t, string(reason), failure.Reason)
			assert.Equal(t, 1, run.agent.ExitCode())
			_, statErr := os.Stat(filepath.Join(run.dir, "argv.1"))
			assert.True(t, os.IsNotExist(statErr), "the CLI did not start")
			assert.Empty(t, gateway.requests)
			assert.Equal(t, "claude", gateway.provider)
			require.Len(t, gateway.steps, 1)
			assert.Equal(t, "agent-run", gateway.steps[0].DAGName)
			assert.Equal(t, "review", gateway.steps[0].StepName)
		})
	}
}

// The CLI gets the address of the relay and the relay secret in place of a key,
// NO_PROXY with the loopback names, and no credential of the deployment. The real
// key is in no file and in no log. A request to the relay reaches the gateway.
func TestAgentRunEndpointCLIEnvironment(t *testing.T) {
	t.Parallel()

	gateway := &fakeGateway{}
	run := newFakeClaudeRun(t, agentWithEndpoint(), nil, streamSuccess)
	run.wrap = func(ctx context.Context) context.Context { return aigateway.WithClient(ctx, gateway) }
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "wait.1"), nil, 0o600))

	done := make(chan error, 1)
	go func() { done <- run.run() }()

	// The CLI waits. Read what it received, and call the relay as it would.
	env := waitEnvFile(t, run.dir, 1)
	base, secret := env["ANTHROPIC_BASE_URL"], env["ANTHROPIC_API_KEY"]
	assert.True(t, strings.HasPrefix(base, "http://127.0.0.1:"), base)
	assert.GreaterOrEqual(t, len(secret), 43, "the relay secret is 32 random bytes")
	assert.NotContains(t, secret, theRealKey)
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		assert.Contains(t, strings.Split(env[key], ","), "127.0.0.1", key)
		assert.Contains(t, strings.Split(env[key], ","), "localhost", key)
	}
	assert.Empty(t, env["ANTHROPIC_AUTH_TOKEN"])
	assert.Empty(t, env["CLAUDE_CODE_OAUTH_TOKEN"])

	req, err := http.NewRequest(http.MethodPost, base+"/v1/messages", strings.NewReader(`{"model":"m"}`))
	require.NoError(t, err)
	req.Header.Set("X-Api-Key", secret)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.JSONEq(t, `{"ok":true}`, string(body))
	require.Len(t, gateway.requests, 1)
	assert.Empty(t, gateway.requests[0].Header.Get("X-Api-Key"), "the relay secret does not leave the worker")

	// A request with another secret is refused, and the relay ends with the attempt.
	bad, _ := http.NewRequest(http.MethodPost, base+"/v1/messages", strings.NewReader(`{}`))
	bad.Header.Set("X-Api-Key", theRealKey)
	badResp, err := (&http.Client{Timeout: 5 * time.Second}).Do(bad)
	require.NoError(t, err)
	_ = badResp.Body.Close()
	assert.Equal(t, 401, badResp.StatusCode)

	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "go.1"), nil, 0o600))
	require.NoError(t, <-done)
	_, err = (&http.Client{Timeout: time.Second}).Post(base+"/v1/messages", "application/json", strings.NewReader("{}"))
	assert.Error(t, err, "the relay stops at the end of the attempt")

	// The key of the provider is nowhere on the worker.
	assert.NotContains(t, run.stdout.String()+run.stderr.String(), theRealKey)
	assert.NotContains(t, run.stdout.String()+run.stderr.String(), secret, "the relay secret is masked in the logs")
	require.NoError(t, filepath.Walk(run.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			assert.NotContains(t, string(data), theRealKey, path)
		}
		return nil
	}))
}

// The environment of the deployment can carry a model route of the egress proxy
// and a credential. The step replaces them (Spec 094, "Client settings").
func TestRelayEnvironmentReplacesTheRouteOfTheDeployment(t *testing.T) {
	t.Parallel()

	env := relayEnvironment([]string{
		"PATH=/usr/bin",
		"ANTHROPIC_BASE_URL=http://egress-proxy:8080",
		"ANTHROPIC_API_KEY=placeholder-of-the-proxy",
		"ANTHROPIC_AUTH_TOKEN=token",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth",
		// Each of these gives the CLI a route that skips the relay.
		"CLAUDE_CODE_USE_BEDROCK=1",
		"CLAUDE_CODE_USE_VERTEX=1",
		"ANTHROPIC_BEDROCK_BASE_URL=https://bedrock.example",
		"ANTHROPIC_VERTEX_PROJECT_ID=p",
		"NO_PROXY=10.0.0.0/8, example.org",
		"no_proxy=localhost",
		"HTTPS_PROXY=http://egress-proxy:3128",
	}, "http://127.0.0.1:4242", "the-secret")

	got := map[string][]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		got[key] = append(got[key], value)
	}
	assert.Equal(t, []string{"http://127.0.0.1:4242"}, got["ANTHROPIC_BASE_URL"])
	assert.Equal(t, []string{"the-secret"}, got["ANTHROPIC_API_KEY"])
	assert.Empty(t, got["ANTHROPIC_AUTH_TOKEN"])
	assert.Empty(t, got["CLAUDE_CODE_OAUTH_TOKEN"])
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_PROJECT_ID"} {
		assert.Empty(t, got[name], name)
	}
	assert.Equal(t, []string{"/usr/bin"}, got["PATH"])
	assert.Equal(t, []string{"http://egress-proxy:3128"}, got["HTTPS_PROXY"], "the proxy for other traffic stays")
	assert.Equal(t, []string{"10.0.0.0/8,example.org,localhost,127.0.0.1"}, got["NO_PROXY"])
	assert.Equal(t, got["NO_PROXY"], got["no_proxy"])
}

// A refusal of the relay gives the reason of the refusal to a failed attempt.
func TestAgentRunEndpointRefusalGivesTheReason(t *testing.T) {
	t.Parallel()

	gateway := &fakeGateway{relayErr: aigateway.Fail(aigateway.ReasonEndpointForbidden, "the DAG is not in the allow list")}
	run := newFakeClaudeRun(t, agentWithEndpoint(), nil,
		`{"type":"assistant","error":"authentication_failed","message":{"content":[]}}`+"\n"+`{"type":"result","subtype":"success","is_error":true,"result":"Invalid API key"}`+"\n")
	run.wrap = withGateway(gateway)
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "exit.1"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "wait.1"), nil, 0o600))

	done := make(chan error, 1)
	go func() { done <- run.run() }()
	env := waitEnvFile(t, run.dir, 1)
	req, _ := http.NewRequest(http.MethodPost, env["ANTHROPIC_BASE_URL"]+"/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("X-Api-Key", env["ANTHROPIC_API_KEY"])
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode)
	assert.Contains(t, string(body), "endpoint_forbidden")
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "go.1"), nil, 0o600))

	err = <-done
	require.Error(t, err)
	var failure *executor.AgentError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "endpoint_forbidden", failure.Reason)
	assert.Contains(t, err.Error(), "the AI relay refused a request: endpoint_forbidden")
	assert.Contains(t, run.stderr.String(), "ai relay: refuse POST /v1/messages: endpoint_forbidden", "the step log tells the refusal")
}

// The start check carries the model of the step after resolution. A model that the
// models list of the endpoint lacks fails the step before the CLI starts, with the
// reason model_unknown. An empty model carries nothing.
func TestAgentRunEndpointStartCheckCarriesTheModel(t *testing.T) {
	t.Parallel()

	t.Run("the model reaches the check", func(t *testing.T) {
		t.Parallel()
		gateway := &fakeGateway{}
		run := newFakeClaudeRun(t, agentWithEndpoint(), nil, streamSuccess)
		run.wrap = withGateway(gateway)
		require.NoError(t, run.run())
		assert.Equal(t, "sonnet", gateway.model)
		assert.Equal(t, 1, gateway.modelChecks)
	})

	t.Run("a resolved reference", func(t *testing.T) {
		t.Parallel()
		gateway := &fakeGateway{}
		run := newFakeClaudeRun(t, agentWithEndpoint(), nil, streamSuccess)
		run.agent.configs[0].flags["model"] = "claude-from-reference"
		run.wrap = withGateway(gateway)
		require.NoError(t, run.run())
		assert.Equal(t, "claude-from-reference", gateway.model)
	})

	t.Run("no model means the default of the CLI", func(t *testing.T) {
		t.Parallel()
		gateway := &fakeGateway{}
		run := newFakeClaudeRun(t, agentWithEndpoint(), nil, streamSuccess)
		delete(run.agent.configs[0].flags, "model")
		run.wrap = withGateway(gateway)
		require.NoError(t, run.run())
		assert.Empty(t, gateway.model)
		assert.Zero(t, gateway.modelChecks)
	})

	t.Run("model_unknown fails before the CLI", func(t *testing.T) {
		t.Parallel()
		gateway := &fakeGateway{checkErr: aigateway.Fail(aigateway.ReasonModelUnknown,
			`the model "claude-ghost" is not in the models list of the endpoint (read at 2026-11-04T08:00:00Z); reload the list on the AI page, or fix the model`)}
		run := newFakeClaudeRun(t, agentWithEndpoint(), nil, streamSuccess)
		run.agent.configs[0].flags["model"] = "claude-ghost"
		run.wrap = withGateway(gateway)
		err := run.run()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `step "review": endpoint "cle1": model_unknown: the model "claude-ghost" is not in the models list of the endpoint (read at 2026-11-04T08:00:00Z); reload the list on the AI page, or fix the model`)
		var failure *executor.AgentError
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, ir.AgentKindError, failure.Kind)
		assert.Equal(t, "model_unknown", failure.Reason)
		assert.Equal(t, 1, run.agent.ExitCode())
		_, statErr := os.Stat(filepath.Join(run.dir, "argv.1"))
		assert.True(t, os.IsNotExist(statErr), "the CLI did not start")
		assert.Empty(t, gateway.requests)
	})
}
