// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	dockerexec "github.com/dagucloud/dagu/v2/internal/runtime/builtin/docker"
	"github.com/dagucloud/dagu/v2/internal/runtime/modelusage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinProviderInvocations(t *testing.T) {
	tests := []struct {
		name          string
		config        map[string]any
		expectedBin   string
		expectedArgs  []string
		expectedStdin string
	}{
		{"claude", map[string]any{"provider": "claude"}, "claude", []string{"-p", "hello"}, "context"},
		{"codex", map[string]any{"provider": "codex"}, "codex", []string{"exec", "hello", "--skip-git-repo-check"}, "context"},
		{"copilot", map[string]any{"provider": "copilot"}, "copilot", []string{"-p", "hello"}, "context"},
		{"opencode", map[string]any{"provider": "opencode"}, "opencode", []string{"run", "hello"}, "context"},
		{"pi", map[string]any{"provider": "pi"}, "pi", []string{"-p", "hello"}, "context"},
		{"gemini", map[string]any{"provider": "gemini"}, "gemini", []string{"-p", "hello"}, "context"},
		{"cursor", map[string]any{"provider": "cursor"}, "cursor-agent", []string{"-p", "hello\n\ncontext", "--output-format", "text"}, ""},
		{"cline", map[string]any{"provider": "cline", "model": "model-id"}, "cline", []string{"--model", "model-id", "hello"}, "context"},
		{"aider", map[string]any{"provider": "aider"}, "aider", []string{"--message", "hello\n\ncontext"}, ""},
		{"qwen", map[string]any{"provider": "qwen"}, "qwen", []string{"-p", "hello"}, "context"},
		{"goose", map[string]any{"provider": "goose"}, "goose", []string{"run", "--text", "hello\n\ncontext", "--quiet"}, ""},
		{"kiro", map[string]any{"provider": "kiro"}, "kiro-cli", []string{"chat", "--no-interactive", "hello"}, "context"},
		{"droid", map[string]any{"provider": "droid"}, "droid", []string{"exec", "hello\n\ncontext"}, ""},
		{"amp", map[string]any{"provider": "amp"}, "amp", []string{"-x", "hello"}, "context"},
		{"deepseek", map[string]any{"provider": "deepseek", "patch": "overlay.yml"}, "dsh", []string{"--profile", "headless", "--patch", "overlay.yml", "hello\n\ncontext"}, ""},
		{"kilo", map[string]any{"provider": "kilo"}, "kilo", []string{"run", "hello", "--auto"}, "context"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configs, err := buildProviderConfigs(tt.config, nil)
			require.NoError(t, err)
			require.Len(t, configs, 1)
			assert.Equal(t, tt.expectedBin, configs[0].binaryName())

			args, stdin, err := configs[0].buildInvocation("hello", "context")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedArgs, args)
			assert.Equal(t, tt.expectedStdin, mustReadAll(t, stdin))
		})
	}
}

func TestManagedOpenCodeMode(t *testing.T) {
	t.Parallel()

	mode, err := opencodehost.Mode(map[string]any{
		"provider": "opencode",
		"model":    "openai/gpt-5",
	})
	require.NoError(t, err)
	assert.True(t, mode.Managed)
	assert.False(t, mode.Required)
	assert.Empty(t, mode.Reason)

	mode, err = opencodehost.Mode(map[string]any{
		"provider": "opencode",
		"port":     4096,
	})
	require.NoError(t, err)
	assert.False(t, mode.Managed)
	assert.False(t, mode.Required)
	assert.Contains(t, mode.Reason, "CLI integration")

	mode, err = opencodehost.Mode(map[string]any{
		"provider": "opencode",
		"managed":  true,
		"port":     4096,
	})
	require.Error(t, err)
	assert.True(t, mode.Required)
}

func TestNormalizeOpenCodeMessages(t *testing.T) {
	t.Parallel()

	messages := []openCodeMessage{{
		Info: json.RawMessage(`{"role":"assistant","id":"message-1","providerID":"openai","modelID":"gpt-5"}`),
		Parts: []json.RawMessage{
			json.RawMessage(`{"type":"text","text":"Done"}`),
			json.RawMessage(`{"type":"text","text":"All tests passed"}`),
			json.RawMessage(`{"id":"part-2","type":"tool","tool":"bash","callID":"call-1","state":{"status":"completed","input":{"command":"go test ./..."}}}`),
			json.RawMessage(`{"id":"part-3","type":"step-finish","tokens":{"input":12,"output":8,"reasoning":3,"total":23},"cost":0.25}`),
		},
	}, {
		Info: json.RawMessage(`{"role":"assistant","id":"message-2","providerID":"openai","modelID":"gpt-5"}`),
		Parts: []json.RawMessage{
			json.RawMessage(`{"type":"text","text":"Follow-up"}`),
			json.RawMessage(`{"type":"step-finish","tokens":{"input":2,"output":3,"total":5},"cost":0.05}`),
		},
	}}

	chat, events, usage, models := normalizeOpenCodeMessages(messages)

	require.Len(t, chat, 2)
	assert.Equal(t, ir.LLMRoleAssistant, chat[0].Role)
	assert.Equal(t, "Done\nAll tests passed", chat[0].Content)
	require.Len(t, chat[0].ToolCalls, 1)
	assert.Equal(t, "bash", chat[0].ToolCalls[0].Function.Name)
	assert.Contains(t, chat[0].ToolCalls[0].Function.Arguments, "go test ./...")
	require.NotNil(t, chat[0].Metadata)
	assert.Equal(t, 23, chat[0].Metadata.TotalTokens)
	require.NotNil(t, chat[1].Metadata)
	assert.Equal(t, 5, chat[1].Metadata.TotalTokens)
	require.Len(t, events, 6)
	assert.NotEqual(t, events[0].ID, events[1].ID)
	assert.Equal(t, int64(28), usage.TotalTokens)
	assert.Equal(t, 0.30, usage.Cost)
	require.Len(t, models, 1)
	assert.Equal(t, ir.ModelUsage{
		Provider: "openai", Model: "gpt-5", Calls: 2, InputTokens: 14, OutputTokens: 11, ReasoningTokens: 3,
		Cost: 0.30, CostReportedCalls: 2,
	}, models[0])
}

// A step's model usage from an OpenCode session names the model of each
// message and counts the cached prompt in its input, which OpenCode reports
// apart.
func TestOpenCodeModelUsageCountsCache(t *testing.T) {
	t.Parallel()

	_, _, _, models := normalizeOpenCodeMessages([]openCodeMessage{{
		Info:  json.RawMessage(`{"role":"assistant","id":"message-1","providerID":"anthropic","modelID":"claude-sonnet-4-6"}`),
		Parts: []json.RawMessage{json.RawMessage(`{"type":"step-finish","tokens":{"input":5,"output":20,"reasoning":4,"cache":{"read":300,"write":40}}}`)},
	}, {
		Info:  json.RawMessage(`{"role":"user","id":"message-2"}`),
		Parts: []json.RawMessage{json.RawMessage(`{"type":"text","text":"more"}`)},
	}})
	assert.Equal(t, []ir.ModelUsage{{
		Provider: "anthropic", Model: "claude-sonnet-4-6", Calls: 1,
		InputTokens: 345, CachedInputTokens: 300, CacheWriteTokens: 40, OutputTokens: 20, ReasoningTokens: 4,
	}}, models)
}

// Refreshing a session again reports its latest total, which replaces the
// one before, so the step's usage is the session's, not the sum of refreshes.
func TestManagedOpenCodeRefreshReportsModelUsage(t *testing.T) {
	t.Parallel()

	turn := `[{"info":{"role":"assistant","id":"message-1","providerID":"openai","modelID":"gpt-5"},"parts":[{"type":"step-finish","tokens":{"input":10,"output":2},"cost":0.01}]}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, turn)
	}))
	t.Cleanup(server.Close)
	exec := &harnessExecutor{agentSession: &ir.AgentSession{Generation: 1}}
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Password: "test"}, http: server.Client()}
	meter := modelusage.NewMeter(nil)
	ctx := modelusage.WithMeter(t.Context(), meter)

	for range 3 {
		_, err := exec.refreshManagedMessages(ctx, client, "session-1")
		require.NoError(t, err)
	}

	usage := meter.Snapshot()
	require.NotNil(t, usage)
	assert.Equal(t, []ir.ModelUsage{{Provider: "openai", Model: "gpt-5", Calls: 1, InputTokens: 10, OutputTokens: 2, Cost: 0.01, CostReportedCalls: 1}}, usage.Total())
}

func TestManagedOpenCodeResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		messages []openCodeMessage
		wantText string
		wantDone bool
		wantErr  string
	}{
		{
			name: "completed response",
			messages: []openCodeMessage{
				openCodeTestMessage(`{"id":"assistant-old","role":"assistant","parentID":"prompt-old","finish":"stop","time":{"completed":1}}`, `{"type":"text","text":"Old result"}`),
				openCodeTestMessage(`{"id":"assistant-new","role":"assistant","parentID":"prompt-1","finish":"stop","time":{"completed":2}}`, `{"type":"text","text":"Done"}`),
			},
			wantText: "Done",
			wantDone: true,
		},
		{
			name: "completed empty response",
			messages: []openCodeMessage{
				openCodeTestMessage(`{"id":"assistant-1","role":"assistant","parentID":"prompt-1","finish":"stop","time":{"completed":1}}`),
			},
			wantDone: true,
		},
		{
			name: "unfinished response",
			messages: []openCodeMessage{
				openCodeTestMessage(`{"id":"assistant-1","role":"assistant","parentID":"prompt-1"}`, `{"type":"text","text":"Working"}`),
			},
		},
		{
			name: "response error",
			messages: []openCodeMessage{
				openCodeTestMessage(`{"id":"assistant-1","role":"assistant","parentID":"prompt-1","error":{"name":"ProviderModelNotFoundError","data":{"message":"Model not found"}}}`),
			},
			wantDone: true,
			wantErr:  "Model not found",
		},
		{
			name: "unrelated response",
			messages: []openCodeMessage{
				openCodeTestMessage(`{"id":"assistant-1","role":"assistant","parentID":"prompt-other","finish":"stop","time":{"completed":1}}`, `{"type":"text","text":"Wrong result"}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, done, err := managedOpenCodeResult(tt.messages, "prompt-1")
			assert.Equal(t, tt.wantText, text)
			assert.Equal(t, tt.wantDone, done)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

func TestOpenCodeSessionError(t *testing.T) {
	t.Parallel()

	properties := json.RawMessage(`{"sessionID":"session-1","error":{"name":"ProviderModelNotFoundError","data":{"message":"Model not found"}}}`)
	message, ok := openCodeSessionError(properties, "session-1")
	assert.True(t, ok)
	assert.Equal(t, "Model not found", message)

	_, ok = openCodeSessionError(properties, "session-2")
	assert.False(t, ok)
}

func TestManagedOpenCodeRetryUsesNewSessionGeneration(t *testing.T) {
	t.Parallel()

	const providerError = "Model not found: openrouter/deepseek/deepseek-v4-flash"
	type submission struct {
		sessionID string
		messageID string
		prompt    string
	}

	var mu sync.Mutex
	created := 0
	messages := make(map[string][]openCodeMessage)
	submissions := make([]submission, 0, 2)
	promptSubmitted := []chan struct{}{make(chan struct{}), make(chan struct{})}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config":
			_, _ = io.WriteString(w, `{"share":"disabled"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			mu.Lock()
			created++
			sessionID := "session-" + strconv.Itoa(created)
			mu.Unlock()
			_, _ = io.WriteString(w, `{"id":"`+sessionID+`"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			mu.Lock()
			generation := created
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-promptSubmitted[generation-1]:
			case <-r.Context().Done():
				return
			}
			if generation == 1 {
				_, _ = io.WriteString(w, `data: {"type":"session.error","properties":{"sessionID":"session-1","error":{"name":"ProviderModelNotFoundError","data":{"message":"`+providerError+`"}}}}`+"\n\n")
				w.(http.Flusher).Flush()
			}
			_, _ = io.WriteString(w, `data: {"type":"session.idle","properties":{"sessionID":"session-`+strconv.Itoa(generation)+`"}}`+"\n\n")
			w.(http.Flusher).Flush()
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			sessionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/session/"), "/prompt_async")
			var body struct {
				MessageID string `json:"messageID"`
				Parts     []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"parts"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			generation := created
			submissions = append(submissions, submission{sessionID: sessionID, messageID: body.MessageID, prompt: body.Parts[0].Text})
			messages[sessionID] = []openCodeMessage{openCodeTestMessage(`{"id":"`+body.MessageID+`","role":"user"}`, `{"type":"text","text":"`+body.Parts[0].Text+`"}`)}
			if generation == 2 {
				messages[sessionID] = append(messages[sessionID], openCodeTestMessage(
					`{"id":"assistant-2","role":"assistant","parentID":"`+body.MessageID+`","finish":"stop","time":{"completed":1}}`,
					`{"type":"text","text":"Completed on retry"}`,
				))
			}
			mu.Unlock()
			close(promptSubmitted[generation-1])
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/message"):
			sessionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/session/"), "/message")
			mu.Lock()
			response := append([]openCodeMessage(nil), messages[sessionID]...)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(response)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	exec := &harnessExecutor{prompt: "Do the work", workDir: t.TempDir()}
	ctx := runtime.WithEnv(t.Context(), runtime.Env{})
	host := opencodehost.Config{URL: server.URL, Password: "test", InstanceID: "host-1"}

	stdout, err := exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, host)
	require.Nil(t, stdout)
	require.EqualError(t, err, providerError)
	require.NotNil(t, exec.agentSession)
	assert.Equal(t, ir.AgentSessionFailed, exec.agentSession.State)
	assert.Equal(t, providerError, exec.agentSession.LastError)
	assert.Equal(t, 1, exec.agentSession.Generation)
	failedSessionID := exec.agentSession.SessionID
	failedMessageID := exec.agentSession.PromptMessageID

	stdout, err = exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, host)
	require.NoError(t, err)
	output, readErr := io.ReadAll(stdout)
	require.NoError(t, readErr)
	require.NoError(t, cleanupStdoutSpool(stdout))
	assert.Equal(t, "Completed on retry\n", string(output))
	assert.Equal(t, ir.AgentSessionSucceeded, exec.agentSession.State)
	assert.Empty(t, exec.agentSession.LastError)
	assert.Equal(t, 2, exec.agentSession.Generation)
	assert.Equal(t, "session-2", exec.agentSession.SessionID)
	assert.Equal(t, failedSessionID, exec.agentSession.DiscardedSessionID)
	assert.True(t, exec.agentSession.DiscardedOwned)
	assert.NotEqual(t, failedMessageID, exec.agentSession.PromptMessageID)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, submissions, 2)
	assert.Equal(t, "Do the work", submissions[0].prompt)
	assert.Equal(t, submissions[0].prompt, submissions[1].prompt)
	assert.Equal(t, "session-1", submissions[0].sessionID)
	assert.Equal(t, "session-2", submissions[1].sessionID)
	assert.Equal(t, failedMessageID, submissions[0].messageID)
	assert.Equal(t, exec.agentSession.PromptMessageID, submissions[1].messageID)
	assert.True(t, strings.HasPrefix(submissions[0].messageID, "msg_dagu_"))
}

func openCodeTestMessage(info string, parts ...string) openCodeMessage {
	message := openCodeMessage{Info: json.RawMessage(info), Parts: make([]json.RawMessage, len(parts))}
	for i := range parts {
		message.Parts[i] = json.RawMessage(parts[i])
	}
	return message
}

func TestOpenCodeClientClassifiesNotFoundByEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Password: "secret"}, http: server.Client()}

	err := client.json(t.Context(), http.MethodGet, "/session/session-1", nil, nil)
	require.ErrorIs(t, err, errManagedSessionUnavailable)
	err = client.json(t.Context(), http.MethodGet, "/config", nil, nil)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errManagedSessionUnavailable)
}

func TestHarnessStopContinuesAfterManagedAbort(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	fallbackStopped := false
	exec := &harnessExecutor{
		managedHost: opencodehost.Config{
			URL: server.URL, Username: "opencode", Password: "secret", InstanceID: "host-1",
		},
		agentSession:          &ir.AgentSession{SessionID: "session-1"},
		sharedContainerCancel: func() { fallbackStopped = true },
	}

	require.NoError(t, exec.Stop(cmdutil.TerminationIntent{}))
	assert.True(t, fallbackStopped)
}

func TestOpenCodeInterruptAwaitsIdle(t *testing.T) {
	t.Parallel()

	const statusPoll = "GET /session/status"
	for _, tc := range []struct {
		name            string
		interruptStatus int
		busy            bool
		wantErr         error
		wantPolls       int
	}{
		{name: "settles after interrupt", busy: true, wantPolls: 2},
		{name: "rejected interrupt of idle session", interruptStatus: http.StatusInternalServerError, wantPolls: 1},
		{name: "missing session", interruptStatus: http.StatusNotFound, busy: true},
		{name: "still busy at deadline", interruptStatus: http.StatusInternalServerError, busy: true, wantErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host := newManagedTestHost(t, func(h *managedTestHost) {
				h.interruptStatus = tc.interruptStatus
				h.busy["session-1"] = tc.busy
			})
			client := &openCodeClient{host: host.config, http: newOpenCodeHTTPClient(time.Second)}
			ctx := t.Context()
			if tc.wantErr != nil {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
				defer cancel()
			}

			err := client.interruptAndAwaitIdle(ctx, "session-1")

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Contains(t, err.Error(), "still busy")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "POST /session/session-1/abort", host.requestLog()[0])
			assert.Equal(t, tc.wantPolls, host.count(statusPoll))
		})
	}

	t.Run("unreachable host", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()
		client := &openCodeClient{host: opencodehost.Config{URL: server.URL}, http: newOpenCodeHTTPClient(time.Second)}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		require.ErrorIs(t, client.interruptAndAwaitIdle(ctx, "session-1"), errManagedHostUnavailable)
		require.NoError(t, ctx.Err(), "an unreachable host must not consume the cleanup budget")
	})
}

// In a session Dagu does not own, an active turn may be the user's, so only a
// reply shows that Dagu's prompt started.
func TestOpenCodePromptStartEvidence(t *testing.T) {
	t.Parallel()

	host := newManagedTestHost(t, func(h *managedTestHost) { h.busy["session-1"] = true })
	client := &openCodeClient{host: host.config, http: newOpenCodeHTTPClient(time.Second)}
	require.NoError(t, client.awaitPromptStart(t.Context(), "session-1", "msg-1", true))

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, client.awaitPromptStart(ctx, "session-1", "msg-1", false), context.DeadlineExceeded)
}

// Closing the idle connections of http.DefaultTransport, as
// httptest.Server.Close does, can fail a request that is in flight on it. The
// OpenCode connection must survive such a reset. Not parallel: it resets the
// shared default transport.
func TestOpenCodeConnSurvivesTransportReset(t *testing.T) {
	var mu sync.Mutex
	var remoteAddrs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		remoteAddrs = append(remoteAddrs, r.RemoteAddr)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	host := opencodehost.Config{URL: server.URL, Password: "secret", InstanceID: "host-1"}

	require.NoError(t, abortManagedOpenCode(t.Context(), host, "session-1", ""))
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	require.NoError(t, abortManagedOpenCode(t.Context(), host, "session-1", ""))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, remoteAddrs, 2)
	assert.Equal(t, remoteAddrs[0], remoteAddrs[1])
}

func TestManagedOpenCodeCleanRestartCreatesNewSession(t *testing.T) {
	t.Parallel()

	requestedPath := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath <- r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/config" {
			_, _ = io.WriteString(w, `{"share":"disabled"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"session-new"}`)
	}))
	t.Cleanup(server.Close)

	exec := &harnessExecutor{
		workDir:      t.TempDir(),
		agentSession: &ir.AgentSession{SessionID: "session-old", RestartPending: true},
	}
	client := &openCodeClient{
		host:      opencodehost.Config{URL: server.URL, Password: "test", InstanceID: "host-1"},
		directory: exec.workDir,
		http:      server.Client(),
	}

	sessionID, err := exec.ensureManagedSession(t.Context(), client, providerConfig{
		flags: map[string]any{"session": "session-configured", "fork": true},
	}, true)

	require.NoError(t, err)
	assert.Equal(t, "session-new", sessionID)
	assert.Equal(t, "GET /config", <-requestedPath)
	assert.Equal(t, "POST /session", <-requestedPath)
}

func TestManagedOpenCodeTransportLossWaitsForRestart(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"share":"disabled"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"session-1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/session/session-1/prompt_async":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	exec := &harnessExecutor{prompt: "Do the work", workDir: t.TempDir()}
	ctx := runtime.WithEnv(t.Context(), runtime.Env{})
	stdout, err := exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, opencodehost.Config{
		URL: server.URL, Password: "test", InstanceID: "host-1",
	})

	require.NoError(t, err)
	require.NoError(t, cleanupStdoutSpool(stdout))
	require.NotNil(t, exec.agentSession)
	assert.Equal(t, ir.AgentSessionUnavailable, exec.agentSession.State)
	status, err := exec.DetermineNodeStatus()
	require.NoError(t, err)
	assert.Equal(t, ir.NodeWaiting, status)
}

// A cancelled run interrupts its session and records whether the host
// confirmed that the session stopped.
func TestManagedOpenCodeCancelInterruptsSession(t *testing.T) {
	t.Parallel()

	const prompt = "POST /session/session-1/prompt_async"
	for _, tc := range []struct {
		name            string
		cancelAt        string
		startAfterPolls int
		drop            bool
		wantEvent       string
	}{
		{name: "running", cancelAt: "running", wantEvent: "aborted"},
		{name: "turn starts after acceptance", cancelAt: "running", startAfterPolls: 2, wantEvent: "aborted"},
		{name: "prompt in flight", cancelAt: "submitting", wantEvent: "aborted"},
		{name: "before submission", cancelAt: "before submit", wantEvent: "aborted"},
		{name: "before the session", cancelAt: "before session", wantEvent: "aborted"},
		{name: "unconfirmed", cancelAt: "running", drop: true, wantEvent: "abort-unverified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(runtime.WithEnv(t.Context(), runtime.Env{}))
			defer cancel()
			host := newManagedTestHost(t, func(h *managedTestHost) {
				h.dropAfterInterrupt = tc.drop
				h.startAfterPolls = tc.startAfterPolls
				if tc.cancelAt == "submitting" {
					h.onPrompt = func(*http.Request) { cancel() }
				}
			})
			exec := &harnessExecutor{prompt: "Do the work", workDir: t.TempDir()}
			switch tc.cancelAt {
			case "running":
				exec.SetProgressCallback(onManagedRunning(exec, cancel))
			case "before submit":
				exec.SetProgressCallback(func() {
					if session := exec.GetAgentSession(); session.State == ir.AgentSessionStarting && session.PromptMessageID != "" {
						cancel()
					}
				})
			case "before session":
				exec.SetProgressCallback(cancel)
			}

			stdout, err := exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, host.config)

			require.Nil(t, stdout)
			require.ErrorIs(t, err, context.Canceled)
			session := exec.GetAgentSession()
			assert.Equal(t, ir.AgentSessionAborted, session.State)
			assert.Equal(t, tc.wantEvent, session.Events[len(session.Events)-1].Status)
			if tc.cancelAt == "before session" {
				assert.NotContains(t, host.requestLog(), "POST /session", "a cancelled run creates no session")
			}
			if tc.cancelAt == "before submit" || tc.cancelAt == "before session" {
				assert.False(t, session.PromptSent)
				assert.NotContains(t, host.requestLog(), prompt, "a cancelled run submits no prompt")
				assert.NotContains(t, host.requestLog(), "POST /session/session-1/abort", "no turn of Dagu's to interrupt")
				return
			}
			// A submission completes despite cancellation, so the run knows the host took the prompt.
			assert.True(t, session.PromptSent)
			assert.True(t, host.wasInterrupted("session-1"), "the interrupt must reach the running turn")
			if tc.drop {
				require.ErrorIs(t, err, errManagedHostUnavailable)
				assert.Contains(t, session.LastError, "did not confirm")
				return
			}
			assert.Empty(t, session.LastError)
			assert.False(t, host.isBusy("session-1"), "the session settled before the run returned")
		})
	}
}

// Stopping a managed attempt ends the step without starting the legacy CLI or
// the next provider. Not parallel: the host is injected through the environment.
func TestManagedOpenCodeStopEndsStep(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	for _, stage := range []string{"before start", "running"} {
		t.Run(stage, func(t *testing.T) {
			host := newManagedTestHost(t, nil)
			t.Setenv("_DAGU_INTERNAL_OPENCODE_URL", host.config.URL)
			t.Setenv("_DAGU_INTERNAL_OPENCODE_USERNAME", host.config.Username)
			t.Setenv("_DAGU_INTERNAL_OPENCODE_PASSWORD", host.config.Password)
			t.Setenv("_DAGU_INTERNAL_OPENCODE_INSTANCE_ID", host.config.InstanceID)
			marker := filepath.Join(t.TempDir(), "cli-ran")
			definition := &ir.HarnessDefinition{
				Binary:     writeHarnessTestBinary(t, "opencode", "#!/bin/sh\ntouch \""+marker+"\"\n"),
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			}
			exec := &harnessExecutor{
				stdout:  &strings.Builder{},
				stderr:  &strings.Builder{},
				prompt:  "Do the work",
				workDir: t.TempDir(),
				configs: []providerConfig{
					{name: "opencode", definition: definition, managed: true},
					{name: "fallback", definition: definition},
				},
			}
			stop := func() { _ = exec.Stop(cmdutil.TerminationFromSignal(syscall.SIGTERM)) }
			if stage == "running" {
				exec.SetProgressCallback(onManagedRunning(exec, stop))
			} else {
				stop()
			}

			err := exec.Run(runtime.WithEnv(t.Context(), runtime.Env{}))

			require.ErrorIs(t, err, context.Canceled)
			assert.NoFileExists(t, marker)
			if stage == "running" {
				assert.Equal(t, ir.AgentSessionAborted, exec.GetAgentSession().State)
				assert.Contains(t, host.requestLog(), "POST /session/session-1/abort")
				return
			}
			assert.Empty(t, host.requestLog())
		})
	}
}

// A retry discards the previous session. Its replacement starts only once the
// previous attempt's work has stopped.
func TestManagedOpenCodeRetrySettlesDiscardedSession(t *testing.T) {
	t.Parallel()

	t.Run("interrupts owned session", func(t *testing.T) {
		t.Parallel()
		host := newManagedTestHost(t, func(h *managedTestHost) { h.busy["session-old"] = true })
		exec := &harnessExecutor{prompt: "Do the work", workDir: t.TempDir(), agentSession: &ir.AgentSession{
			Provider: "opencode", Generation: 1, SessionID: "session-old", SessionOwned: true,
			PromptSent: true, State: ir.AgentSessionAborted,
		}}
		ctx, cancel := context.WithCancel(runtime.WithEnv(t.Context(), runtime.Env{}))
		defer cancel()
		exec.SetProgressCallback(onManagedRunning(exec, cancel))

		_, err := exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, host.config)

		require.ErrorIs(t, err, context.Canceled)
		requests := host.requestLog()
		interrupt := slices.Index(requests, "POST /session/session-old/abort")
		created := slices.Index(requests, "POST /session")
		require.GreaterOrEqual(t, interrupt, 0, "the discarded session must be interrupted")
		require.Greater(t, created, interrupt+2, "the replacement waits until the discarded session settles")
		session := exec.GetAgentSession()
		assert.Equal(t, 2, session.Generation)
		assert.Equal(t, "session-1", session.SessionID)
		assert.Equal(t, "session-old", session.DiscardedSessionID)
	})

	t.Run("keeps settling after stop", func(t *testing.T) {
		t.Parallel()
		host := newManagedTestHost(t, func(h *managedTestHost) { h.busy["session-old"] = true })
		exec := &harnessExecutor{prompt: "Do the work", workDir: t.TempDir(), agentSession: &ir.AgentSession{
			Provider: "opencode", Generation: 1, SessionID: "session-old", SessionOwned: true,
			PromptSent: true, State: ir.AgentSessionAborted,
		}}
		ctx, cancel := context.WithCancel(runtime.WithEnv(t.Context(), runtime.Env{}))
		defer cancel()
		// The stop arrives as the new attempt starts, before the discarded session settles.
		exec.SetProgressCallback(cancel)

		_, err := exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, host.config)

		require.ErrorIs(t, err, context.Canceled)
		assert.True(t, host.wasInterrupted("session-old"))
		assert.False(t, host.isBusy("session-old"), "the discarded session settled before the run returned")
		assert.NotContains(t, host.requestLog(), "POST /session", "a stopped attempt creates no replacement")
		session := exec.GetAgentSession()
		assert.Equal(t, ir.AgentSessionAborted, session.State)
		assert.Equal(t, "aborted", session.Events[len(session.Events)-1].Status)
	})

	t.Run("waits for session it does not own", func(t *testing.T) {
		t.Parallel()
		host := newManagedTestHost(t, func(h *managedTestHost) { h.busy["session-user"] = true })
		client := &openCodeClient{host: host.config, http: newOpenCodeHTTPClient(time.Second)}
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()

		err := settleDiscardedSession(ctx, client, "session-user", false)

		require.ErrorContains(t, err, "session-user from the previous attempt may still be running")
		assert.NotContains(t, host.requestLog(), "POST /session/session-user/abort", "never interrupt a session Dagu does not own")
	})

	t.Run("checks again on the next attempt", func(t *testing.T) {
		t.Parallel()
		host := newManagedTestHost(t, func(h *managedTestHost) { h.statusDown = true })
		exec := &harnessExecutor{prompt: "Do the work", workDir: t.TempDir(), agentSession: &ir.AgentSession{
			Provider: "opencode", Generation: 1, SessionID: "session-user",
			PromptSent: true, State: ir.AgentSessionFailed,
		}}
		ctx := runtime.WithEnv(t.Context(), runtime.Env{})

		_, err := exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, host.config)

		require.NoError(t, err)
		assert.Equal(t, ir.AgentSessionUnavailable, exec.GetAgentSession().State)
		assert.NotContains(t, host.requestLog(), "POST /session", "no replacement before the discarded session settles")

		host.setStatusDown(false)
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		exec.SetProgressCallback(onManagedRunning(exec, cancel))
		_, err = exec.runManagedOpenCode(runCtx, providerConfig{flags: map[string]any{}}, host.config)
		require.ErrorIs(t, err, context.Canceled)
		assert.Contains(t, host.requestLog(), "POST /session")
		session := exec.GetAgentSession()
		assert.Equal(t, 2, session.Generation)
		assert.Equal(t, "session-user", session.DiscardedSessionID)
	})
}

// A prompt whose turn was never seen to start may still start in its session.
// A retry then submits it to that session again rather than replacing it.
func TestManagedOpenCodeRetryRedeliversUnstartedPrompt(t *testing.T) {
	t.Parallel()

	// The host takes the prompt, but its turn has not started when the
	// cancelled run loses the host's status.
	host := newManagedTestHost(t, func(h *managedTestHost) {
		h.startAfterPolls = 1000
		h.interruptStatus = http.StatusInternalServerError
	})
	exec := &harnessExecutor{prompt: "Do the work", workDir: t.TempDir()}
	ctx, cancel := context.WithCancel(runtime.WithEnv(t.Context(), runtime.Env{}))
	defer cancel()
	exec.SetProgressCallback(onManagedRunning(exec, func() {
		host.setStatusDown(true)
		cancel()
	}))
	_, err := exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, host.config)
	require.ErrorIs(t, err, errManagedHostUnavailable)
	session := exec.GetAgentSession()
	assert.Equal(t, "abort-unverified", session.Events[len(session.Events)-1].Status)
	assert.False(t, session.PromptSent, "an unstarted prompt is delivered again")
	assert.Contains(t, host.requestLog(), "POST /session/session-1/abort", "a turn the lost status hid is still interrupted")
	assert.Contains(t, session.LastError, "/abort returned 500", "the rejected interrupt is reported")

	host.setStatusDown(false)
	host.setStartAfterPolls(0)
	host.setInterruptStatus(0)
	ctx, cancel = context.WithCancel(runtime.WithEnv(t.Context(), runtime.Env{}))
	defer cancel()
	exec.SetProgressCallback(onManagedRunning(exec, cancel))
	_, err = exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{}}, host.config)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, host.count("POST /session"), "the retry keeps the session")
	prompts := host.promptMessageIDs()
	require.Len(t, prompts, 2)
	assert.Equal(t, prompts[0], prompts[1], "the retry submits the same prompt")
	session = exec.GetAgentSession()
	assert.Equal(t, 1, session.Generation)
	assert.Equal(t, "aborted", session.Events[len(session.Events)-1].Status)
}

// A session the user supplied may run the user's own turn. When Dagu cannot see
// its prompt's turn start there, it does not interrupt the session.
func TestManagedOpenCodeCancelLeavesUnownedSession(t *testing.T) {
	t.Parallel()

	host := newManagedTestHost(t, func(h *managedTestHost) { h.startAfterPolls = 1000 })
	exec := &harnessExecutor{prompt: "Do the work", workDir: t.TempDir()}
	ctx, cancel := context.WithCancel(runtime.WithEnv(t.Context(), runtime.Env{}))
	defer cancel()
	exec.SetProgressCallback(onManagedRunning(exec, func() {
		host.setStatusDown(true)
		cancel()
	}))

	_, err := exec.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{"session": "session-1"}}, host.config)

	require.ErrorIs(t, err, errManagedHostUnavailable)
	session := exec.GetAgentSession()
	assert.False(t, session.SessionOwned)
	assert.Equal(t, "abort-unverified", session.Events[len(session.Events)-1].Status)
	assert.NotContains(t, host.requestLog(), "POST /session/session-1/abort")
}

// onManagedRunning returns a progress callback that calls fn once the managed
// session starts running.
func onManagedRunning(exec *harnessExecutor, fn func()) func() {
	var once sync.Once
	return func() {
		if session := exec.GetAgentSession(); session != nil && session.State == ir.AgentSessionRunning {
			once.Do(fn)
		}
	}
}

// managedTestHost is an OpenCode v1 host that creates session-1. A session is
// busy from prompt submission, or from setBusy, until an interrupt, and then
// settles after one more status poll; an interrupt of an idle session does
// nothing. With startAfterPolls, an accepted prompt's turn starts only after
// that many status polls. A non-zero interruptStatus rejects interrupts with
// that status. With dropAfterInterrupt, status requests lose their connection
// once any session is interrupted, and with setStatusDown until it is lifted.
type managedTestHost struct {
	config             opencodehost.Config
	interruptStatus    int
	startAfterPolls    int
	dropAfterInterrupt bool
	onPrompt           func(r *http.Request)

	mu           sync.Mutex
	requests     []string
	promptIDs    []string
	statusDown   bool
	pendingPolls int
	busy         map[string]bool
	interrupted  map[string]bool
}

func newManagedTestHost(t *testing.T, configure func(*managedTestHost)) *managedTestHost {
	t.Helper()
	host := &managedTestHost{busy: map[string]bool{}, interrupted: map[string]bool{}}
	if configure != nil {
		configure(host)
	}
	server := httptest.NewServer(http.HandlerFunc(host.serve))
	t.Cleanup(server.Close)
	host.config = opencodehost.Config{URL: server.URL, Username: "opencode", Password: "test", InstanceID: "host-1"}
	return host
}

func (h *managedTestHost) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, r.Method+" "+r.URL.Path)
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/config":
		_, _ = io.WriteString(w, `{"share":"disabled"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/session",
		r.Method == http.MethodGet && r.URL.Path == "/session/session-1":
		_, _ = io.WriteString(w, `{"id":"session-1"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/event":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	case r.Method == http.MethodPost && r.URL.Path == "/session/session-1/prompt_async":
		// Reading the whole body also lets the server notice a dropped client.
		var body struct {
			MessageID string `json:"messageID"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.Copy(io.Discard, r.Body)
		h.mu.Lock()
		h.promptIDs = append(h.promptIDs, body.MessageID)
		if h.pendingPolls = h.startAfterPolls; h.pendingPolls == 0 {
			h.busy["session-1"] = true
		}
		h.mu.Unlock()
		if h.onPrompt != nil {
			h.onPrompt(r)
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
		sessionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/session/"), "/abort")
		h.mu.Lock()
		rejected := h.interruptStatus
		if rejected == 0 && h.busy[sessionID] {
			h.interrupted[sessionID] = true
		}
		h.mu.Unlock()
		if rejected != 0 {
			w.WriteHeader(rejected)
			return
		}
		_, _ = io.WriteString(w, `true`)
	case r.Method == http.MethodGet && r.URL.Path == "/session/session-1/message":
		_, _ = io.WriteString(w, `[]`)
	case r.Method == http.MethodGet && r.URL.Path == "/session/status":
		h.mu.Lock()
		if h.statusDown {
			h.mu.Unlock()
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		if h.pendingPolls > 0 {
			if h.pendingPolls--; h.pendingPolls == 0 {
				h.busy["session-1"] = true
			}
		}
		statuses := make(map[string]map[string]string, len(h.busy))
		for sessionID, busy := range h.busy {
			if !busy {
				continue
			}
			statuses[sessionID] = map[string]string{"type": "busy"}
			if h.interrupted[sessionID] {
				delete(h.busy, sessionID)
			}
		}
		drop := h.dropAfterInterrupt && len(h.interrupted) > 0
		h.mu.Unlock()
		if drop {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		_ = json.NewEncoder(w).Encode(statuses)
	default:
		http.NotFound(w, r)
	}
}

func (h *managedTestHost) setBusy(sessionID string, busy bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.busy[sessionID] = busy
}

func (h *managedTestHost) setStatusDown(down bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statusDown = down
}

func (h *managedTestHost) setInterruptStatus(status int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.interruptStatus = status
}

func (h *managedTestHost) setStartAfterPolls(polls int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.startAfterPolls = polls
}

func (h *managedTestHost) promptMessageIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.promptIDs)
}

func (h *managedTestHost) isBusy(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.busy[sessionID]
}

func (h *managedTestHost) wasInterrupted(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.interrupted[sessionID]
}

func (h *managedTestHost) count(request string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.requests {
		if r == request {
			n++
		}
	}
	return n
}

func (h *managedTestHost) requestLog() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.requests)
}

func TestManagedOpenCodeRefreshUpdatesTimeline(t *testing.T) {
	t.Parallel()

	responses := make(chan string, 3)
	responses <- `[{"info":{"role":"assistant","id":"message-1"},"parts":[{"id":"part-1","type":"text","text":"Working"}]}]`
	responses <- `[{"info":{"role":"assistant","id":"message-1"},"parts":[{"id":"part-1","type":"text","text":"Done"}]}]`
	responses <- `[{"info":{"role":"assistant","id":"message-1"},"parts":[{"id":"part-1","type":"text","text":"Done"}]}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, <-responses)
	}))
	t.Cleanup(server.Close)

	progressUpdates := 0
	exec := &harnessExecutor{
		agentSession:     &ir.AgentSession{},
		progressCallback: func() { progressUpdates++ },
	}
	client := &openCodeClient{
		host: opencodehost.Config{URL: server.URL, Password: "test"},
		http: server.Client(),
	}

	_, err := exec.refreshManagedMessages(t.Context(), client, "session-1")
	require.NoError(t, err)
	_, err = exec.refreshManagedMessages(t.Context(), client, "session-1")
	require.NoError(t, err)
	_, err = exec.refreshManagedMessages(t.Context(), client, "session-1")
	require.NoError(t, err)

	require.Len(t, exec.agentSession.Events, 1)
	assert.Equal(t, "Done", exec.agentSession.Events[0].Content)
	assert.Equal(t, 2, progressUpdates)
}

func TestManagedOpenCodeAttachmentLimit(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "large.txt")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", maxManagedAttachmentRawBytes+1)), 0o600))
	_, err := managedFileParts("", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "10 MiB")
}

func TestManagedFilePartsCaseInsensitiveExtension(t *testing.T) {
	t.Parallel()

	// mime.TypeByExtension only knows lowercase extensions, so an
	// uppercase attachment extension must still resolve a media type.
	path := filepath.Join(t.TempDir(), "IMAGE.PNG")
	require.NoError(t, os.WriteFile(path, []byte("png-data"), 0o600))
	parts, err := managedFileParts("", path)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.Equal(t, "image/png", parts[0]["mime"])
}

func TestOpenCodeWildcardMatch(t *testing.T) {
	t.Parallel()

	assert.True(t, openCodeWildcardMatch("git status *", "git status"))
	assert.True(t, openCodeWildcardMatch("git status *", "git status --short"))
	assert.True(t, openCodeWildcardMatch("src/*.go", "src/main.go"))
	assert.True(t, openCodeWildcardMatch("file?.txt", "file1.txt"))
	assert.False(t, openCodeWildcardMatch("src/*.go", "README.md"))
}

func TestManagedOpenCodeSessionPermissionRepliesOnce(t *testing.T) {
	t.Parallel()

	replies := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reply string `json:"reply"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		replies <- body.Reply
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	exec := &harnessExecutor{agentSession: &ir.AgentSession{Interactions: []ir.AgentInteraction{{
		ID: "permission-1", Kind: ir.AgentInteractionPermission, Status: ir.AgentInteractionAnswered,
		Permission: "bash", Decision: "session", AllowForSessionPatterns: []string{"git status *"},
	}}}}
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Password: "secret"}, http: server.Client()}

	resumed, err := exec.applyManagedInteractionResponses(t.Context(), client, "session-1")

	require.NoError(t, err)
	assert.True(t, resumed)
	assert.Equal(t, "once", <-replies)
	require.Len(t, exec.agentSession.PermissionGrants, 1)
	assert.Equal(t, []string{"git status *"}, exec.agentSession.PermissionGrants[0].Patterns)
}

func TestHarnessExecutorPushBackContextAugmentsPromptWithLogPath(t *testing.T) {
	t.Parallel()

	exec := &harnessExecutor{prompt: "Fix the implementation"}
	exec.SetPushBackContext(map[string]string{
		"FEEDBACK": "tighten the tests",
		"SCOPE":    "unit only",
	}, 2)
	exec.SetPushBackPreviousStdout("/tmp/dagu/review.out")

	prompt := exec.effectivePrompt()

	assert.Contains(t, prompt, "Fix the implementation")
	assert.Contains(t, prompt, "Push-back iteration: 2")
	assert.Contains(t, prompt, "Previous stdout log: /tmp/dagu/review.out")
	assert.Contains(t, prompt, "- FEEDBACK: tighten the tests")
	assert.Contains(t, prompt, "- SCOPE: unit only")
	assert.NotContains(t, prompt, "previous stdout content")
}

func TestConfigToFlags(t *testing.T) {
	t.Run("reserved_keys_skipped", func(t *testing.T) {
		flags := configToFlags(map[string]any{
			"provider": "claude",
			"fallback": []any{
				map[string]any{"provider": "codex"},
			},
			// DAG defaults pass the ACP policy to every step, including CLI ones.
			"acp_permission": "allow",
		}, nil)
		assert.Empty(t, flags)
	})

	t.Run("bool_number_and_array", func(t *testing.T) {
		flags := configToFlags(map[string]any{
			"bare":           true,
			"max-turns":      20,
			"max-budget-usd": 5.5,
			"allow-tool":     []any{"shell(git:*)", "write"},
		}, nil)
		assert.Equal(t, []string{
			"--allow-tool", "shell(git:*)",
			"--allow-tool", "write",
			"--bare",
			"--max-budget-usd", "5.5",
			"--max-turns", "20",
		}, flags)
	})

	t.Run("builtin_flags_normalize_underscores", func(t *testing.T) {
		flags := configToFlags(map[string]any{
			"full_auto":           true,
			"max_turns":           20,
			"skip_git_repo_check": true,
		}, nil)
		assert.Equal(t, []string{
			"--full-auto",
			"--max-turns", "20",
			"--skip-git-repo-check",
		}, flags)
	})

	t.Run("definition_overrides_flag_tokens", func(t *testing.T) {
		flags := configToFlags(map[string]any{
			"provider":   "gemini",
			"model":      "gemini-2.5-pro",
			"allow-tool": []any{"shell(git:*)"},
		}, &ir.HarnessDefinition{
			FlagStyle:   ir.HarnessFlagStyleSingleDash,
			OptionFlags: map[string]string{"allow-tool": "--allowedTool"},
		})
		assert.Equal(t, []string{
			"--allowedTool", "shell(git:*)",
			"-model", "gemini-2.5-pro",
		}, flags)
	})
}

func TestNormalizeConfigMap(t *testing.T) {
	cfg := normalizeConfigMap(map[string]any{
		"provider":    "${PROVIDER}",
		"bare":        "true",
		"max-turns":   "10",
		"temperature": "5.5",
		"model":       "sonnet",
		"fallback": []any{
			map[string]any{
				"provider":  "${FALLBACK_PROVIDER}",
				"full-auto": "true",
			},
		},
	})

	assert.Equal(t, "${PROVIDER}", cfg["provider"])
	assert.Equal(t, true, cfg["bare"])
	assert.EqualValues(t, 10, cfg["max-turns"])
	assert.EqualValues(t, 5.5, cfg["temperature"])
	assert.Equal(t, "sonnet", cfg["model"])

	fallback := mustFallback(t, cfg["fallback"])
	require.Len(t, fallback, 1)
	assert.Equal(t, "${FALLBACK_PROVIDER}", fallback[0]["provider"])
	assert.Equal(t, true, fallback[0]["full-auto"])
}

func TestExtractFallbackConfigs(t *testing.T) {
	primary, fallback, err := extractFallbackConfigs(map[string]any{
		"provider": "claude",
		"model":    "sonnet",
		"fallback": []any{
			map[string]any{"provider": "codex", "full-auto": true},
			map[string]any{"provider": "copilot", "silent": true},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"provider": "claude",
		"model":    "sonnet",
	}, primary)
	require.Len(t, fallback, 2)
	assert.Equal(t, "codex", fallback[0]["provider"])
	assert.Equal(t, "copilot", fallback[1]["provider"])
}

func TestValidateHarnessStep(t *testing.T) {
	t.Run("missing_prompt", func(t *testing.T) {
		err := validateHarnessStep(ir.Step{
			ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "claude"}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "prompt")
	})

	t.Run("missing_config", func(t *testing.T) {
		err := validateHarnessStep(ir.Step{
			Commands: []ir.CommandEntry{{Command: "prompt"}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config is required")
	})

	t.Run("missing_provider", func(t *testing.T) {
		err := validateHarnessStep(ir.Step{
			Commands:       []ir.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config.provider is required")
	})

	t.Run("templated_provider_allowed", func(t *testing.T) {
		err := validateHarnessStep(ir.Step{
			Commands:       []ir.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "${PROVIDER}"}},
		})
		assert.NoError(t, err)
	})

	t.Run("templated_fallback_provider_allowed", func(t *testing.T) {
		err := validateHarnessStep(ir.Step{
			Commands: []ir.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{
				"provider": "claude",
				"fallback": []any{
					map[string]any{"provider": "${FALLBACK_PROVIDER}"},
				},
			}},
		})
		assert.NoError(t, err)
	})

	t.Run("multiple_commands_rejected", func(t *testing.T) {
		err := validateHarnessStep(ir.Step{
			Commands: []ir.CommandEntry{
				{Command: "prompt one"},
				{Command: "prompt two"},
			},
			ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "claude"}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "field 'command': action \"harness\" supports only one command")
		assert.NotContains(t, err.Error(), "executor")
	})

	t.Run("invalid_fallback_shape", func(t *testing.T) {
		err := validateHarnessStep(ir.Step{
			Commands: []ir.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{
				"provider": "claude",
				"fallback": []any{"codex"},
			}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "fallback[0]")
	})

	t.Run("nested_fallback_rejected", func(t *testing.T) {
		err := validateHarnessStep(ir.Step{
			Commands: []ir.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{
				"provider": "claude",
				"fallback": []any{
					map[string]any{
						"provider": "codex",
						"fallback": []any{
							map[string]any{"provider": "copilot"},
						},
					},
				},
			}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config.fallback is not supported")
	})
}

func TestResolveProvider(t *testing.T) {
	t.Run("builtin", func(t *testing.T) {
		cfg, err := resolveProvider(map[string]any{"provider": "claude"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "claude", cfg.binaryName())

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"-p", "hello"}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("custom_definition_shadows_builtin", func(t *testing.T) {
		cfg, err := resolveProvider(map[string]any{"provider": "gemini"}, ir.HarnessDefinitions{
			"gemini": {
				Binary:     "custom-gemini",
				PrefixArgs: []string{"run"},
				PromptMode: ir.HarnessPromptModeFlag,
				PromptFlag: "--prompt",
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "custom-gemini", cfg.binaryName())

		cfg.flags = map[string]any{"provider": "gemini", "model": "gemini-2.5-pro"}
		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"run", "--prompt", "hello", "--model", "gemini-2.5-pro"}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("deleted_definition_reveals_builtin", func(t *testing.T) {
		cfg, err := resolveProvider(map[string]any{"provider": "gemini"}, ir.HarnessDefinitions{
			"gemini": nil,
		})
		require.NoError(t, err)
		assert.Equal(t, "gemini", cfg.binaryName())
	})

	t.Run("unknown_provider_names_are_deduplicated", func(t *testing.T) {
		_, err := resolveProvider(map[string]any{"provider": "missing"}, ir.HarnessDefinitions{
			"gemini": {
				Binary:     "custom-gemini",
				PromptMode: ir.HarnessPromptModeArg,
			},
		})
		require.Error(t, err)
		assert.Equal(t, 1, strings.Count(err.Error(), "gemini"))
	})

	t.Run("templated_provider_runtime_error", func(t *testing.T) {
		_, err := resolveProvider(map[string]any{"provider": "${PROVIDER}"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unresolved provider template")
	})
}

func TestBuildProviderConfigs(t *testing.T) {
	t.Run("primary_and_fallback", func(t *testing.T) {
		if goruntime.GOOS == "windows" {
			t.Skip("Skipping shell-based test on Windows")
		}

		primary := writeHarnessTestBinary(t, "primary", "#!/bin/sh\nexit 0\n")
		fallback := writeHarnessTestBinary(t, "fallback", "#!/bin/sh\nexit 0\n")
		defs := ir.HarnessDefinitions{
			"primary": {
				Binary:     primary,
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
			"fallback": {
				Binary:     fallback,
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
		}

		configs, err := buildProviderConfigs(map[string]any{
			"provider": "primary",
			"fallback": []any{
				map[string]any{"provider": "fallback"},
			},
		}, defs)
		require.NoError(t, err)
		require.Len(t, configs, 2)
		assert.Equal(t, primary, configs[0].binaryName())
		assert.Equal(t, fallback, configs[1].binaryName())
	})

	// DAG defaults give every step the ACP permission policy; a CLI provider
	// must not receive it as an unknown flag.
	t.Run("acp_permission_not_a_cli_flag", func(t *testing.T) {
		configs, err := buildProviderConfigs(ir.NormalizeBuiltinHarnessFlagKeys(map[string]any{
			"provider":       "codex",
			"acp_permission": "allow",
		}), nil)
		require.NoError(t, err)
		args, _, err := configs[0].buildInvocation("hello", "")
		require.NoError(t, err)
		assert.NotContains(t, strings.Join(args, " "), "acp")
	})

	t.Run("reject_nested_fallback", func(t *testing.T) {
		_, err := buildProviderConfigs(map[string]any{
			"provider": "claude",
			"fallback": []any{
				map[string]any{
					"provider": "codex",
					"fallback": []any{
						map[string]any{"provider": "copilot"},
					},
				},
			},
		}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config.fallback is not supported")
	})

	t.Run("builtin_provider_defaults_are_applied", func(t *testing.T) {
		configs, err := buildProviderConfigs(map[string]any{
			"provider": "codex",
		}, nil)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, map[string]any{
			"provider":            "codex",
			"skip-git-repo-check": true,
		}, configs[0].flags)
	})

	t.Run("custom_definition_skips_builtin_defaults", func(t *testing.T) {
		configs, err := buildProviderConfigs(map[string]any{
			"provider": "codex",
		}, ir.HarnessDefinitions{
			"codex": {
				Binary:     "custom-codex",
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
		})
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, "custom-codex", configs[0].binaryName())
		assert.Equal(t, map[string]any{"provider": "codex"}, configs[0].flags)
	})

	t.Run("builtin_provider_defaults_can_be_overridden", func(t *testing.T) {
		configs, err := buildProviderConfigs(map[string]any{
			"provider":            "codex",
			"skip_git_repo_check": false,
		}, nil)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, map[string]any{
			"provider":            "codex",
			"skip-git-repo-check": false,
		}, configs[0].flags)
	})

	t.Run("builtin_provider_aliases_are_deduped", func(t *testing.T) {
		configs, err := buildProviderConfigs(map[string]any{
			"provider":            "codex",
			"skip-git-repo-check": false,
		}, nil)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, map[string]any{
			"provider":            "codex",
			"skip-git-repo-check": false,
		}, configs[0].flags)
	})

}

func TestProviderConfigBuildInvocation(t *testing.T) {
	t.Run("arg_mode_before_flags", func(t *testing.T) {
		cfg := providerConfig{
			name: "gemini",
			definition: &ir.HarnessDefinition{
				Binary:         "gemini",
				PrefixArgs:     []string{"run"},
				PromptMode:     ir.HarnessPromptModeArg,
				PromptPosition: ir.HarnessPromptPositionBeforeFlags,
				FlagStyle:      ir.HarnessFlagStyleGNULong,
			},
			flags: map[string]any{
				"provider": "gemini",
				"model":    "gemini-2.5-pro",
			},
		}

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"run", "hello", "--model", "gemini-2.5-pro"}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("arg_mode_after_flags", func(t *testing.T) {
		cfg := providerConfig{
			name: "aider",
			definition: &ir.HarnessDefinition{
				Binary:         "aider",
				PrefixArgs:     []string{"exec"},
				PromptMode:     ir.HarnessPromptModeArg,
				PromptPosition: ir.HarnessPromptPositionAfterFlags,
				FlagStyle:      ir.HarnessFlagStyleSingleDash,
			},
			flags: map[string]any{
				"provider": "aider",
				"model":    "sonnet",
			},
		}

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"exec", "-model", "sonnet", "hello"}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("flag_mode", func(t *testing.T) {
		cfg := providerConfig{
			name: "gemini",
			definition: &ir.HarnessDefinition{
				Binary:         "gemini",
				PrefixArgs:     []string{"run"},
				PromptMode:     ir.HarnessPromptModeFlag,
				PromptFlag:     "--prompt",
				PromptPosition: ir.HarnessPromptPositionBeforeFlags,
				FlagStyle:      ir.HarnessFlagStyleGNULong,
				OptionFlags:    map[string]string{"allow-tool": "--allowedTool"},
			},
			flags: map[string]any{
				"provider":   "gemini",
				"model":      "gemini-2.5-pro",
				"allow-tool": []any{"shell(git:*)"},
			},
		}

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"run",
			"--prompt", "hello",
			"--allowedTool", "shell(git:*)",
			"--model", "gemini-2.5-pro",
		}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("stdin_mode", func(t *testing.T) {
		cfg := providerConfig{
			name: "llm",
			definition: &ir.HarnessDefinition{
				Binary:     "llm",
				PrefixArgs: []string{"run"},
				PromptMode: ir.HarnessPromptModeStdin,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
			flags: map[string]any{
				"provider": "llm",
				"model":    "o3",
			},
		}

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"run", "--model", "o3"}, args)
		assert.Equal(t, "hello\n\ncontext", mustReadAll(t, stdin))
	})
}

func TestHarnessExecutorRun_FallbackBuffersStdout(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	primary := writeHarnessTestBinary(t, "primary", `#!/bin/sh
echo "primary stdout"
echo "primary stderr" >&2
exit 1
`)
	fallback := writeHarnessTestBinary(t, "fallback", `#!/bin/sh
echo "fallback stdout"
echo "fallback stderr" >&2
exit 0
`)

	exec := &harnessExecutor{
		stdout: &strings.Builder{},
		stderr: &strings.Builder{},
		configs: []providerConfig{
			{
				name: "primary",
				definition: &ir.HarnessDefinition{
					Binary:     primary,
					PromptMode: ir.HarnessPromptModeArg,
					FlagStyle:  ir.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "primary"},
			},
			{
				name: "fallback",
				definition: &ir.HarnessDefinition{
					Binary:     fallback,
					PromptMode: ir.HarnessPromptModeArg,
					FlagStyle:  ir.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "fallback"},
			},
		},
		prompt: "hello",
	}

	stdout := exec.stdout.(*strings.Builder)
	stderr := exec.stderr.(*strings.Builder)
	err := exec.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "fallback stdout\n", stdout.String())
	assert.Contains(t, stderr.String(), "primary stderr")
	assert.Contains(t, stderr.String(), "fallback stderr")
	assert.Contains(t, stderr.String(), "trying fallback")
	assert.Equal(t, 0, exec.ExitCode())
}

// A coding agent run through its command line reports no model usage, so
// the step names it rather than showing that it used nothing.
func TestHarnessExecutorRun_MarksModelUsageNotReported(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	agent := writeHarnessTestBinary(t, "agent", `#!/bin/sh
echo "done"
`)
	exec := &harnessExecutor{
		stdout: &strings.Builder{},
		stderr: &strings.Builder{},
		configs: []providerConfig{{
			name: "claude",
			definition: &ir.HarnessDefinition{
				Binary:     agent,
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
		}},
		prompt: "hello",
	}
	meter := modelusage.NewMeter(nil)

	require.NoError(t, exec.Run(modelusage.WithMeter(context.Background(), meter)))
	assert.Equal(t, &ir.StepModelUsage{NotReported: "claude"}, meter.Snapshot())
}

func TestHarnessExecutorRun_IncludesFailedStdoutInError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	primary := writeHarnessTestBinary(t, "primary", `#!/bin/sh
echo "provider auth failed"
exit 1
`)

	var stdout strings.Builder
	var stderr strings.Builder
	exec := &harnessExecutor{
		stdout: &stdout,
		stderr: &stderr,
		configs: []providerConfig{
			{
				name: "primary",
				definition: &ir.HarnessDefinition{
					Binary:     primary,
					PromptMode: ir.HarnessPromptModeArg,
					FlagStyle:  ir.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "primary"},
			},
		},
		prompt: "hello",
	}

	err := exec.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recent stdout:")
	assert.Contains(t, err.Error(), "provider auth failed")
	assert.Contains(t, stderr.String(), "recent stdout (tail):")
	assert.Contains(t, stderr.String(), "provider auth failed")
	assert.Empty(t, stdout.String())
	assert.Equal(t, 1, exec.ExitCode())
}

func TestHarnessExecutorRun_ContextCancellationSkipsFallback(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	marker := filepath.Join(t.TempDir(), "fallback-ran")
	primary := writeHarnessTestBinary(t, "primary", `#!/bin/sh
echo "ready" >&2
exec sleep 60
`)
	fallback := writeHarnessTestBinary(t, "fallback", "#!/bin/sh\ntouch \""+marker+"\"\nexit 0\n")

	for _, stage := range []string{"before_start", "running"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			wantErr := context.Canceled
			if stage == "before_start" {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				wantErr = context.DeadlineExceeded
			}
			defer cancel()
			var stdout strings.Builder
			var stderr strings.Builder
			exec := &harnessExecutor{
				stdout: &stdout,
				stderr: &cancelOnWrite{Writer: &stderr, cancel: cancel},
				configs: []providerConfig{
					{
						name: "primary",
						definition: &ir.HarnessDefinition{
							Binary:     primary,
							PromptMode: ir.HarnessPromptModeArg,
							FlagStyle:  ir.HarnessFlagStyleGNULong,
						},
						flags: map[string]any{"provider": "primary"},
					},
					{
						name: "fallback",
						definition: &ir.HarnessDefinition{
							Binary:     fallback,
							PromptMode: ir.HarnessPromptModeArg,
							FlagStyle:  ir.HarnessFlagStyleGNULong,
						},
						flags: map[string]any{"provider": "fallback"},
					},
				},
				prompt: "hello",
			}

			err := exec.Run(ctx)
			require.ErrorIs(t, err, wantErr)
			assert.NoFileExists(t, marker)
			assert.NotContains(t, stderr.String(), "trying fallback")
			assert.Equal(t, 124, exec.ExitCode())
		})
	}
}

// A graceful stop does not cancel the step context, so the fallback decision
// relies on the stop request itself.
func TestHarnessExecutorRun_StopSkipsFallback(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	marker := filepath.Join(t.TempDir(), "fallback-ran")
	primary := writeHarnessTestBinary(t, "primary", `#!/bin/sh
echo "ready" >&2
exec sleep 60
`)
	fallback := writeHarnessTestBinary(t, "fallback", "#!/bin/sh\ntouch \""+marker+"\"\nexit 0\n")
	var stderr strings.Builder
	exec := &harnessExecutor{
		stdout: &strings.Builder{},
		configs: []providerConfig{
			{
				name: "primary",
				definition: &ir.HarnessDefinition{
					Binary:     primary,
					PromptMode: ir.HarnessPromptModeArg,
					FlagStyle:  ir.HarnessFlagStyleGNULong,
				},
			},
			{
				name: "fallback",
				definition: &ir.HarnessDefinition{
					Binary:     fallback,
					PromptMode: ir.HarnessPromptModeArg,
					FlagStyle:  ir.HarnessFlagStyleGNULong,
				},
			},
		},
		prompt: "hello",
	}
	var stopOnce sync.Once
	exec.stderr = &callOnWrite{Writer: &stderr, call: func() {
		stopOnce.Do(func() { _ = exec.Stop(cmdutil.TerminationFromSignal(syscall.SIGTERM)) })
	}}

	require.Error(t, exec.Run(context.Background()))
	assert.NoFileExists(t, marker)
	assert.NotContains(t, stderr.String(), "trying fallback")
}

// A stop that arrives before an attempt starts its backend keeps it from
// starting, as one between a failed attempt and its fallback does.
func TestHarnessExecutorRun_StopBeforeStart(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	marker := filepath.Join(t.TempDir(), "agent-ran")
	exec := &harnessExecutor{
		stdout: &strings.Builder{},
		stderr: &strings.Builder{},
		configs: []providerConfig{{
			name: "agent",
			definition: &ir.HarnessDefinition{
				Binary:     writeHarnessTestBinary(t, "agent", "#!/bin/sh\ntouch \""+marker+"\"\n"),
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
		}},
		prompt: "hello",
	}
	require.NoError(t, exec.Stop(cmdutil.TerminationFromSignal(syscall.SIGTERM)))

	require.ErrorIs(t, exec.Run(context.Background()), context.Canceled)
	assert.NoFileExists(t, marker)
}

// callOnWrite runs call after each write, once the subprocess has started.
type callOnWrite struct {
	io.Writer
	call func()
}

func (w *callOnWrite) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.call()
	return n, err
}

// cancelOnWrite cancels only after the subprocess signals that it has started.
type cancelOnWrite struct {
	io.Writer
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.cancel()
	return n, err
}

// A cancellation that races a real startup failure keeps the failure and does
// not report the timeout exit code.
func TestHarnessExecutorRun_CancelKeepsStartupError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr strings.Builder
	exec := &harnessExecutor{
		stdout: &stdout,
		stderr: &stderr,
		configs: []providerConfig{
			{
				name: "primary",
				definition: &ir.HarnessDefinition{
					Binary:     filepath.Join(t.TempDir(), "missing-binary"),
					PromptMode: ir.HarnessPromptModeArg,
					FlagStyle:  ir.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "primary"},
			},
		},
		prompt: "hello",
	}

	err := exec.Run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotEqual(t, context.Canceled.Error(), err.Error())
	assert.NotEqual(t, 124, exec.ExitCode())
}

func TestHarnessExecutorRun_CreatesWorkingDir(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	workDir := filepath.Join(t.TempDir(), "nested", "workdir")
	bin := writeHarnessTestBinary(t, "pwd", "#!/bin/sh\npwd\n")

	var stdout strings.Builder
	var stderr strings.Builder
	exec := &harnessExecutor{
		stdout: &stdout,
		stderr: &stderr,
		configs: []providerConfig{
			{
				name: "pwd",
				definition: &ir.HarnessDefinition{
					Binary:     bin,
					PromptMode: ir.HarnessPromptModeArg,
					FlagStyle:  ir.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "pwd"},
			},
		},
		prompt:  "hello",
		workDir: workDir,
	}

	err := exec.Run(context.Background())
	require.NoError(t, err)
	assert.DirExists(t, workDir)
	assert.Contains(t, stdout.String(), workDir)
}

func TestHarnessExecutorRun_UsesPATHFromRuntimeEnv(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	binDir := t.TempDir()
	binName := "path-provider"
	binPath := filepath.Join(binDir, binName)
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\necho \"resolved from path\"\n"), 0o755))

	dag := &ir.DAG{
		Name:       "harness-path",
		WorkingDir: t.TempDir(),
		Harnesses: ir.HarnessDefinitions{
			"custom": {
				Binary:     binName,
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
		},
	}
	step := ir.Step{
		Name:     "step1",
		Commands: []ir.CommandEntry{{Command: "hello"}},
		ExecutorConfig: ir.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "custom"},
		},
	}

	ctx := newHarnessTestContext(t, dag, step, "PATH="+binDir)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	var stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "resolved from path\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestBuildHarnessContainerRunConfig_PodmanImageMode(t *testing.T) {
	ct := ir.Container{
		Image:      "localhost/reviewer:latest",
		WorkingDir: "/work",
		Network:    "host",
		Volumes:    []string{"/host/src:/src:ro"},
		Env:        []string{"HARNESS_TOKEN=secret"},
	}
	inherited := []string{"PATH=/bin", "HARNESS_TOKEN=old"}
	// DAGU_CONTAINER_RUNTIME=podman selects the podman runtime for the engine.
	envs := map[string]string{dockerexec.ContainerRuntimeEnv: "podman"}

	cfg, runCmd, err := buildHarnessContainerRunConfig(
		"/work", ct, nil, "missing-agent", []string{"hello from harness"}, inherited, envs,
	)
	require.NoError(t, err)

	// podman runtime selects podman's Docker-compatible socket.
	assert.Equal(t, dockerexec.PodmanDaemonHostDefault, cfg.DaemonHost)
	assert.Equal(t, "localhost/reviewer:latest", cfg.Image)
	assert.True(t, cfg.AutoRemove, "ephemeral by default (keep_container false)")
	assert.True(t, cfg.ShouldStart)

	require.NotNil(t, cfg.Container)
	// The agent binary is the entrypoint (so an image ENTRYPOINT is not doubled),
	// and the args are the command.
	assert.Equal(t, []string{"missing-agent"}, cfg.Container.Entrypoint)
	assert.Equal(t, []string{"hello from harness"}, runCmd)

	// Env is full KEY=value on the container config (secrets never in argv);
	// explicit container.env overrides inherited by key.
	assert.Contains(t, cfg.Container.Env, "PATH=/bin")
	assert.Contains(t, cfg.Container.Env, "HARNESS_TOKEN=secret")
	assert.NotContains(t, cfg.Container.Env, "HARNESS_TOKEN=old")

	// network: host maps to the SDK host network mode.
	require.NotNil(t, cfg.Host)
	assert.Equal(t, "host", string(cfg.Host.NetworkMode))
}

func TestBuildHarnessContainerRunConfig_DockerRuntimeUsesFromEnv(t *testing.T) {
	// docker is the default: both an unset env and an explicit "docker" leave
	// DaemonHost empty, preserving upstream client.FromEnv behavior.
	for _, envs := range []map[string]string{
		nil,
		{dockerexec.ContainerRuntimeEnv: "docker"},
	} {
		ct := ir.Container{Image: "localhost/reviewer:latest"}
		cfg, _, err := buildHarnessContainerRunConfig(
			"/work", ct, nil, "claude", []string{"-p", "hi"}, nil, envs,
		)
		require.NoError(t, err)
		assert.Equal(t, "", cfg.DaemonHost, "env %v should not override the daemon host", envs)
	}
}

func TestBuildHarnessContainerRunConfig_PodmanHostOverride(t *testing.T) {
	ct := ir.Container{Image: "localhost/reviewer:latest"}
	envs := map[string]string{
		dockerexec.ContainerRuntimeEnv: "podman",
		dockerexec.PodmanDaemonHostEnv: "unix:///custom/podman.sock",
	}
	cfg, _, err := buildHarnessContainerRunConfig(
		"/work", ct, nil, "claude", []string{"-p", "hi"}, nil, envs,
	)
	require.NoError(t, err)
	assert.Equal(t, "unix:///custom/podman.sock", cfg.DaemonHost)
}

func TestBuildHarnessContainerRunConfig_InvalidRuntimeRejected(t *testing.T) {
	ct := ir.Container{Image: "localhost/reviewer:latest"}
	envs := map[string]string{dockerexec.ContainerRuntimeEnv: "containerd"}
	_, _, err := buildHarnessContainerRunConfig(
		"/work", ct, nil, "claude", []string{"-p", "hi"}, nil, envs,
	)
	require.Error(t, err)
}

func TestBuildHarnessContainerRunConfig_ExecModeUsesFullCommand(t *testing.T) {
	ct := ir.Container{Exec: "existing-container"}
	envs := map[string]string{dockerexec.ContainerRuntimeEnv: "podman"}
	cfg, runCmd, err := buildHarnessContainerRunConfig(
		"/work", ct, nil, "missing-agent", []string{"hello"}, []string{"FOO=bar"}, envs,
	)
	require.NoError(t, err)

	assert.Equal(t, "existing-container", cfg.ContainerName)
	assert.Equal(t, dockerexec.PodmanDaemonHostDefault, cfg.DaemonHost)
	// exec into an existing container: full [binary, args...], no image entrypoint.
	assert.Equal(t, []string{"missing-agent", "hello"}, runCmd)
	require.NotNil(t, cfg.ExecOptions)
	assert.Contains(t, cfg.ExecOptions.Env, "FOO=bar")
}

func TestBuildHarnessContainerRunConfig_EmptyBinaryRejected(t *testing.T) {
	ct := ir.Container{Image: "localhost/reviewer:latest"}
	_, _, err := buildHarnessContainerRunConfig("/work", ct, nil, "", nil, nil, nil)
	require.Error(t, err)
}

// TestBuildHarnessContainerRunConfig_ImageModeNamedContainerRejected guards the
// image-mode command-shape invariant: in image mode the agent binary is supplied
// via the container Entrypoint, which the daemon applies only on container CREATE.
// A container.name can resolve to an already-running container, where Client.Run
// execs the args INTO it and docker exec ignores the entrypoint, dropping the
// agent binary. So image mode must reject container.name; exec mode is the
// supported way to run inside an existing container.
func TestBuildHarnessContainerRunConfig_ImageModeNamedContainerRejected(t *testing.T) {
	ct := ir.Container{Image: "localhost/reviewer:latest", Name: "already-running"}
	envs := map[string]string{dockerexec.ContainerRuntimeEnv: "podman"}
	_, _, err := buildHarnessContainerRunConfig(
		"/work", ct, nil, "claude", []string{"-p", "hi"}, nil, envs,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container.name is not supported")

	// Sanity: the same image WITHOUT a name still builds fine (the proven path).
	ctNoName := ir.Container{Image: "localhost/reviewer:latest"}
	_, _, err = buildHarnessContainerRunConfig(
		"/work", ctNoName, nil, "claude", []string{"-p", "hi"}, nil, envs,
	)
	require.NoError(t, err)

	// Exec mode legitimately targets an existing container and must NOT be rejected
	// by the image-mode guard (it returns the full [binary, args...] command).
	ctExec := ir.Container{Exec: "already-running"}
	cfg, runCmd, err := buildHarnessContainerRunConfig(
		"/work", ctExec, nil, "claude", []string{"-p", "hi"}, nil, envs,
	)
	require.NoError(t, err)
	assert.Equal(t, "already-running", cfg.ContainerName)
	assert.Equal(t, []string{"claude", "-p", "hi"}, runCmd)
}

// TestServiceRuntimeEnv_ReadsProcessEnvOnly locks the contract that runtime/socket
// selection is a SERVICE-LEVEL decision: dockerexec.ServiceRuntimeEnv must source
// the two selection keys from the engine process environment (os.LookupEnv) only,
// so a DAG- or step-level env: entry cannot override it. This harness-side test
// proves the property end-to-end through buildHarnessContainerRunConfig (the
// resolver's own unit tests live in the docker package).
func TestServiceRuntimeEnv_ReadsProcessEnvOnly(t *testing.T) {
	t.Run("ignores_dag_and_step_scope", func(t *testing.T) {
		// Process env selects docker (default). A DAG/step env: entry that sets the
		// selector to podman must NOT leak in, because ServiceRuntimeEnv never
		// consults the runtime scope. Assert that buildHarnessContainerRunConfig,
		// fed by ServiceRuntimeEnv(), keeps the docker default daemon host even
		// though the inheritedEnv below carries a podman selector.
		osUnsetForTest(t, dockerexec.ContainerRuntimeEnv) // process: unset -> docker default
		osUnsetForTest(t, dockerexec.PodmanDaemonHostEnv)

		got := dockerexec.ServiceRuntimeEnv()
		_, leaked := got[dockerexec.ContainerRuntimeEnv]
		assert.False(t, leaked, "selector must not come from anywhere but process env")

		ct := ir.Container{Image: "localhost/reviewer:latest"}
		// inheritedEnv simulates a DAG/step env: trying to redirect the runtime;
		// the resolver must ignore it because it reads ServiceRuntimeEnv() only.
		cfg, _, err := buildHarnessContainerRunConfig(
			"/work", ct, nil, "claude", []string{"-p", "hi"},
			[]string{dockerexec.ContainerRuntimeEnv + "=podman"},
			dockerexec.ServiceRuntimeEnv(),
		)
		require.NoError(t, err)
		assert.Equal(t, "", cfg.DaemonHost,
			"with process env unset, daemon host stays docker default even if a DAG/step set podman")
	})
}

// osUnsetForTest unsets an environment variable for the duration of the test,
// restoring any prior value on cleanup. t.Setenv cannot unset, so we manage it here.
func osUnsetForTest(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// TestRunContainerOnce_StdinScriptRejected covers the daemon-free rejection path:
// containerized harness does not support stdin input, because Client.Run has no
// stdin. The script + container combination is rejected earlier at validation
// (see TestValidateHarnessStep_ScriptWithContainerRejected); this guards the
// remaining stdin source — a custom provider with prompt_mode: stdin — which is
// only knowable after provider resolution, so it must be rejected before any SDK
// client is initialized.
func TestRunContainerOnce_StdinScriptRejected(t *testing.T) {
	exec := &harnessExecutor{
		step: ir.Step{
			Name:      "review",
			Container: &ir.Container{Image: "localhost/reviewer:latest"},
		},
		prompt: "do the thing",
		script: "extra stdin context",
	}
	cfg := providerConfig{
		name: "stdinly",
		definition: &ir.HarnessDefinition{
			Binary:     "stdinly",
			PromptMode: ir.HarnessPromptModeStdin,
			FlagStyle:  ir.HarnessFlagStyleGNULong,
		},
		flags: map[string]any{"provider": "stdinly"},
	}
	_, err := exec.runContainerOnce(newHarnessTestContext(t, nil, exec.step), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support stdin")
	assert.Equal(t, 1, exec.ExitCode())
}

func TestWaitForCanceledContainerRun(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("preserves run cleanup error", func(t *testing.T) {
		cleanupErr := errors.New("cleanup failed")
		runDone := make(chan containerRunResult, 1)
		runDone <- containerRunResult{err: errors.Join(context.Canceled, cleanupErr)}

		err := waitForCanceledContainerRun(ctx, runDone)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, cleanupErr)
	})

	t.Run("falls back to cancellation", func(t *testing.T) {
		runDone := make(chan containerRunResult, 1)
		runDone <- containerRunResult{}

		require.ErrorIs(t, waitForCanceledContainerRun(ctx, runDone), context.Canceled)
	})
}

// TestValidateHarnessStep_ScriptWithContainerRejected proves the script + container
// combination fails fast at DAG-load validation rather than at run time. The
// containerized harness provider runs via Client.Run which has no stdin, so a
// script piped to stdin on the host path cannot be delivered.
func TestValidateHarnessStep_ScriptWithContainerRejected(t *testing.T) {
	step := ir.Step{
		Name:           "review",
		Commands:       []ir.CommandEntry{{Command: "do the thing"}},
		Script:         "extra stdin context",
		Container:      &ir.Container{Image: "localhost/reviewer:latest"},
		ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "claude"}},
	}
	err := validateHarnessStep(step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "script")
	assert.Contains(t, err.Error(), "container")

	// Same step WITHOUT a container is valid (script on the host path is fine).
	stepNoContainer := step
	stepNoContainer.Container = nil
	require.NoError(t, validateHarnessStep(stepNoContainer))

	// Same step WITHOUT a script is valid (containerized harness, no stdin needed).
	stepNoScript := step
	stepNoScript.Script = ""
	require.NoError(t, validateHarnessStep(stepNoScript))
}

// TestBuildHarnessContainerRunConfig_AcceptsResourceLimits proves the harness
// container config can carry DAG resource limits: ApplyResourceLimitsToConfig
// (which runContainerOnce calls before InitializeClient, mirroring the docker
// executor and DAG-level container paths) maps CPU/memory onto the container
// HostConfig. Without that call a containerized harness.run step would run
// unbounded by the DAG's configured limits.
func TestBuildHarnessContainerRunConfig_AcceptsResourceLimits(t *testing.T) {
	ct := ir.Container{Image: "localhost/reviewer:latest"}
	envs := map[string]string{dockerexec.ContainerRuntimeEnv: "podman"}
	cfg, _, err := buildHarnessContainerRunConfig(
		"/work", ct, nil, "claude", []string{"-p", "hi"}, nil, envs,
	)
	require.NoError(t, err)

	limits := &ir.ResourceLimits{CPUMillis: 500, MemoryBytes: 1024 * 1024 * 1024}
	applied := dockerexec.ApplyResourceLimitsToConfig(cfg, limits)
	require.True(t, applied, "image-mode harness config must accept resource limits")
	require.NotNil(t, cfg.Host)
	assert.Equal(t, int64(500)*1_000_000, cfg.Host.Resources.NanoCPUs)
	assert.Equal(t, int64(1024*1024*1024), cfg.Host.Resources.Memory)
}

func TestMergedContainerEnv(t *testing.T) {
	got := mergedContainerEnv(
		[]string{"PATH=/bin", "TOKEN=old", "KEEP=1"},
		[]string{"TOKEN=new", "EXTRA=2", "=ignored", "bad"},
	)
	assert.Contains(t, got, "PATH=/bin")
	assert.Contains(t, got, "KEEP=1")
	assert.Contains(t, got, "TOKEN=new") // explicit overrides inherited
	assert.NotContains(t, got, "TOKEN=old")
	assert.Contains(t, got, "EXTRA=2")
	// Malformed entries without a key are ignored.
	assert.NotContains(t, got, "=ignored")
	assert.NotContains(t, got, "bad")
}

func TestHarnessExecutorRun_ResolvesRelativeBinaryFromWorkingDir(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	workDir := t.TempDir()
	binPath := filepath.Join(workDir, "bin", "agent")
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), 0o755))
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\necho \"resolved from workdir\"\n"), 0o755))

	dag := &ir.DAG{
		Name:       "harness-workdir",
		WorkingDir: workDir,
		Harnesses: ir.HarnessDefinitions{
			"custom": {
				Binary:     "./bin/agent",
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
		},
	}
	step := ir.Step{
		Name:     "step1",
		Commands: []ir.CommandEntry{{Command: "hello"}},
		ExecutorConfig: ir.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "custom"},
		},
	}

	ctx := newHarnessTestContext(t, dag, step)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	var stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "resolved from workdir\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestHarnessExecutorRun_FallbackBinaryOptionalUntilNeeded(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	primary := writeHarnessTestBinary(t, "primary", "#!/bin/sh\necho \"primary ok\"\nexit 0\n")

	dag := &ir.DAG{
		Name:       "harness-fallback",
		WorkingDir: t.TempDir(),
		Harnesses: ir.HarnessDefinitions{
			"primary": {
				Binary:     primary,
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
			"fallback": {
				Binary:     "definitely-missing-harness-binary",
				PromptMode: ir.HarnessPromptModeArg,
				FlagStyle:  ir.HarnessFlagStyleGNULong,
			},
		},
	}
	step := ir.Step{
		Name:     "step1",
		Commands: []ir.CommandEntry{{Command: "hello"}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "harness",
			Config: map[string]any{
				"provider": "primary",
				"fallback": []any{
					map[string]any{"provider": "fallback"},
				},
			},
		},
	}

	ctx := newHarnessTestContext(t, dag, step)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	var stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "primary ok\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestNewHarnessRejectsMultipleCommands(t *testing.T) {
	step := ir.Step{
		Name: "step1",
		Commands: []ir.CommandEntry{
			{Command: "hello"},
			{Command: "goodbye"},
		},
		ExecutorConfig: ir.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "claude"},
		},
	}

	ctx := newHarnessTestContext(t, nil, step)
	_, err := newHarness(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field 'command': action \"harness\" supports only one command")
	assert.NotContains(t, err.Error(), "executor")
}

func TestExtractPrompt(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.Equal(t, "", extractPrompt(ir.Step{}))
	})

	t.Run("cmd_with_args", func(t *testing.T) {
		step := ir.Step{
			Commands: []ir.CommandEntry{{CmdWithArgs: "Write tests for auth"}},
		}
		assert.Equal(t, "Write tests for auth", extractPrompt(step))
	})

	t.Run("command_only", func(t *testing.T) {
		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "Refactor"}},
		}
		assert.Equal(t, "Refactor", extractPrompt(step))
	})

	t.Run("command_with_args", func(t *testing.T) {
		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "analyze", Args: []string{"--deep", "src/"}}},
		}
		assert.Equal(t, "analyze --deep src/", extractPrompt(step))
	})
}

func TestGetProvider(t *testing.T) {
	for _, name := range ir.BuiltinCLIHarnessProviderNames() {
		t.Run(name, func(t *testing.T) {
			p, err := getProvider(name)
			require.NoError(t, err)
			assert.Equal(t, name, p.name)
		})
	}
}

func TestBuiltinCLIProvidersStayInSyncWithCoreList(t *testing.T) {
	registered := make([]string, 0, len(providers))
	for name := range providers {
		registered = append(registered, name)
	}
	sort.Strings(registered)

	assert.Equal(t, ir.BuiltinCLIHarnessProviderNames(), registered)
}

func TestRegisterProviderPanicsOnDuplicate(t *testing.T) {
	dupName := "duplicate-test-provider"
	delete(providers, dupName)
	t.Cleanup(func() {
		delete(providers, dupName)
	})

	provider := &providerDescriptor{name: dupName}
	registerProvider(provider)
	require.PanicsWithValue(
		t,
		`harness: duplicate provider registration "duplicate-test-provider"`,
		func() {
			registerProvider(provider)
		},
	)
}

func TestExitCodeFromError(t *testing.T) {
	assert.Equal(t, 0, exitCodeFromError(nil))
	assert.Equal(t, 1, exitCodeFromError(assert.AnError))
}

func writeHarnessTestBinary(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}

func mustReadAll(t *testing.T, reader io.Reader) string {
	t.Helper()

	if reader == nil {
		return ""
	}
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(data)
}

func mustFallback(t *testing.T, value any) []map[string]any {
	t.Helper()

	switch v := value.(type) {
	case []map[string]any:
		return v
	case []any:
		ret := make([]map[string]any, len(v))
		for i := range v {
			item, ok := v[i].(map[string]any)
			require.True(t, ok, "fallback[%d] should be a map[string]any", i)
			ret[i] = item
		}
		return ret
	default:
		t.Fatalf("unexpected fallback type %T", value)
		return nil
	}
}

func newHarnessTestContext(t *testing.T, dag *ir.DAG, step ir.Step, envs ...string) context.Context {
	t.Helper()

	if dag == nil {
		dag = &ir.DAG{Name: "harness-test", WorkingDir: t.TempDir()}
	}
	if dag.Name == "" {
		dag.Name = "harness-test"
	}
	if dag.WorkingDir == "" {
		dag.WorkingDir = t.TempDir()
	}

	ctx := runtime.NewContext(context.Background(), dag, "run-1", "", runtime.WithEnvVars(envs...))
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
}
