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
	"os/exec"
	"path/filepath"
	"strings"
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
	var askQuestion, stallTurn, editFile atomic.Bool
	root := t.TempDir()
	// OpenCode only captures filesystem snapshots inside a Git worktree.
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	initialized, err := exec.CommandContext(t.Context(), "git", "-C", root, "init").CombinedOutput()
	require.NoError(t, err, "%s", initialized)
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
		if len(request.Tools) > 0 && editFile.Swap(false) {
			const arguments = `{"path":"snapshot-check.txt","content":"DAGU_SNAPSHOT_OK\n"}`
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"write\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_write\",\"type\":\"function\",\"function\":{\"name\":\"write\",\"arguments\":%q}}]},\"finish_reason\":null}]}\n\n", arguments)
			_, _ = io.WriteString(w, `data: {"id":"write","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
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
	missingExecutor := &harnessExecutor{prompt: "unknown command", workDir: root}
	missingOutput, err := missingExecutor.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{"command": "dagu_missing_command"}}, hostConfig)
	require.ErrorContains(t, err, "CommandNotFoundError")
	require.Nil(t, missingOutput)
	require.Equal(t, ir.AgentSessionFailed, missingExecutor.agentSession.State)
	available, err := opencodehost.SessionAvailable(ctx, hostConfig, root, missingExecutor.agentSession.SessionID)
	require.NoError(t, err)
	require.True(t, available, "an unknown command must not make the session unavailable")

	editFile.Store(true)
	editExecutor := &harnessExecutor{prompt: "Write the snapshot marker file", workDir: root}
	editOutput, err := editExecutor.runManagedOpenCode(ctx, providerConfig{flags: map[string]any{"model": "dagutest/test"}}, hostConfig)
	require.NoError(t, err)
	require.NoError(t, cleanupStdoutSpool(editOutput))
	written, err := os.ReadFile(filepath.Join(root, "snapshot-check.txt"))
	require.NoError(t, err, "%+v", editExecutor.agentSession.Events)
	require.Equal(t, "DAGU_SNAPSHOT_OK\n", string(written))
	var changed []string
	for _, event := range editExecutor.agentSession.Events {
		if event.Type == "patch" {
			changed = append(changed, event.Files...)
		}
	}
	require.Contains(t, changed, "snapshot-check.txt")
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
	available, err = opencodehost.SessionAvailable(ctx, hostConfig, root, executor.agentSession.SessionID)
	require.NoError(t, err)
	require.True(t, available)
	require.NoError(t, opencodehost.DeleteSession(ctx, hostConfig, root, executor.agentSession.SessionID))
	available, err = opencodehost.SessionAvailable(ctx, hostConfig, root, executor.agentSession.SessionID)
	require.NoError(t, err)
	require.False(t, available)
	client := &openCodeClient{host: hostConfig, directory: root, http: newOpenCodeHTTPClient(3 * time.Second)}
	_, err = client.getSession(ctx, executor.agentSession.SessionID)
	require.ErrorIs(t, err, errManagedSessionUnavailable)
	_, err = client.messages(ctx, executor.agentSession.SessionID)
	require.ErrorIs(t, err, errManagedSessionUnavailable)
}

// A root with child sessions stays settling for one more poll, because a
// child's report can start another turn of the root.
func TestV2ChildrenSettle(t *testing.T) {
	var polls, listed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session/active":
			polls.Add(1)
			_, _ = w.Write([]byte(`{"data":{"ses_other":{"type":"running"}}}`))
		case "/api/session/ses_other":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_other","location":{"directory":"/workspace"}}}`))
		case "/api/session":
			listed.Add(1)
			require.Equal(t, "ses_root", r.URL.Query().Get("parentID"))
			_, _ = w.Write([]byte(`{"data":[{"id":"ses_child","parentID":"ses_root"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.3.2"}, directory: "/workspace", http: server.Client()}

	require.NoError(t, client.awaitIdle(t.Context(), "ses_root"))

	require.EqualValues(t, 2, polls.Load())
	require.EqualValues(t, 2, listed.Load())
}

// A failed child listing is not an empty listing: the root may have children.
func TestV2ListingFailureAssumesChildren(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "/api/session":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.3.2"}, directory: "/workspace", http: server.Client()}

	family, err := client.family(t.Context(), "ses_root")

	require.NoError(t, err)
	require.True(t, family.idle())
	require.True(t, family.hasChildren)
}

// An active session whose ancestry cannot be read has unknown membership: it
// blocks idle and is retried on every poll.
func TestV2UnreadableSessionUnknown(t *testing.T) {
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_other":{"type":"running"}}}`))
		case "/api/session/ses_other":
			gets.Add(1)
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.3.2"}, directory: "/workspace", http: server.Client()}

	for range 2 {
		family, err := client.family(t.Context(), "ses_root")
		require.NoError(t, err)
		require.Empty(t, family.descendants)
		require.Equal(t, []string{"ses_other"}, family.unknown)
		require.False(t, family.idle())
	}

	require.EqualValues(t, 2, gets.Load())
}

// A session confirmed unrelated (root-less or missing) is ignored.
func TestV2ConfirmedUnrelated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_a":{"type":"running"},"ses_b":{"type":"running"}}}`))
		case "/api/session/ses_a":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_a"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"_tag":"SessionNotFoundError"}`))
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.3.2"}, directory: "/workspace", http: server.Client()}

	family, err := client.family(t.Context(), "ses_root")

	require.NoError(t, err)
	require.True(t, family.idle())
}

// Exhausting the depth bound or meeting a cycle leaves membership unknown.
func TestV2DepthAndCycleUnknown(t *testing.T) {
	for name, parents := range map[string]func(int) string{
		"depth": func(i int) string { return fmt.Sprintf("ses_%d", i+1) },
		"cycle": func(i int) string { return fmt.Sprintf("ses_%d", (i+1)%2) },
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/session/active" {
					_, _ = w.Write([]byte(`{"data":{"ses_0":{"type":"running"}}}`))
					return
				}
				var i int
				if _, err := fmt.Sscanf(r.URL.Path, "/api/session/ses_%d", &i); err != nil {
					http.NotFound(w, r)
					return
				}
				_, _ = fmt.Fprintf(w, `{"data":{"id":"ses_%d","parentID":%q}}`, i, parents(i))
			}))
			defer server.Close()
			client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.3.2"}, directory: "/workspace", http: server.Client()}

			family, err := client.family(t.Context(), "ses_root")

			require.NoError(t, err)
			require.Equal(t, []string{"ses_0"}, family.unknown)
			require.False(t, family.idle())
		})
	}
}

// Settlement waits while an active session is unreadable and finishes once
// it can be read.
func TestV2SettleUnreadable(t *testing.T) {
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_other":{"type":"running"}}}`))
		case "/api/session/ses_other":
			if gets.Add(1) < 3 {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"id":"ses_other"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.3.2"}, directory: "/workspace", http: server.Client()}

	require.NoError(t, client.awaitIdle(t.Context(), "ses_root"))
	require.GreaterOrEqual(t, gets.Load(), int32(3))

	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session/active" {
			_, _ = w.Write([]byte(`{"data":{"ses_other":{"type":"running"}}}`))
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer always.Close()
	blocked := &openCodeClient{host: opencodehost.Config{URL: always.URL, Version: "2.3.2"}, directory: "/workspace", http: always.Client()}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	err := blocked.awaitIdle(ctx, "ses_root")

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "ses_other")
}

// Parent links are cached, so repeated polls do not re-read a session.
func TestV2ParentCache(t *testing.T) {
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_child":{"type":"running"}}}`))
		case "/api/session/ses_child":
			gets.Add(1)
			_, _ = w.Write([]byte(`{"data":{"id":"ses_child","parentID":"ses_root","location":{"directory":"/workspace"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.3.2"}, directory: "/workspace", http: server.Client()}

	for range 3 {
		family, err := client.family(t.Context(), "ses_root")
		require.NoError(t, err)
		require.Equal(t, []string{"ses_child"}, family.descendants)
	}

	require.EqualValues(t, 1, gets.Load())
}

// v1 has no background children, so no other session is inspected.
func TestV1FamilyNoWalk(t *testing.T) {
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/session/status"):
			_, _ = w.Write([]byte(`{"ses_other":{"type":"busy"}}`))
		case strings.HasPrefix(r.URL.Path, "/session/"):
			gets.Add(1)
			_, _ = w.Write([]byte(`{"id":"ses_other"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "1.14.0"}, directory: "/workspace", http: server.Client()}

	family, err := client.family(t.Context(), "ses_root")

	require.NoError(t, err)
	require.True(t, family.idle())
	require.Zero(t, gets.Load())
}

// subagentProvider scripts an OpenAI-compatible provider: a parent that
// spawns a background child, a child that runs a slow shell command, and a
// later parent turn woken by the child's report.
func subagentProvider(t *testing.T) *httptest.Server {
	chunk := func(w http.ResponseWriter, delta map[string]any, finish string) {
		data, err := json.Marshal(map[string]any{"id": "x", "object": "chat.completion.chunk", "created": 1, "model": "test", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nilIfEmpty(finish)}}})
		require.NoError(t, err)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}
	call := func(w http.ResponseWriter, name string, args map[string]any) {
		arguments, err := json.Marshal(args)
		require.NoError(t, err)
		chunk(w, map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": name, "arguments": string(arguments)}}}}, "")
		chunk(w, map[string]any{}, "tool_calls")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
	say := func(w http.ResponseWriter, text string) {
		chunk(w, map[string]any{"role": "assistant", "content": text}, "")
		chunk(w, map[string]any{}, "stop")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		var request struct {
			Tools    []json.RawMessage `json:"tools"`
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		last := string(request.Messages[len(request.Messages)-1])
		all := fmt.Sprint(request.Messages)
		switch {
		case len(request.Tools) == 0:
			say(w, "title")
		case strings.Contains(last, `"role":"tool"`) && strings.Contains(last, "ses_"):
			say(w, "PARENT_LAUNCHED")
		case strings.Contains(last, `"role":"tool"`):
			say(w, "CHILD_DONE")
		case !strings.Contains(all, "SPAWN"):
			call(w, "shell", map[string]any{"command": "touch bg.started; sleep 3; echo done > bg.done", "description": "slow work"})
		case strings.Contains(last, "<subagent"):
			say(w, "PARENT_FOLLOWUP")
		default:
			call(w, "subagent", map[string]any{"agent": "general", "description": "slow work", "prompt": "CHILD_WORK"})
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// OpenCode 2.3 subagents run in the background and report back to the parent
// later. A managed step settles only after they do, and a cancelled step
// leaves none of them running.
func TestRealV2Subagent(t *testing.T) {
	binary := os.Getenv("DAGU_TEST_OPENCODE_V2_EXECUTABLE")
	if binary == "" {
		t.Skip("DAGU_TEST_OPENCODE_V2_EXECUTABLE is not set")
	}
	root := t.TempDir()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	initialized, err := exec.CommandContext(t.Context(), "git", "-C", root, "init").CombinedOutput()
	require.NoError(t, err, "%s", initialized)
	provider := subagentProvider(t)
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "OPENCODE_CONFIG_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	t.Setenv("OPENCODE_CONFIG_PROJECT_DISABLE", "1")
	t.Setenv("OPENCODE_DISABLE_CLAUDE_CODE", "1")
	t.Setenv("OPENCODE_DISABLE_MODELS_FETCH", "1")
	settings := map[string]any{"model": "dagutest/test", "providers": map[string]any{"dagutest": map[string]any{"package": "@ai-sdk/openai-compatible", "settings": map[string]any{"baseURL": provider.URL + "/v1", "apiKey": "local-test"}, "models": map[string]any{"test": map[string]any{"name": "test"}}}}}
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
	if hostConfig.Version == "2.1.4" {
		t.Skip("background subagents need OpenCode 2.3 or newer")
	}
	cfg := providerConfig{flags: map[string]any{"model": "dagutest/test"}}
	ctx, cancel := context.WithTimeout(runtime.WithEnv(t.Context(), runtime.Env{}), 90*time.Second)
	defer cancel()
	done, started := filepath.Join(root, "bg.done"), filepath.Join(root, "bg.started")

	executor := &harnessExecutor{prompt: "SPAWN", workDir: root}
	stdout, err := executor.runManagedOpenCode(ctx, cfg, hostConfig)
	require.NoError(t, err)
	output, err := io.ReadAll(stdout)
	require.NoError(t, err)
	require.NoError(t, cleanupStdoutSpool(stdout))
	require.FileExists(t, done, "the step succeeded before its subagent finished")
	require.Equal(t, ir.AgentSessionSucceeded, executor.GetAgentSession().State)
	require.Contains(t, string(output), "PARENT_FOLLOWUP", "the parent's later turn must be collected")

	require.NoError(t, os.Remove(done))
	require.NoError(t, os.Remove(started))
	cancelCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for cancelCtx.Err() == nil {
			if _, err := os.Stat(started); err == nil {
				cancelRun()
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	defer func() { cancelRun(); <-watched }()
	cancelled := &harnessExecutor{prompt: "SPAWN", workDir: root}
	_, err = cancelled.runManagedOpenCode(cancelCtx, cfg, hostConfig)
	require.ErrorIs(t, err, context.Canceled)
	session := cancelled.GetAgentSession()
	require.Equal(t, ir.AgentSessionAborted, session.State)
	require.Equal(t, "aborted", session.Events[len(session.Events)-1].Status, session.LastError)
	client := &openCodeClient{host: hostConfig, directory: root, http: newOpenCodeHTTPClient(3 * time.Second)}
	active, err := client.activeSessions(ctx)
	require.NoError(t, err)
	require.Empty(t, active, "no session may run after the step is cancelled")
	require.Never(t, func() bool { _, err := os.Stat(done); return err == nil }, 4*time.Second, 200*time.Millisecond, "the cancelled step's subagent kept running")
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if r.URL.Path == "/api/session/ses_test" {
			_, _ = io.WriteString(w, `{"_tag":"SessionNotFoundError"}`)
			return
		}
		_, _ = io.WriteString(w, `{"_tag":"FormNotFoundError"}`)
	}))
	defer server.Close()
	c := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, sessionID: "ses_test", http: server.Client()}
	for _, path := range []string{"/session/ses_test/permission/req/reply", "/session/ses_test/form/req", "/session/ses_test/form/req/reply"} {
		err := c.json(t.Context(), http.MethodPost, path, nil, nil)
		require.Error(t, err)
		require.NotErrorIs(t, err, errManagedSessionUnavailable)
	}
	require.ErrorIs(t, c.json(t.Context(), http.MethodGet, "/session/ses_test", nil, nil), errManagedSessionUnavailable)
}

func TestV2NotFound(t *testing.T) {
	for _, tc := range []struct {
		name        string
		path        string
		body        string
		unavailable bool
	}{
		{"command", "/session/ses_test/command", `{"_tag":"CommandNotFoundError"}`, false},
		{"model", "/session/ses_test/model", `{"_tag":"ModelNotFoundError"}`, false},
		{"location", "/session/ses_test", `{"_tag":"LocationNotFoundError"}`, false},
		{"session", "/session/ses_test/command", `{"_tag":"SessionNotFoundError"}`, true},
		{"interaction_session", "/session/ses_test/form/req/reply", `{"_tag":"SessionNotFoundError"}`, true},
		{"permission", "/session/ses_test/permission/req/reply", `{"_tag":"PermissionNotFoundError"}`, false},
		{"form", "/session/ses_test/form/req", `{"_tag":"FormNotFoundError"}`, false},
		{"unknown", "/session/ses_test", `{"_tag":"FutureNotFoundError"}`, false},
		{"untagged", "/session/ses_test", `{}`, false},
		{"malformed", "/session/ses_test", `<html>Not found</html>`, false},
		{"trailing", "/session/ses_test", `{"_tag":"SessionNotFoundError"}garbage`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, http: server.Client()}
			err := client.postNoContent(t.Context(), tc.path, nil)
			require.Error(t, err)
			_, historyErr := client.messages(t.Context(), "ses_test")
			require.Error(t, historyErr)
			if tc.unavailable {
				require.ErrorIs(t, err, errManagedSessionUnavailable)
				require.ErrorIs(t, historyErr, errManagedSessionUnavailable)
				return
			}
			require.NotErrorIs(t, err, errManagedSessionUnavailable)
			require.NotErrorIs(t, historyErr, errManagedSessionUnavailable)
			require.ErrorContains(t, err, "404")
			if tc.name == "malformed" || tc.name == "trailing" {
				require.ErrorContains(t, err, "invalid error response")
				require.ErrorContains(t, historyErr, "invalid error response")
				return
			}
			var failure struct {
				Tag string `json:"_tag"`
			}
			require.NoError(t, json.Unmarshal([]byte(tc.body), &failure))
			require.ErrorContains(t, err, failure.Tag)
			require.ErrorContains(t, historyErr, failure.Tag)
		})
	}
}

func TestV2ResumeErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tag   string
		state ir.AgentSessionState
	}{
		{"location", "LocationNotFoundError", ir.AgentSessionFailed},
		{"session", "SessionNotFoundError", ir.AgentSessionUnavailable},
		{"host", "", ir.AgentSessionUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations.Add(1)
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprintf(w, `{"_tag":%q}`, tc.tag)
			}))
			defer server.Close()
			if tc.name == "host" {
				server.Close()
			}
			executor := &harnessExecutor{agentSession: &ir.AgentSession{SessionID: "ses_test", SessionOwned: false}}
			ctx := runtime.WithEnv(t.Context(), runtime.Env{})
			output, err := executor.runManagedOpenCode(ctx, providerConfig{}, opencodehost.Config{URL: server.URL, Version: "2.1.4"})
			if tc.state == ir.AgentSessionFailed {
				require.ErrorContains(t, err, tc.tag)
				require.Nil(t, output)
			} else {
				require.NoError(t, err)
				require.NoError(t, cleanupStdoutSpool(output))
				require.Equal(t, ir.NodeWaiting, executor.determinedStatus)
			}
			require.Equal(t, tc.state, executor.agentSession.State)
			require.Equal(t, "ses_test", executor.agentSession.SessionID)
			require.False(t, executor.agentSession.SessionOwned)
			require.Zero(t, mutations.Load(), "setup must not interrupt or replace a user-supplied session")
		})
	}
}

// A typed 404 on history fails the poll; a transient error waits for the next one.
func TestV2HistoryError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"location", http.StatusNotFound, `{"_tag":"LocationNotFoundError"}`, "LocationNotFoundError"},
		{"transient", http.StatusInternalServerError, `{}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/session/ses_test/message" {
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				_, _ = io.WriteString(w, `{"data":[]}`)
			}))
			defer server.Close()
			client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, http: server.Client()}
			executor := &harnessExecutor{agentSession: &ir.AgentSession{SessionID: "ses_test"}}
			waiting, err := executor.reconcileManagedSession(t.Context(), client, "ses_test")
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
			require.False(t, waiting)
		})
	}
}

func TestV2SnapshotRefresh(t *testing.T) {
	var files atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"msg_answer","type":"assistant","content":[{"type":"text","text":"edited"}],"snapshot":{"files":%s},"tokens":{"input":10,"output":5}}]}`, files.Load())
	}))
	defer server.Close()
	client := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, http: server.Client()}
	updates := 0
	executor := &harnessExecutor{agentSession: &ir.AgentSession{SessionID: "ses_test"}, progressCallback: func() { updates++ }}
	ctx := runtime.WithEnv(t.Context(), runtime.Env{})
	files.Store(`[]`)
	_, err := executor.refreshManagedMessages(ctx, client, "ses_test")
	require.NoError(t, err)
	require.Len(t, executor.agentSession.Events, 2)
	usage := executor.agentSession.Events[1]
	files.Store(`["main.go"]`)
	_, err = executor.refreshManagedMessages(ctx, client, "ses_test")
	require.NoError(t, err)
	require.Len(t, executor.agentSession.Events, 3)
	require.Equal(t, usage, executor.agentSession.Events[1], "usage event identity must survive snapshot appearance")
	patch := executor.agentSession.Events[2]
	require.Equal(t, "patch", patch.Type)
	files.Store(`["main.go","image.png"]`)
	_, err = executor.refreshManagedMessages(ctx, client, "ses_test")
	require.NoError(t, err)
	require.Len(t, executor.agentSession.Events, 3)
	updated := executor.agentSession.Events[2]
	require.Equal(t, []string{"main.go", "image.png"}, updated.Files)
	require.Equal(t, patch.ID, updated.ID)
	require.Equal(t, patch.Sequence, updated.Sequence)
	require.Equal(t, patch.Timestamp, updated.Timestamp)
	_, err = executor.refreshManagedMessages(ctx, client, "ses_test")
	require.NoError(t, err)
	require.Len(t, executor.agentSession.Events, 3)
	require.Equal(t, 3, updates)
	require.EqualValues(t, 15, executor.agentSession.Usage.TotalTokens)
}

func TestV2Snapshot(t *testing.T) {
	var messages []openCodeV2Message
	require.NoError(t, json.Unmarshal([]byte(`[
 {"id":"msg_prompt","type":"user","text":"edit"},
 {"id":"msg_answer","type":"assistant","finish":"stop","time":{"completed":1},"content":[{"type":"text","text":"edited"}],"snapshot":{"files":["image.png","main.go"]}}
 ]`), &messages))
	chat, events, _, _ := normalizeOpenCodeMessages(normalizeV2Messages(messages))
	require.Len(t, chat, 2)
	require.Len(t, events, 3)
	patch := events[2]
	require.Equal(t, "patch", patch.Type)
	require.Equal(t, "assistant", patch.Role)
	require.Equal(t, []string{"image.png", "main.go"}, patch.Files)
	require.Equal(t, "msg_answer:patch", patch.ID)
	messages[0].Snapshot.Files = []string{"ignored.go"}

	// New content must not change the patch identity during history refreshes.
	messages[1].Content = nil
	_, repeated, _, _ := normalizeOpenCodeMessages(normalizeV2Messages(messages))
	require.Len(t, repeated, 2)
	require.Equal(t, patch.ID, repeated[1].ID)
	require.Equal(t, patch.Files, repeated[1].Files)
	require.NoError(t, json.Unmarshal([]byte(`[{"id":"msg_prompt","type":"user","text":"edit"},{"id":"msg_answer","type":"assistant","content":[],"snapshot":{"files":[]}}]`), &messages))
	_, empty, _, _ := normalizeOpenCodeMessages(normalizeV2Messages(messages))
	require.Len(t, empty, 1)
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
		case "/api/session":
			_, _ = w.Write([]byte(`{"data":[]}`))
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

// A stop during prompt setup keeps the prompt from being submitted.
func TestV2SetupCancellation(t *testing.T) {
	var prompted atomic.Bool
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/ses_test/agent":
			cancel()
			w.WriteHeader(http.StatusNoContent)
		case "/api/session/ses_test/prompt":
			prompted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := &openCodeClient{host: opencodehost.Config{URL: server.URL, Version: "2.1.4"}, http: server.Client()}
	e := &harnessExecutor{}

	_, submitted, err := e.submitManagedPrompt(ctx, c, providerConfig{flags: map[string]any{"agent": "build"}}, "ses_test", "msg_test", nil)

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, submitted)
	require.False(t, prompted.Load())
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
	e := &harnessExecutor{agentSession: &ir.AgentSession{PromptMessageID: "msg_test"}}
	began, err := e.submitManagedPromptV2(ctx, c, providerConfig{flags: map[string]any{"command": "slow"}}, "ses_test", "msg_test", nil)
	require.Error(t, err)
	require.True(t, began)
	// A command carries no message ID, so a retry must not submit it again.
	require.True(t, e.agentSession.PromptSent)
	require.Empty(t, e.agentSession.PromptMessageID)
	require.True(t, submitted.Load())
	require.True(t, interrupted.Load(), "accepted command must be interrupted even after caller cancellation")
}
