// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestV2Session(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/config":
			require.Equal(t, "/workspace", r.URL.Query().Get("location[directory]"))
			_, _ = w.Write([]byte(`[{"type":"document","info":{"share":"disabled"}}]`))
		case "/api/session":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, map[string]any{"directory": "/workspace"}, body["location"])
			_, _ = w.Write([]byte(`{"data":{"id":"ses_test","location":{"directory":"/workspace"}}}`))
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_test":{"type":"running"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, directory: "/workspace", http: server.Client()}
	require.NoError(t, client.validateManagedConfig(t.Context()))
	session, err := client.createSession(t.Context(), "test", "build")
	require.NoError(t, err)
	require.Equal(t, "ses_test", session.ID)
	require.Equal(t, "/workspace", session.Directory)
	status, err := client.sessionStatus(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "busy", status)
}

func TestRealV2Run(t *testing.T) {
	binary := os.Getenv("DAGU_TEST_OPENCODE_V2_EXECUTABLE")
	if binary == "" {
		t.Skip("DAGU_TEST_OPENCODE_V2_EXECUTABLE is not set")
	}
	var askQuestion, stallTurn atomic.Bool
	var stallOnce sync.Once
	turnStalled := make(chan struct{})
	releaseStall := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		var request struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		questionEnabled := false
		for _, tool := range request.Tools {
			if tool.Function.Name == "question" {
				questionEnabled = true
			}
		}
		if questionEnabled && askQuestion.Swap(false) {
			chunk := map[string]any{"id": "question", "object": "chat.completion.chunk", "created": 1, "model": "test", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_question", "type": "function", "function": map[string]any{"name": "question", "arguments": `{"questions":[{"question":"Choose a branch","header":"Branch","options":[{"label":"Stable","description":"Use stable"}]}]}`}}}}, "finish_reason": nil}}}
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			_, _ = io.WriteString(w, `data: {"id":"question","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		// Only an agent turn offers tools, so title generation never stalls.
		if len(request.Tools) > 0 && stallTurn.Load() {
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, "data: {\"id\":\"stall\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"working\"},\"finish_reason\":null}]}\n\n")
			w.(http.Flusher).Flush()
			stallOnce.Do(func() { close(turnStalled) })
			select {
			case <-r.Context().Done():
			case <-releaseStall:
			}
			return
		}

		_, _ = io.WriteString(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"DAGU_V2_OK\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	defer close(releaseStall)
	root := t.TempDir()
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "OPENCODE_CONFIG_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	t.Setenv("OPENCODE_CONFIG_PROJECT_DISABLE", "1")
	t.Setenv("OPENCODE_DISABLE_CLAUDE_CODE", "1")
	t.Setenv("OPENCODE_DISABLE_MODELS_FETCH", "1")
	settings := map[string]any{"model": "dagutest/test", "providers": map[string]any{"dagutest": map[string]any{"package": "@ai-sdk/openai-compatible", "settings": map[string]any{"baseURL": provider.URL + "/v1", "apiKey": "local-test"}, "models": map[string]any{"test": map[string]any{"name": "test"}}}}}
	settings["permissions"] = []any{map[string]any{"action": "question", "resource": "*", "effect": "ask"}}
	settings["commands"] = map[string]any{"smoke": map[string]any{"template": "Reply to $ARGUMENTS"}}
	content, err := json.Marshal(settings)
	require.NoError(t, err)
	t.Setenv("OPENCODE_CONFIG_CONTENT", string(content))
	host := opencodehost.New(t.Context(), config.OpenCodeConfig{Executable: binary})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, host.Close(ctx))
	}()
	hostConfig, err := host.Ensure()
	require.NoError(t, err)
	require.Equal(t, "2.1.4", hostConfig.Version)
	executor := &harnessExecutor{prompt: "Reply with the test marker", workDir: root}
	ctx, cancel := context.WithTimeout(runtime.WithEnv(t.Context(), runtime.Env{}), 30*time.Second)
	defer cancel()
	stdout, err := executor.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{"model": "dagutest/test"}}, hostConfig)
	require.NoError(t, err)
	defer cleanupStdoutSpool(stdout)
	output, err := io.ReadAll(stdout)
	require.NoError(t, err)
	require.Contains(t, string(output), "DAGU_V2_OK")
	commandExecutor := &harnessExecutor{prompt: "command marker", workDir: root}
	commandOutput, commandErr := commandExecutor.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{"model": "dagutest/test", "command": "smoke"}}, hostConfig)
	require.NoError(t, commandErr)
	defer cleanupStdoutSpool(commandOutput)
	commandText, err := io.ReadAll(commandOutput)
	require.NoError(t, err)
	require.Contains(t, string(commandText), "DAGU_V2_OK")
	askQuestion.Store(true)
	questionExecutor := &harnessExecutor{prompt: "Ask which branch to use", workDir: root}
	waitingOutput, err := questionExecutor.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{"model": "dagutest/test"}}, hostConfig)
	require.NoError(t, err)
	require.NoError(t, cleanupStdoutSpool(waitingOutput))

	require.Equal(t, ir.AgentSessionWaiting, questionExecutor.agentSession.State)
	require.Len(t, questionExecutor.agentSession.Interactions, 1)
	require.Equal(t, ir.AgentInteractionPermission, questionExecutor.agentSession.Interactions[0].Kind)
	questionExecutor.agentSession.Interactions[0].Status = ir.AgentInteractionAnswered
	questionExecutor.agentSession.Interactions[0].Decision = "once"
	questionOutput, err := questionExecutor.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{"model": "dagutest/test"}}, hostConfig)
	require.NoError(t, err)
	require.NoError(t, cleanupStdoutSpool(questionOutput))
	require.Equal(t, ir.AgentSessionWaiting, questionExecutor.agentSession.State)
	require.Len(t, questionExecutor.agentSession.Interactions, 2)
	require.Equal(t, ir.AgentInteractionQuestion, questionExecutor.agentSession.Interactions[1].Kind)
	questionExecutor.agentSession.Interactions[1].Status = ir.AgentInteractionAnswered
	questionExecutor.agentSession.Interactions[1].Answers = [][]string{{"Stable"}}
	resumedOutput, err := questionExecutor.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{"model": "dagutest/test"}}, hostConfig)
	require.NoError(t, err)
	defer cleanupStdoutSpool(resumedOutput)
	resumedText, err := io.ReadAll(resumedOutput)
	require.NoError(t, err)
	require.Contains(t, string(resumedText), "DAGU_V2_OK")

	// A cancelled turn is reported aborted only after OpenCode settles it.
	stallTurn.Store(true)
	interruptCtx, interruptCancel := context.WithCancel(ctx)
	defer interruptCancel()
	go func() {
		select {
		case <-turnStalled:
			interruptCancel()
		case <-interruptCtx.Done():
		}
	}()
	interruptExecutor := &harnessExecutor{prompt: "Start a long answer", workDir: root}
	_, err = interruptExecutor.runManagedOpenCode(interruptCtx, providerConfig{flags: map[string]any{"model": "dagutest/test"}}, hostConfig)
	require.ErrorIs(t, err, context.Canceled)
	interrupted := interruptExecutor.GetAgentSession()
	require.Equal(t, ir.AgentSessionAborted, interrupted.State)
	require.Equal(t, "aborted", interrupted.Events[len(interrupted.Events)-1].Status, interrupted.LastError)
	available, err := opencodehost.SessionAvailable(ctx, hostConfig, root, executor.agentSession.SessionID)
	require.NoError(t, err)
	require.True(t, available)
	require.NoError(t, opencodehost.DeleteSession(ctx, hostConfig, root, executor.agentSession.SessionID))
	available, err = opencodehost.SessionAvailable(ctx, hostConfig, root, executor.agentSession.SessionID)
	require.NoError(t, err)
	require.False(t, available)
}

func TestV2Messages(t *testing.T) {
	var messages []openCodeV2Message
	require.NoError(t, json.Unmarshal([]byte(`[
 {"id":"msg_old","type":"user","text":"old","time":{"created":1}},
 {"id":"msg_prompt","type":"user","text":"new","time":{"created":2}},
 {"id":"msg_answer","type":"assistant","time":{"created":3,"completed":4},"model":{"providerID":"test","id":"model"},"content":[{"type":"text","text":"answer"}],"finish":"stop","tokens":{"input":10,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.01},
 {"id":"msg_idle","type":"idle","outcome":"succeeded","time":{"created":5}}
 ]`), &messages))
	normalized := normalizeV2Messages(messages)
	output, completed, err := managedOpenCodeResult(normalized, "msg_prompt")
	require.NoError(t, err)
	require.True(t, completed)
	require.Equal(t, "answer", output)
	_, _, usage, models := normalizeOpenCodeMessages(normalized)
	require.EqualValues(t, 15, usage.TotalTokens)
	require.Len(t, models, 1)
	messages[len(messages)-1].Outcome = "failed"
	messages[len(messages)-1].Error = &struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}{Type: "APIError", Message: "model failed"}
	_, _, err = managedOpenCodeResult(normalizeV2Messages(messages), "msg_prompt")
	require.EqualError(t, err, "model failed")
}

func TestV2Pagination(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(fmt.Sprint(repeat), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/session/ses_test/message", r.URL.Path)
				requests++
				if requests == 1 {
					require.Equal(t, "asc", r.URL.Query().Get("order"))
					_, _ = io.WriteString(w, `{"data":[{"id":"msg_prompt","type":"user","text":"question"}],"cursor":{"next":"opaque +/?"}}`)
					return
				}
				require.Empty(t, r.URL.Query().Get("order"))
				require.Equal(t, "opaque +/?", r.URL.Query().Get("cursor"))
				if repeat {
					_, _ = io.WriteString(w, `{"data":[],"cursor":{"next":"opaque +/?"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"data":[{"id":"msg_answer","type":"assistant","finish":"stop","time":{"completed":1},"content":[{"type":"text","text":"answer"}]}],"cursor":{}}`)
			}))
			defer server.Close()
			client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, http: server.Client()}
			messages, err := client.messages(t.Context(), "ses_test")
			if repeat {
				require.ErrorContains(t, err, "repeated a message cursor")
				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, requests)
			output, complete, err := managedOpenCodeResult(messages, "msg_prompt")
			require.NoError(t, err)
			require.True(t, complete)
			require.Equal(t, "answer", output)
		})
	}
}

func TestV2Interactions(t *testing.T) {
	const form = `{"id":"form_test","sessionID":"ses_test","title":"Choose","fields":[{"key":"choice","type":"string","title":"Choice","description":"Pick one","options":[{"value":"stable","label":"Stable"}]},{"key":"count","type":"integer"},{"key":"many","type":"multiselect","options":[{"value":"a","label":"Alpha"}]}]}`
	var permission, answer map[string]any
	rejected := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/permission/request":
			_, _ = io.WriteString(w, `{"data":[{"id":"per_test","sessionID":"ses_test","action":"bash","resources":["git status"],"save":["git *"]}]}`)
		case "/api/session/ses_test/permission/per_test/reply":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&permission))
			w.WriteHeader(http.StatusNoContent)
		case "/api/form":
			_, _ = io.WriteString(w, `{"data":[`+form+`]}`)
		case "/api/session/ses_test/form/form_test":
			if r.Method == http.MethodDelete {
				rejected = true
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = io.WriteString(w, `{"data":`+form+`}`)
		case "/api/session/ses_test/form/form_test/reply":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&answer))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, sessionID: "ses_test", http: server.Client()}
	permissions, err := client.permissions(t.Context())
	require.NoError(t, err)
	require.Len(t, permissions, 1)
	require.Equal(t, "bash", permissions[0].Permission)
	require.Equal(t, []string{"git *"}, permissions[0].Always)
	require.NoError(t, client.replyPermission(t.Context(), "per_test", "once"))
	require.Equal(t, map[string]any{"decision": "once"}, permission)
	questions, err := client.questions(t.Context())
	require.NoError(t, err)
	require.Len(t, questions, 1)
	require.Len(t, questions[0].Questions, 3)
	require.True(t, questions[0].Questions[2].Multiple)
	require.NoError(t, client.replyQuestion(t.Context(), "form_test", [][]string{{"Stable"}, {"3"}, {"Alpha"}}))
	require.Equal(t, map[string]any{"answer": map[string]any{"choice": "stable", "count": float64(3), "many": []any{"a"}}}, answer)
	require.NoError(t, client.rejectQuestion(t.Context(), "form_test"))
	require.True(t, rejected)
}

func TestV2PermissionEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/permission/request" {
			_, _ = io.WriteString(w, `{"data":[{"id":"per_test","sessionID":"ses_test","action":"question","resources":["*"]}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, http: server.Client()}
	executor := &harnessExecutor{agentSession: &ir.AgentSession{SessionID: "ses_test"}}
	var event openCodeEvent
	require.NoError(t, json.Unmarshal([]byte(`{"type":"permission.asked","data":{"sessionID":"ses_test"}}`), &event))
	waiting, _, err := executor.handleManagedEvent(runtime.WithEnv(t.Context(), runtime.Env{}), client, "ses_test", event)
	require.NoError(t, err)
	require.True(t, waiting)
	require.Len(t, executor.agentSession.Interactions, 1)
}

func TestV2MissingInteraction(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	c := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, sessionID: "ses_test", http: server.Client()}
	for _, path := range []string{"/session/ses_test/permission/req/reply", "/session/ses_test/form/req", "/session/ses_test/form/req/reply"} {
		err := c.json(t.Context(), http.MethodPost, path, nil, nil)
		require.Error(t, err)
		require.NotErrorIs(t, err, errManagedSessionUnavailable)
	}
	require.ErrorIs(t, c.json(t.Context(), http.MethodGet, "/session/ses_test", nil, nil), errManagedSessionUnavailable)
}

// OpenCode v2 accepts an interrupt before cleanup settles; the session stays
// active until its execution finishes.
func TestV2InterruptAwaitsIdle(t *testing.T) {
	var interrupted atomic.Bool
	var activePolls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session/ses_test/interrupt":
			interrupted.Store(true)
			_, _ = w.Write([]byte(`{"interrupted":true}`))
		case "/api/session/active":
			if activePolls.Add(1) <= 2 {
				_, _ = w.Write([]byte(`{"data":{"ses_test":{"type":"running"}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, http: server.Client()}
	require.NoError(t, c.interruptAndAwaitIdle(t.Context(), "ses_test"))
	require.True(t, interrupted.Load())
	require.Equal(t, int32(3), activePolls.Load())
}

func TestV2CommandCancellation(t *testing.T) {
	var submitted, interrupted atomic.Bool
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/ses_test/command":
			submitted.Store(true)
			cancel()
			w.WriteHeader(http.StatusNoContent)
		case "/api/session/ses_test/interrupt":
			interrupted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case "/api/session/ses_test/inbox", "/api/session/ses_test/message":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, http: server.Client()}
	e := &harnessExecutor{}
	err := e.submitManagedPromptV2(ctx, c, providerConfig{flags: map[string]any{"command": "slow"}}, "ses_test", "msg_test", nil)
	require.Error(t, err)
	require.True(t, submitted.Load())
	require.True(t, interrupted.Load(), "accepted command must be interrupted even after caller cancellation")
}
