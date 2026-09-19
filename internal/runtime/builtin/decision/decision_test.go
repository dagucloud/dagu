// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const responseJSON = `{
  "model":"jev-test", "provider":"TypeSafe", "id":"request-1",
  "answers":{
    "department":{"type":"choice","choice":"billing","probabilities":{"billing":0.6,"other":0.4},"confidence":0.1},
    "urgency":{"type":"score","score":1.4,"legend":{"0":"Routine","1":"Soon","2":"Now"},"probabilities":{"0":0.1,"1":0.4,"2":0.5},"confidence":0.2},
    "refund":{"type":"noul","noul":0.95}
  },
  "usage":{"input_tokens":100,"output_tokens":25,"cost":0.0000042,"sequence":9007199254740993,"estimate":1.2300e+19}
}`

func testConfig(t *testing.T) map[string]any {
	t.Helper()
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
  "provider":"openrouter","model":"jev-test","state":{"message":"Refund please","count":2,"active":true},
  "questions":{
    "department":{"type":"choice","instructions":"Which department?","criteria":{"billing":"Charges","other":null}},
    "urgency":{"type":"score","instructions":"How urgent?","criteria":["Routine","Soon","Now"]},
    "refund":{"type":"noul","instructions":"Refund requested?"}
  }
}`), &raw))
	return raw
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ provider, path, key string }{
		{openRouter, "/api/alpha/decisions", "OPENROUTER_API_KEY"},
		{typeSafe, "/v1/systemone", "TYPESAFE_API_KEY"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.path, r.URL.Path)
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				var body map[string]any
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.Len(t, body, 3)
				assert.Equal(t, "jev-test", body["model"])
				assert.Equal(t, map[string]any{"message": "Refund please", "count": float64(2), "active": true}, body["state"])
				_, _ = w.Write([]byte(responseJSON))
			}))
			defer server.Close()
			raw := testConfig(t)
			raw["provider"] = tc.provider
			raw["base_url"] = server.URL + tc.path[:strings.LastIndex(tc.path, "/")] + "/"
			scope := value.NewEnvScope(nil, false).WithEntry(tc.key, "test-key", value.EnvSourceSecret)
			ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: scope})
			exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
			require.NoError(t, err)
			defer exec.(*decisionExecutor).Close()
			var stdout bytes.Buffer
			exec.SetStdout(&stdout)
			require.NoError(t, exec.Run(ctx))
			assert.JSONEq(t, responseJSON, stdout.String())
			var result struct{ Usage map[string]json.RawMessage }
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
			assert.Equal(t, "0.0000042", string(result.Usage["cost"]))
			assert.Equal(t, "9007199254740993", string(result.Usage["sequence"]))
			assert.Equal(t, "1.2300e+19", string(result.Usage["estimate"]))
		})
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"provider", func(c map[string]any) { c["provider"] = "openai" }},
		{"model", func(c map[string]any) { c["model"] = "" }},
		{"state", func(c map[string]any) { delete(c, "state") }},
		{"scalar", func(c map[string]any) { c["state"] = 42 }},
		{"questions", func(c map[string]any) { c["questions"] = map[string]any{} }},
		{"extra", func(c map[string]any) { c["stream"] = true }},
		{"url", func(c map[string]any) { c["base_url"] = "file:///tmp/request" }},
		{"key", func(c map[string]any) { c["api_key_name"] = "not a name" }},
		{"type", func(c map[string]any) {
			c["questions"] = map[string]any{"q": map[string]any{"type": "boolean", "instructions": "Yes?"}}
		}},
		{"choice", func(c map[string]any) {
			c["questions"] = map[string]any{"q": map[string]any{"type": "choice", "instructions": "Which?", "criteria": map[string]any{"only": nil}}}
		}},
		{"score", func(c map[string]any) {
			c["questions"] = map[string]any{"q": map[string]any{"type": "score", "instructions": "How much?", "criteria": []any{"one"}}}
		}},
		{"noul", func(c map[string]any) {
			c["questions"] = map[string]any{"q": map[string]any{"type": "noul", "instructions": "Yes?", "criteria": map[string]any{"true": "Yes"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig(t)
			tc.edit(c)
			require.Error(t, validateStep(ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: c}}))
		})
	}
	raw := testConfig(t)
	raw["provider"] = "${env.PROVIDER}"
	raw["base_url"] = "${env.ENDPOINT}"
	require.NoError(t, validateStep(ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}}))
}

func TestResponse(t *testing.T) {
	t.Parallel()
	cfg, err := parseConfig(testConfig(t))
	require.NoError(t, err)
	for _, tc := range []struct{ name, from, to string }{
		{"missing", `"refund":`, `"wrong":`},
		{"type", `"type":"noul"`, `"type":"choice"`},
		{"choice", `"choice":"billing"`, `"choice":"unknown"`},
		{"score", `"score":1.4`, `"score":3`},
		{"confidence", `"confidence":0.1`, `"confidence":true`},
		{"probability", `"noul":0.95`, `"noul":1.5`},
		{"distribution", `"billing":0.6`, `"wrong":0.6`},
		{"partial_distribution", `,"other":0.4`, ``},
		{"partial_legend", `,"2":"Now"`, ``},
		{"legend", `"2":"Now"`, `"2":null`},
		{"usage", `"input_tokens":100`, `"input_tokens":-1`},
		{"model", `"model":"jev-test"`, `"model":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response map[string]any
			decoder := json.NewDecoder(strings.NewReader(strings.Replace(responseJSON, tc.from, tc.to, 1)))
			decoder.UseNumber()
			require.NoError(t, decoder.Decode(&response))
			require.Error(t, validateResponse(response, cfg.Questions))
		})
	}
}

func TestBaseURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		url     string
		wantErr bool
	}{
		{"https://api.example.com/v1", false},
		{"http://localhost:8080/v1", false},
		{"http://127.0.0.1:8080/v1", false},
		{"http://[::1]:8080/v1", false},
		{"http://api.example.com/v1", true},
		{"http://192.168.1.2/v1", true},
		{"http://localhost.example.com/v1", true},
		{"https://api.example.com/v1?", true},
		{"https://api.example.com/v1?key=value", true},
		{"https://api.example.com/v1#", true},
		{"https://api.example.com/v1#fragment", true},
	} {
		t.Run(tc.url, func(t *testing.T) {
			cfg := config{Provider: openRouter, Model: "jev-test", BaseURL: tc.url}
			err := cfg.validate(false)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHTTPFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		calls  int32
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":"secret-\"quoted\"-value"}`, 1},
		{"limited", http.StatusTooManyRequests, `{"error":"busy"}`, 2},
		{"overloaded", 529, `{"error":"busy"}`, 2},
		{"malformed", http.StatusOK, `not json`, 1},
		{"trailing", http.StatusOK, responseJSON + `{}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			cfg := llm.DefaultConfig()
			cfg.MaxRetries = 1
			cfg.InitialInterval = time.Millisecond
			c := client{http: llm.NewHTTPClient(cfg), endpoint: server.URL, apiKey: "key", maxResponseBytes: ir.DefaultMaxOutputSize}
			_, err := c.evaluate(t.Context(), request{})
			require.Error(t, err)
			if tc.status != http.StatusOK {
				assert.NotContains(t, err.Error(), tc.body)
			}
			assert.Equal(t, tc.calls, calls.Load())
		})
	}
}

func TestCancellation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"kill", "run_context", "body"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "body" {
					_, _ = io.WriteString(w, `{"model":`)
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			raw := testConfig(t)
			raw["base_url"] = server.URL
			scope := value.NewEnvScope(nil, false).WithEntry("OPENROUTER_API_KEY", "key", value.EnvSourceSecret)
			ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: scope})
			exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
			require.NoError(t, err)
			defer exec.(*decisionExecutor).Close()
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- exec.Run(runCtx) }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not start")
			}
			if mode == "kill" {
				require.NoError(t, exec.Kill(os.Interrupt))
			} else {
				cancel()
			}
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("request did not cancel")
			}
		})
	}
}

func TestRunCancellation(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, responseJSON)
	}))
	defer server.Close()
	raw := testConfig(t)
	raw["base_url"] = server.URL
	scope := value.NewEnvScope(nil, false).WithEntry("OPENROUTER_API_KEY", "key", value.EnvSourceSecret)
	ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: scope})
	exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
	require.NoError(t, err)
	defer exec.(*decisionExecutor).Close()
	exec.SetStdout(io.Discard)
	runCtx, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, exec.Run(runCtx), context.Canceled)
	assert.Zero(t, calls.Load())
}

func TestCredentials(t *testing.T) {
	t.Parallel()
	raw := testConfig(t)
	raw["api_key_name"] = "CUSTOM_KEY"
	ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: value.NewEnvScope(nil, false)})
	_, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
	require.ErrorContains(t, err, "CUSTOM_KEY")

	secret := "secret-\"quoted\"-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+secret, r.Header.Get("Authorization"))
		var req request
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, secret, req.State)
		_, _ = w.Write([]byte(strings.Replace(responseJSON, "request-1", `secret-\"quoted\"-value`, 1)))
	}))
	defer server.Close()
	raw["base_url"] = server.URL
	raw["state"] = secret
	ctx = runtime.WithEnv(t.Context(), runtime.Env{Scope: value.NewEnvScope(nil, false).WithEntry("CUSTOM_KEY", secret, value.EnvSourceSecret)})
	exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
	require.NoError(t, err)
	defer exec.(*decisionExecutor).Close()
	var stdout bytes.Buffer
	exec.SetStdout(&stdout)
	require.NoError(t, exec.Run(ctx))
	var result map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.Equal(t, "*******", result["id"])
}

// A secret value that is merely a substring of the authored state must not
// rewrite the request; the provider has to score the text the author wrote.
func TestRequestNotMasked(t *testing.T) {
	t.Parallel()
	const state = "production outage"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, state, req.State)
		_, _ = w.Write([]byte(responseJSON))
	}))
	defer server.Close()
	raw := testConfig(t)
	raw["base_url"] = server.URL
	raw["state"] = state
	scope := value.NewEnvScope(nil, false).
		WithEntry("OPENROUTER_API_KEY", "test-key", value.EnvSourceSecret).
		WithEntry("STAGE", "prod", value.EnvSourceSecret)
	ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: scope})
	exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
	require.NoError(t, err)
	defer exec.(*decisionExecutor).Close()
	exec.SetStdout(io.Discard)
	require.NoError(t, exec.Run(ctx))
}

func TestResponseLimit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, responseJSON)
	}))
	defer server.Close()
	for _, limit := range []int{len(responseJSON), len(responseJSON) - 1} {
		raw := testConfig(t)
		raw["base_url"] = server.URL
		scope := value.NewEnvScope(nil, false).WithEntry("OPENROUTER_API_KEY", "key", value.EnvSourceSecret)
		ctx := runtime.WithEnv(t.Context(), runtime.Env{Context: runtime.Context{DAG: &ir.DAG{MaxOutputSize: limit}}, Scope: scope})
		exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
		require.NoError(t, err)
		defer exec.(*decisionExecutor).Close()
		var stdout bytes.Buffer
		exec.SetStdout(&stdout)
		err = exec.Run(ctx)
		if limit < len(responseJSON) {
			require.ErrorContains(t, err, "maximum size limit")
			assert.Empty(t, stdout.String())
		} else {
			require.NoError(t, err)
		}
	}
}

func TestConnectionReuse(t *testing.T) {
	t.Parallel()
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return the credential so each executor must mask its own key.
		body := strings.Replace(responseJSON, "request-1", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), 1)
		_, _ = io.WriteString(w, body)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	for _, key := range []string{"first-key", "second-key"} {
		raw := testConfig(t)
		raw["base_url"] = server.URL
		scope := value.NewEnvScope(nil, false).WithEntry("OPENROUTER_API_KEY", key, value.EnvSourceSecret)
		ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: scope})
		exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
		require.NoError(t, err)
		var stdout bytes.Buffer
		exec.SetStdout(&stdout)
		require.NoError(t, exec.Run(ctx))
		require.NoError(t, exec.(*decisionExecutor).Close())
		assert.NotContains(t, stdout.String(), key)
		assert.Contains(t, stdout.String(), `"id":"*******"`)
	}
	assert.EqualValues(t, 1, connections.Load())
}

// Fixtures are the documented response examples, not live provider recordings.
func TestProviderResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		provider  string
		questions map[string]question
	}{
		// https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-questions-and-answers-request
		{openRouter, map[string]question{
			"is_bug":  {Type: noulType, Instructions: "Is the customer reporting a software defect?"},
			"team":    {Type: choiceType, Instructions: "Which team should own this ticket?", Criteria: map[string]any{"account": nil, "frontend": nil, "payments": nil}},
			"urgency": {Type: scoreType, Instructions: "How urgent is this ticket?", Criteria: []any{"Can wait for the next release", "Should be fixed this week", "Blocking revenue right now"}},
		}},
		// https://docs.typesafe.ai/api
		{typeSafe, map[string]question{"is_urgent": {Type: noulType, Instructions: "Is this urgent?"}}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile("testdata/" + tc.provider + ".json")
			require.NoError(t, err)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
			defer server.Close()
			c := client{http: sharedHTTPClient, endpoint: server.URL, apiKey: "test-key", maxResponseBytes: ir.DefaultMaxOutputSize}
			_, err = c.evaluate(t.Context(), request{Model: "jev-latest", State: "Urgent: checkout fails", Questions: tc.questions})
			require.NoError(t, err)
		})
	}
}

func TestEndpoint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ provider, baseURL, endpoint, key string }{
		{openRouter, "", "https://openrouter.ai/api/alpha/decisions", "OPENROUTER_API_KEY"},
		{typeSafe, "", "https://api.typesafe.ai/v1/systemone", "TYPESAFE_API_KEY"},
		{openRouter, "https://example.com/custom/v2/", "https://example.com/custom/v2/decisions", "OPENROUTER_API_KEY"},
		{typeSafe, "https://example.com/custom%2Froot", "https://example.com/custom%2Froot/systemone", "TYPESAFE_API_KEY"},
	} {
		cfg := config{Provider: tc.provider, BaseURL: tc.baseURL, Model: "test"}
		require.NoError(t, cfg.validate(false))
		endpoint, key, err := cfg.connection()
		require.NoError(t, err)
		assert.Equal(t, tc.endpoint, endpoint)
		assert.Equal(t, tc.key, key)
	}
}

func TestCancellationIsolation(t *testing.T) {
	t.Parallel()
	started := make(chan string, 2)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- r.Header.Get("Authorization")
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		_, _ = io.WriteString(w, responseJSON)
	}))
	// Release blocked handlers even if an assertion fails.
	t.Cleanup(server.Close)
	newExec := func(key string) *decisionExecutor {
		raw := testConfig(t)
		raw["base_url"] = server.URL
		scope := value.NewEnvScope(nil, false).WithEntry("OPENROUTER_API_KEY", key, value.EnvSourceSecret)
		ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: scope})
		exec, err := newExecutor(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: raw}})
		require.NoError(t, err)
		e := exec.(*decisionExecutor)
		t.Cleanup(func() { _ = e.Close() })
		e.SetStdout(io.Discard)
		return e
	}
	first, second := newExec("first-key"), newExec("second-key")
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- first.Run(t.Context()) }()
	go func() { secondDone <- second.Run(t.Context()) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("requests did not start")
		}
	}
	require.NoError(t, first.Close())
	select {
	case err := <-firstDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("request did not cancel")
	}
	unblock()
	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("other request did not finish")
	}
}
