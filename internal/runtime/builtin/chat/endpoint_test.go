// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agenttools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 094, "chat.completion". These tests run a chat step through the real
// relay and the real gateway of the local mode. A fake provider stands for the
// upstream of each chat type. It records each request, so a test sees what the
// provider received.

// theRealKey is the key that only the gateway reads and only the fake provider
// receives. The step never gets it.
const theRealKey = "sk-real-0123456789-THE-KEY-OF-THE-ENDPOINT"

// recorded is one request that the fake provider received.
type recorded struct {
	Path   string
	Header http.Header
	Body   string
}

// fakeProvider is a provider API. Its handler answers the requests in order.
type fakeProvider struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []recorded
	handler  func(n int, w http.ResponseWriter, body map[string]any)
}

func newFakeProvider(t *testing.T, handler func(n int, w http.ResponseWriter, body map[string]any)) *fakeProvider {
	t.Helper()
	p := &fakeProvider{handler: handler}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		p.mu.Lock()
		p.requests = append(p.requests, recorded{Path: r.URL.Path, Header: r.Header.Clone(), Body: string(raw)})
		n := len(p.requests)
		p.mu.Unlock()
		p.handler(n, w, body)
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeProvider) all() []recorded {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recorded(nil), p.requests...)
}

// endpointSource is the store of the gateway: a few endpoints and their keys.
type endpointSource struct {
	mu        sync.Mutex
	endpoints map[string]aiendpoint.Endpoint
	keys      map[string]string
	forbidden map[string]bool
	// resolveLimit makes the source answer endpoint_unknown after this number of
	// calls, as a deleted endpoint does. Zero means no limit.
	resolveLimit int
	resolves     int
	// lists holds the models list of an endpoint. An endpoint with no list has no
	// model check, as an endpoint whose list no read made.
	lists map[string][]string
	// modelChecks records the model of each check.
	modelChecks []string
}

// CheckModel implements aigateway.ModelChecker with a fixed list.
func (s *endpointSource) CheckModel(_ context.Context, ep aiendpoint.Endpoint, _, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelChecks = append(s.modelChecks, ep.Name+"/"+model)
	list, ok := s.lists[ep.Name]
	if !ok || slices.Contains(list, model) {
		return nil
	}
	return &aiendpoint.Error{Reason: aiendpoint.ReasonModelUnknown, Name: ep.Name, Detail: fmt.Sprintf(
		"the model %q is not in the models list of the endpoint (read at 2026-11-04T08:00:00Z); reload the list on the AI page, or fix the model", model)}
}

func (s *endpointSource) ResolveEndpoint(_ context.Context, name, dagName, _ string) (aiendpoint.Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolves++
	if s.resolveLimit > 0 && s.resolves > s.resolveLimit {
		return aiendpoint.Endpoint{}, &aiendpoint.Error{Reason: aiendpoint.ReasonUnknown, Name: name, Detail: "the endpoint was deleted"}
	}
	ep, ok := s.endpoints[name]
	if !ok {
		return aiendpoint.Endpoint{}, &aiendpoint.Error{Reason: aiendpoint.ReasonUnknown, Name: name,
			Detail: "no endpoint of this name is visible to the DAG"}
	}
	if s.forbidden[name] {
		return aiendpoint.Endpoint{}, &aiendpoint.Error{Reason: aiendpoint.ReasonForbidden, Name: name,
			Detail: fmt.Sprintf("the DAG %q is not in the allow list", dagName)}
	}
	return ep, nil
}

func (s *endpointSource) ReadKey(_ context.Context, ep aiendpoint.Endpoint) (string, error) {
	return s.keys[ep.Name], nil
}

// add registers an endpoint that calls the fake provider. The path is the path of
// the base URL, as a preset has it.
func (s *endpointSource) add(name, preset string, protocol aiendpoint.Protocol, auth aiendpoint.Auth, base string, key string) {
	if s.endpoints == nil {
		s.endpoints = map[string]aiendpoint.Endpoint{}
		s.keys = map[string]string{}
		s.forbidden = map[string]bool{}
	}
	ep := aiendpoint.Endpoint{Name: name, Preset: preset, Protocol: protocol, BaseURL: base, Auth: auth}
	if key != "" {
		ep.SecretID = "secret-" + name
		ep.KeyFingerprint = "0123456789ab"
		s.keys[name] = key
	}
	s.endpoints[name] = ep
}

func newLocalGateway(t *testing.T, src *endpointSource) aigateway.Client {
	t.Helper()
	gateway, err := aigateway.New(aigateway.Config{Endpoints: src, Limits: aigateway.Limits{
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, MaxHeaderBytes: 32768,
		MaxStreamsPerRun: 4, MaxStreams: 16, ConnectTimeout: 2 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, TotalTimeout: 30 * time.Second,
	}})
	require.NoError(t, err)
	return aigateway.Local{Gateway: gateway}
}

// syncBuffer is a buffer that the step writes and the test reads at the same time.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// chatRun is a chat step with an endpoint, on a gateway.
type chatRun struct {
	t        *testing.T
	executor *Executor
	stdout   syncBuffer
	stderr   syncBuffer
	ctx      context.Context

	mu      sync.Mutex
	secrets []string
	bases   []string
}

func newChatRun(t *testing.T, step ir.Step, client aigateway.Client, opts ...runctx.ContextOption) *chatRun {
	t.Helper()
	dag := &ir.DAG{Name: "test"}
	ctx := runctx.NewContext(context.Background(), dag, "run-1", "/tmp/log", opts...)
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, ir.Step{Name: "test"}))
	if client != nil {
		ctx = aigateway.WithClient(ctx, client)
	}
	if step.Name == "" {
		step.Name = "ask"
	}
	exec, err := newChatExecutor(ctx, step)
	require.NoError(t, err)
	run := &chatRun{t: t, executor: exec.(*Executor), ctx: ctx}
	run.executor.SetStdout(&run.stdout)
	run.executor.SetStderr(&run.stderr)
	// The real provider client runs. The test only notes what it received.
	build := run.executor.newRelayProvider
	run.executor.newRelayProvider = func(pt llmpkg.ProviderType, base, secret string) (llmpkg.Provider, error) {
		run.mu.Lock()
		run.secrets = append(run.secrets, secret)
		run.bases = append(run.bases, base)
		run.mu.Unlock()
		return build(pt, base, secret)
	}
	return run
}

func (r *chatRun) run() error { return r.executor.Run(r.ctx) }

func userStep(llm *ir.LLMConfig) ir.Step {
	return ir.Step{
		Name:     "ask",
		LLM:      llm,
		Messages: []ir.PromptMessage{{Role: ir.LLMRoleUser, Content: "Classify this note."}},
	}
}

// chatWire writes the answers of one protocol.
type chatWire struct {
	// path is the path that the client calls after the base URL.
	path string
	// text writes a final answer as JSON.
	text func(content string) string
	// stream writes a final answer as server-sent events.
	stream func(parts ...string) string
	// toolCall writes the answer of a model that calls a tool.
	toolCall func(id, name, arguments string) string
	// credential reads the credential that the provider received.
	credential func(h http.Header) string
}

var openaiWire = chatWire{
	path: "/chat/completions",
	text: func(content string) string {
		return `{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":` + quote(content) + `},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	},
	stream: func(parts ...string) string {
		var sb strings.Builder
		for _, part := range parts {
			sb.WriteString(`data: {"choices":[{"delta":{"content":` + quote(part) + `}}]}` + "\n\n")
		}
		sb.WriteString("data: [DONE]\n\n")
		return sb.String()
	},
	toolCall: func(id, name, arguments string) string {
		return `{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":` + quote(id) +
			`,"type":"function","function":{"name":` + quote(name) + `,"arguments":` + quote(arguments) + `}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	},
	credential: func(h http.Header) string { return h.Get("Authorization") },
}

var anthropicWire = chatWire{
	path: "/v1/messages",
	text: func(content string) string {
		return `{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":` + quote(content) + `}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`
	},
	stream: func(parts ...string) string {
		var sb strings.Builder
		sb.WriteString("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n")
		for _, part := range parts {
			sb.WriteString(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":` + quote(part) + `}}` + "\n\n")
		}
		sb.WriteString("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\n")
		sb.WriteString("data: {\"type\":\"message_stop\"}\n\n")
		return sb.String()
	},
	toolCall: func(id, name, arguments string) string {
		return `{"id":"m1","type":"message","role":"assistant","content":[{"type":"tool_use","id":` + quote(id) +
			`,"name":` + quote(name) + `,"input":` + arguments + `}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":2}}`
	},
	credential: func(h http.Header) string { return h.Get("X-Api-Key") },
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func writeSSE(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, body)
}

// chatTypeCase is one chat type, with the preset that selects it.
type chatTypeCase struct {
	name     string
	preset   string
	protocol aiendpoint.Protocol
	auth     aiendpoint.Auth
	// basePath is the path of the base URL of the endpoint.
	basePath string
	wire     chatWire
	// chatType is the provider type that the preset selects.
	chatType string
}

var chatTypeCases = []chatTypeCase{
	{"openai", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, "/v1", openaiWire, "openai"},
	{"google gemini compat", "google-gemini", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, "/v1beta/openai", openaiWire, "openai"},
	{"anthropic", "anthropic", aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey, "", anthropicWire, "anthropic"},
	{"deepseek anthropic", "deepseek", aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey, "/anthropic", anthropicWire, "anthropic"},
	{"openrouter", "openrouter", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, "/api/v1", openaiWire, "openrouter"},
	{"openrouter anthropic", "openrouter", aiendpoint.ProtocolAnthropic, aiendpoint.AuthBearer, "/api", anthropicWire, "anthropic"},
	{"zai", "zai", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, "/api/paas/v4", openaiWire, "zai"},
	{"local", "ollama", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, "/v1", openaiWire, "local"},
}

// receivedKey returns the real key that the provider received in the header that
// its auth form sets.
func receivedKey(c chatTypeCase, h http.Header) string {
	if c.auth == aiendpoint.AuthXAPIKey {
		return h.Get("X-Api-Key")
	}
	return strings.TrimPrefix(h.Get("Authorization"), "Bearer ")
}

// A chat step with an endpoint calls its relay. The provider type comes from the
// endpoint, the path reaches the provider through the base URL of the endpoint,
// and the provider receives the real key and never the secret of the relay. This
// holds for each chat type, with streaming and without.
func TestChatEndpointEachChatType(t *testing.T) {
	t.Parallel()

	for _, tc := range chatTypeCases {
		for _, stream := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s stream=%v", tc.name, stream), func(t *testing.T) {
				t.Parallel()

				provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, body map[string]any) {
					if stream {
						assert.Equal(t, true, body["stream"])
						writeSSE(w, tc.wire.stream("Hel", "lo ", "there"))
						return
					}
					writeJSON(w, tc.wire.text("Hello there"))
				})
				src := &endpointSource{}
				src.add("cle1", tc.preset, tc.protocol, tc.auth, provider.server.URL+tc.basePath, theRealKey)

				step := userStep(&ir.LLMConfig{Model: "model-x", Endpoint: "cle1", Stream: &stream})
				run := newChatRun(t, step, newLocalGateway(t, src))
				require.NoError(t, run.run())

				assert.Equal(t, "Hello there\n", run.stdout.String())
				requests := provider.all()
				require.Len(t, requests, 1)
				assert.Equal(t, tc.basePath+tc.wire.path, requests[0].Path)
				assert.Equal(t, theRealKey, receivedKey(tc, requests[0].Header), "the provider gets the real key")
				require.Len(t, run.secrets, 1)
				assert.NotContains(t, requests[0].Header, "Proxy-Authorization")
				for name, values := range requests[0].Header {
					assert.NotContains(t, strings.Join(values, ","), run.secrets[0], "the secret of the relay is in no header: %s", name)
				}
				assert.NotContains(t, requests[0].Body, run.secrets[0])
				assert.NotContains(t, requests[0].Body, theRealKey)

				// The relay has the base URL of its protocol.
				if tc.protocol == aiendpoint.ProtocolOpenAI {
					assert.Regexp(t, `^http://127\.0\.0\.1:\d+/v1$`, run.bases[0])
				} else {
					assert.Regexp(t, `^http://127\.0\.0\.1:\d+$`, run.bases[0])
				}
				saved := run.executor.GetMessages()
				require.NotEmpty(t, saved)
				last := saved[len(saved)-1]
				assert.Equal(t, tc.chatType, last.Metadata.Provider, "the type comes from the endpoint")
				assert.Equal(t, "model-x", last.Metadata.Model)
				if !stream {
					assert.Equal(t, 5, last.Metadata.TotalTokens)
				}
			})
		}
	}
}

// The relay lives for one attempt. Its address stops answering at the end of the
// attempt, and each attempt has its own secret.
func TestChatEndpointRelayLivesForOneAttempt(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("ok"))
	})
	src := &endpointSource{}
	src.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
	stream := false
	run := newChatRun(t, userStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1", Stream: &stream}), newLocalGateway(t, src))

	require.NoError(t, run.run())
	require.NoError(t, run.run())
	require.Len(t, run.secrets, 2)
	assert.NotEqual(t, run.secrets[0], run.secrets[1], "each attempt has its own secret")
	assert.NotEqual(t, run.bases[0], run.bases[1], "each attempt has its own relay")
	assert.GreaterOrEqual(t, len(run.secrets[0]), 43, "the secret is 32 random bytes")

	_, err := (&http.Client{Timeout: time.Second}).Post(run.bases[0]+"/chat/completions", "application/json", strings.NewReader("{}"))
	assert.Error(t, err, "the relay stops at the end of the attempt")
}

// The real key reaches the fake provider and no other place: not the environment
// of the process, not an output, not a saved message, not a log of the step, and
// not a request body.
func TestChatEndpointKeyStaysInTheGateway(t *testing.T) {
	t.Parallel()

	for _, tc := range []chatTypeCase{chatTypeCases[0], chatTypeCases[2]} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var envDuringCall []string
			provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
				// The call is in progress: look at the process now.
				envDuringCall = os.Environ()
				writeJSON(w, tc.wire.text("done"))
			})
			src := &endpointSource{}
			src.add("cle1", tc.preset, tc.protocol, tc.auth, provider.server.URL+tc.basePath, theRealKey)
			stream := false
			run := newChatRun(t, userStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1", Stream: &stream, System: "Be brief."}), newLocalGateway(t, src))
			require.NoError(t, run.run())

			for _, entry := range envDuringCall {
				assert.NotContains(t, entry, theRealKey, "the process environment holds no key")
			}
			assert.NotContains(t, run.stdout.String(), theRealKey)
			assert.NotContains(t, run.stderr.String(), theRealKey)
			assert.NotContains(t, run.stderr.String(), run.secrets[0])
			messages, err := json.Marshal(run.executor.GetMessages())
			require.NoError(t, err)
			assert.NotContains(t, string(messages), theRealKey)
			assert.NotContains(t, string(messages), run.secrets[0])
			// The key is in the headers that the provider recorded, and nowhere else.
			requests := provider.all()
			require.Len(t, requests, 1)
			assert.Equal(t, theRealKey, receivedKey(tc, requests[0].Header))
			assert.NotContains(t, requests[0].Body, theRealKey)
		})
	}
}

// A tool call goes through the relay and back: the model asks for a tool, the step
// sends the result in the form of the chat type, and the model answers. The tool
// name is not in the catalog, so the result tells the model so.
func TestChatEndpointToolCallingRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range []chatTypeCase{chatTypeCases[0], chatTypeCases[2]} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider := newFakeProvider(t, func(n int, w http.ResponseWriter, _ map[string]any) {
				if n == 1 {
					writeJSON(w, tc.wire.toolCall("call-1", "missing", `{"city":"Paris"}`))
					return
				}
				writeJSON(w, tc.wire.text("The tool is missing."))
			})
			src := &endpointSource{}
			src.add("cle1", tc.preset, tc.protocol, tc.auth, provider.server.URL+tc.basePath, theRealKey)
			cfg := &ir.LLMConfig{Model: "m", Endpoint: "cle1"}
			run := newChatRun(t, userStep(cfg), newLocalGateway(t, src))
			run.executor.toolRegistry = agenttools.NewRegistryOfNames(map[string]string{"lookup": "Look something up"})
			require.NoError(t, run.run())

			assert.Equal(t, "The tool is missing.\n", run.stdout.String())
			requests := provider.all()
			require.Len(t, requests, 2)
			assert.Contains(t, requests[0].Body, `"lookup"`, "the first request offers the tool")
			assert.Contains(t, requests[1].Body, "call-1", "the second request has the tool result")
			assert.Contains(t, requests[1].Body, `tool \"missing\" not found`)
			for _, r := range requests {
				assert.Equal(t, theRealKey, receivedKey(tc, r.Header))
			}
			require.Len(t, run.secrets, 1, "one relay serves the whole tool loop")
		})
	}
}

// The structured answer goes through the relay: the step offers the respond tool,
// and the arguments of the call are the answer.
func TestChatEndpointStructuredOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []chatTypeCase{chatTypeCases[0], chatTypeCases[2]} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
				writeJSON(w, tc.wire.toolCall("call-r", "respond", `{"category":"refund","amount":12.5}`))
			})
			src := &endpointSource{}
			src.add("cle1", tc.preset, tc.protocol, tc.auth, provider.server.URL+tc.basePath, theRealKey)
			step := userStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1"})
			step.OutputSchema = classifySchema()
			run := newChatRun(t, step, newLocalGateway(t, src))
			require.NoError(t, run.run())

			assert.JSONEq(t, `{"amount":12.5,"category":"refund"}`, strings.TrimSpace(run.stdout.String()))
			requests := provider.all()
			require.Len(t, requests, 1)
			assert.Contains(t, requests[0].Body, `"respond"`)
			assert.Equal(t, theRealKey, receivedKey(tc, requests[0].Header))
		})
	}
}

// A model list falls back in order across two endpoints, and across an endpoint
// and the old method. Each endpoint has its own relay.
func TestChatEndpointFallbackAcrossEndpoints(t *testing.T) {
	t.Parallel()

	refusing := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, `{"error":{"message":"this model does not exist"}}`)
	})
	answering := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, anthropicWire.text("second answer"))
	})
	oldMethod := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("old method answer"))
	})
	const otherKey = "sk-ant-OTHER-REAL-KEY-9876543210"
	src := &endpointSource{}
	src.add("first", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, refusing.server.URL+"/v1", theRealKey)
	src.add("second", "anthropic", aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey, answering.server.URL, otherKey)
	gateway := newLocalGateway(t, src)

	t.Run("two endpoints", func(t *testing.T) {
		t.Parallel()
		cfg := &ir.LLMConfig{Models: []ir.ModelEntry{
			{Name: "m1", Endpoint: "first"},
			{Name: "m2", Endpoint: "second"},
		}}
		run := newChatRun(t, userStep(cfg), gateway)
		require.NoError(t, run.run())
		assert.Equal(t, "second answer\n", run.stdout.String())
		require.Len(t, run.secrets, 2, "each endpoint has its own relay")
		assert.NotEqual(t, run.secrets[0], run.secrets[1])

		first := refusing.all()
		require.NotEmpty(t, first)
		assert.Equal(t, "Bearer "+theRealKey, first[0].Header.Get("Authorization"))
		second := answering.all()
		require.Len(t, second, 1)
		assert.Equal(t, otherKey, second[0].Header.Get("X-Api-Key"), "the second endpoint has its own key")
		assert.NotContains(t, second[0].Body, theRealKey)
	})

	t.Run("endpoint then the old method", func(t *testing.T) {
		t.Parallel()
		cfg := &ir.LLMConfig{Models: []ir.ModelEntry{
			{Name: "m1", Endpoint: "first"},
			{Name: "m3", Provider: "local", BaseURL: oldMethod.server.URL + "/v1"},
		}}
		run := newChatRun(t, userStep(cfg), gateway)
		require.NoError(t, run.run())
		assert.Equal(t, "old method answer\n", run.stdout.String())
		require.Len(t, run.secrets, 1, "the old method starts no relay")
		assert.Len(t, oldMethod.all(), 1, "the call of the old method goes straight to the provider")
	})

	t.Run("all fail", func(t *testing.T) {
		t.Parallel()
		cfg := &ir.LLMConfig{Models: []ir.ModelEntry{
			{Name: "m1", Endpoint: "first"},
			{Name: "m1b", Endpoint: "first"},
		}}
		run := newChatRun(t, userStep(cfg), gateway)
		err := run.run()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "all 2 models exhausted")
	})
}

// Each endpoint of the model list is checked when the step starts. An endpoint of
// a later entry that the DAG may not use fails the step before the first request.
func TestChatEndpointStartCheckCoversEachEntry(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("never"))
	})
	src := &endpointSource{}
	src.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
	src.add("cle2", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
	src.forbidden["cle2"] = true

	cfg := &ir.LLMConfig{Models: []ir.ModelEntry{{Name: "a", Endpoint: "cle1"}, {Name: "b", Endpoint: "cle2"}}}
	run := newChatRun(t, userStep(cfg), newLocalGateway(t, src))
	err := run.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "ask": endpoint "cle2": endpoint_forbidden: the DAG "test" is not in the allow list`)
	assert.Empty(t, provider.all(), "no request goes to a provider")
	assert.Empty(t, run.secrets, "no relay started")
}

// The start check fails the step with the reason and the name of the endpoint.
func TestChatEndpointStartCheckFailures(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("never"))
	})
	cases := map[string]func(*endpointSource){
		"endpoint_unknown": func(s *endpointSource) { delete(s.endpoints, "cle1") },
		"endpoint_forbidden": func(s *endpointSource) {
			s.forbidden["cle1"] = true
		},
		"endpoint_key_missing": func(s *endpointSource) {
			ep := s.endpoints["cle1"]
			ep.SecretID = ""
			s.endpoints["cle1"] = ep
		},
	}
	for reason, mutate := range cases {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			src := &endpointSource{}
			src.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
			mutate(src)
			run := newChatRun(t, userStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1"}), newLocalGateway(t, src))
			err := run.run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), `step "ask": endpoint "cle1": `+reason+`: `)
			assert.Empty(t, run.stdout.String())
		})
	}
	assert.Empty(t, provider.all(), "no request goes to a provider")
}

// A process with no gateway fails the step with the reason relay_unavailable.
func TestChatEndpointNeedsAGateway(t *testing.T) {
	t.Parallel()

	run := newChatRun(t, userStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1"}), nil)
	err := run.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "ask": endpoint "cle1": relay_unavailable: this process has no AI gateway`)
}

// A refusal of the relay or of the gateway during the step is a failure of the step
// with the reason. Here the endpoint is deleted after the start check, so the next
// request fails with endpoint_unknown.
func TestChatEndpointRelayRefusalGivesTheReason(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("never"))
	})
	src := &endpointSource{resolveLimit: 1}
	src.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
	stream := false
	run := newChatRun(t, userStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1", Stream: &stream}), newLocalGateway(t, src))
	err := run.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint_unknown")
	assert.Contains(t, err.Error(), `endpoint "cle1": the AI relay refused a request: endpoint_unknown`)
	assert.Empty(t, provider.all())
	assert.Contains(t, run.stderr.String(), "ai relay: refuse POST /v1/chat/completions: endpoint_unknown")
}

// A status of the provider is not changed: a 401 of the provider reaches the step
// as an authentication failure of the provider, with no key in the message.
func TestChatEndpointUpstreamStatusPasses(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, `{"error":{"message":"invalid api key"}}`)
	})
	src := &endpointSource{}
	src.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
	stream := false
	run := newChatRun(t, userStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1", Stream: &stream}), newLocalGateway(t, src))
	err := run.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "invalid api key")
	assert.NotContains(t, err.Error(), theRealKey)
}

// An endpoint with a local preset and no key is valid: the provider receives no
// credential from the gateway, and the relay secret does not leave the process.
func TestChatEndpointWithNoKey(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("local answer"))
	})
	src := &endpointSource{}
	src.add("lab", "ollama", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", "")
	stream := false
	run := newChatRun(t, userStep(&ir.LLMConfig{Model: "llama3", Endpoint: "lab", Stream: &stream}), newLocalGateway(t, src))
	require.NoError(t, run.run())
	assert.Equal(t, "local answer\n", run.stdout.String())
	requests := provider.all()
	require.Len(t, requests, 1)
	assert.Empty(t, requests[0].Header.Get("Authorization"))
	assert.Empty(t, requests[0].Header.Get("X-Api-Key"))
}

// The old method does not change. With no endpoint, the step needs no gateway,
// reads the key from the environment variable that api_key_name names, and the call
// goes from the step to the provider.
func TestChatOldMethodNeedsNoGateway(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("old answer"))
	})
	stream := false
	cfg := &ir.LLMConfig{
		Provider: "openai", Model: "gpt-4o", BaseURL: provider.server.URL + "/v1",
		APIKeyName: "DAGU_TEST_CHAT_KEY", Stream: &stream,
	}
	run := newChatRun(t, userStep(cfg), nil, runctx.WithEnvVars("DAGU_TEST_CHAT_KEY=sk-from-the-environment"))
	require.NoError(t, run.run())
	assert.Equal(t, "old answer\n", run.stdout.String())
	requests := provider.all()
	require.Len(t, requests, 1)
	assert.Equal(t, "/v1/chat/completions", requests[0].Path)
	assert.Equal(t, "Bearer sk-from-the-environment", requests[0].Header.Get("Authorization"))
	assert.Empty(t, run.secrets, "the old method starts no relay")
}

// The streamed answer reaches stdout chunk by chunk: the first event arrives before
// the provider ends the response.
func TestChatEndpointStreamsThroughTheRelay(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"first"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":" second"}}]}`+"\n\ndata: [DONE]\n\n")
	})
	src := &endpointSource{}
	src.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
	run := newChatRun(t, userStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1"}), newLocalGateway(t, src))

	done := make(chan error, 1)
	go func() { done <- run.run() }()
	require.Eventually(t, func() bool {
		return strings.Contains(run.stdout.String(), "first")
	}, 5*time.Second, 10*time.Millisecond, "the first event comes before the end of the response")
	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, "first second\n", run.stdout.String())
}

// A model that the models list of the endpoint lacks fails the step at start with
// the reason model_unknown. No request goes to the provider and no relay starts.
func TestChatEndpointModelNotInTheList(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("fine"))
	})
	src := &endpointSource{lists: map[string][]string{"cle1": {"gpt-a", "gpt-b"}}}
	src.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
	stream := false

	t.Run("listed", func(t *testing.T) {
		run := newChatRun(t, userStep(&ir.LLMConfig{Model: "gpt-b", Endpoint: "cle1", Stream: &stream}), newLocalGateway(t, src))
		require.NoError(t, run.run())
		assert.Contains(t, run.stdout.String(), "fine")
	})

	before := len(provider.all())
	t.Run("not listed", func(t *testing.T) {
		run := newChatRun(t, userStep(&ir.LLMConfig{Model: "gpt-ghost", Endpoint: "cle1", Stream: &stream}), newLocalGateway(t, src))
		err := run.run()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `step "ask": endpoint "cle1": model_unknown: the model "gpt-ghost" is not in the models list of the endpoint (read at `)
		assert.Empty(t, run.secrets, "no relay started")
	})

	t.Run("each entry of the fallback list", func(t *testing.T) {
		cfg := &ir.LLMConfig{Models: []ir.ModelEntry{{Name: "gpt-a", Endpoint: "cle1"}, {Name: "gpt-ghost", Endpoint: "cle1"}}, Stream: &stream}
		run := newChatRun(t, userStep(cfg), newLocalGateway(t, src))
		err := run.run()
		require.Error(t, err, "the second entry fails the step before the first request")
		assert.Contains(t, err.Error(), `model_unknown: the model "gpt-ghost"`)
		assert.Empty(t, run.secrets)
	})
	assert.Len(t, provider.all(), before, "a failed check sends no request")
}

// A reference in the model is checked after resolution. A step with an endpoint
// and an unknown endpoint fails with endpoint_unknown before the model check.
func TestChatEndpointModelCheckAfterResolutionAndOrder(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("fine"))
	})
	src := &endpointSource{lists: map[string][]string{"cle1": {"gpt-a"}}}
	src.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)
	stream := false

	run := newChatRun(t, userStep(&ir.LLMConfig{Model: "${MODEL}", Endpoint: "cle1", Stream: &stream}), newLocalGateway(t, src),
		runctx.WithEnvVars("MODEL=gpt-a"))
	require.NoError(t, run.run())
	assert.Equal(t, []string{"cle1/gpt-a"}, src.modelChecks, "the check gets the resolved name")

	gone := &endpointSource{lists: map[string][]string{"nope": {"gpt-a"}}}
	run = newChatRun(t, userStep(&ir.LLMConfig{Model: "gpt-ghost", Endpoint: "nope"}), newLocalGateway(t, gone))
	err := run.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "ask": endpoint "nope": endpoint_unknown: `)
	assert.Empty(t, gone.modelChecks, "an unknown endpoint ends the check first")
}
