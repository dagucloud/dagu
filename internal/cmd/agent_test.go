// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentCommandShape(t *testing.T) {
	cmd := Agent()

	assert.Equal(t, "agent", cmd.Name())
	assert.NotNil(t, cmd.Flags().Lookup("prompt"))
	assert.Equal(t, "p", cmd.Flags().Lookup("prompt").Shorthand)
	assert.NotNil(t, cmd.Flags().Lookup("model"))
	assert.Nil(t, cmd.Flags().Lookup("server"))

	subcommands := map[string]bool{}
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}
	assert.True(t, subcommands["history"])
	assert.True(t, subcommands["resume"])
}

func TestRunAgentWithoutPromptStartsInteractiveMode(t *testing.T) {
	cmd := Agent()
	cmd.SetIn(strings.NewReader("/exit\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := runAgent(&Context{
		Context: context.Background(),
		Command: cmd,
	}, nil)

	require.NoError(t, err)
	assert.Contains(t, out.String(), "Type /exit")
}

func TestRemoteClientCreateAgentSession(t *testing.T) {
	var gotPath string
	var gotRequest api.AgentChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotRequest))
		_ = json.NewEncoder(w).Encode(api.CreateAgentSessionResponse{
			SessionId: "session-1",
			Status:    "accepted",
		})
	}))
	defer srv.Close()

	remote := &remoteClient{
		baseURL: srv.URL,
		apiKey:  "test-key",
		client:  srv.Client(),
	}
	resp, err := remote.createAgentSession(context.Background(), api.AgentChatRequest{
		Message: "create a DAG",
		Model:   stringPtrOrNil("gpt-4.1"),
	})

	require.NoError(t, err)
	assert.Equal(t, "/agent/sessions", gotPath)
	assert.Equal(t, "create a DAG", gotRequest.Message)
	require.NotNil(t, gotRequest.Model)
	assert.Equal(t, "gpt-4.1", *gotRequest.Model)
	assert.Equal(t, "session-1", resp.SessionId)
	assert.Equal(t, "accepted", resp.Status)
}

func TestRemoteClientSendAgentMessage(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.AgentStatusResponse{Status: "accepted"})
	}))
	defer srv.Close()

	remote := &remoteClient{
		baseURL: srv.URL,
		apiKey:  "test-key",
		client:  srv.Client(),
	}
	err := remote.sendAgentMessage(context.Background(), "session-1", api.AgentChatRequest{
		Message: "continue",
	})

	require.NoError(t, err)
	assert.Equal(t, "/agent/sessions/session-1/chat", gotPath)
}

func TestRemoteClientRespondAgentPrompt(t *testing.T) {
	var gotPath string
	var gotRequest api.AgentUserPromptResponse
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotRequest))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(api.AgentStatusResponse{Status: "accepted"})
	}))
	defer srv.Close()

	remote := &remoteClient{
		baseURL: srv.URL,
		apiKey:  "test-key",
		client:  srv.Client(),
	}
	err := remote.respondAgentPrompt(context.Background(), "session-1", api.AgentUserPromptResponse{
		PromptId: "prompt-1",
		SelectedOptionIds: &[]string{
			"approve",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "/agent/sessions/session-1/respond", gotPath)
	assert.Equal(t, "prompt-1", gotRequest.PromptId)
	require.NotNil(t, gotRequest.SelectedOptionIds)
	assert.Equal(t, []string{"approve"}, *gotRequest.SelectedOptionIds)
}

func TestRunAgentOnceDoesNotPrintSessionID(t *testing.T) {
	remote := newAgentCommandRemoteServer(t, "reply text")
	var out bytes.Buffer
	cmd := Agent()
	cmd.SetOut(&out)

	err := runAgentOnce(&Context{
		Context:     context.Background(),
		Command:     cmd,
		ContextName: "remote",
		Remote:      remote,
	}, "typed text")

	require.NoError(t, err)
	assert.NotContains(t, out.String(), "Session:")
	assert.NotContains(t, out.String(), "typed text")
	assert.Contains(t, out.String(), "reply text")
}

func TestRunAgentInteractiveDoesNotPrintSessionID(t *testing.T) {
	remote := newAgentCommandRemoteServer(t, "reply text")
	var out bytes.Buffer
	cmd := Agent()
	cmd.SetIn(strings.NewReader("typed text\n/exit\n"))
	cmd.SetOut(&out)

	err := runAgentInteractive(&Context{
		Context:     context.Background(),
		Command:     cmd,
		ContextName: "remote",
		Remote:      remote,
	}, "")

	require.NoError(t, err)
	assert.NotContains(t, out.String(), "Session:")
	assert.NotContains(t, out.String(), "typed text")
	assert.Contains(t, out.String(), "reply text")
}

func TestBuildAgentPromptResponseMatchesOptionLabels(t *testing.T) {
	resp, err := buildAgentPromptResponse(&agentPromptRow{
		ID: "prompt-1",
		Options: []agentPromptOptionRow{
			{ID: "approve", Label: "Approve"},
			{ID: "reject", Label: "Reject"},
		},
	}, "approve")

	require.NoError(t, err)
	assert.Equal(t, "prompt-1", resp.PromptID)
	assert.Equal(t, []string{"approve"}, resp.SelectedOptionIDs)
}

func TestCLIAgentLoggerDoesNotWriteToDefaultLogger(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(original)
	})

	newCLIAgentLogger().Info("queued user message", "session_id", "session-1")

	assert.Empty(t, buf.String())
}

func TestFollowAgentSessionNonInteractivePrintsPendingPromptHint(t *testing.T) {
	var out bytes.Buffer

	err := followAgentSessionNonInteractive(context.Background(), &out, "session-1", func(context.Context) (*agentSessionDetail, error) {
		return &agentSessionDetail{
			Working:          true,
			HasPendingPrompt: true,
		}, nil
	})

	require.NoError(t, err)
	assert.Contains(t, out.String(), "Pending input required; run `dagu agent resume session-1` to respond.")
}

func TestRenderAgentMessageOmitsSpeakerLabels(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, renderAgentMessage(&out, agentMessageRow{
		ID:      "assistant-1",
		Type:    "assistant",
		Content: "hello",
	}))

	assert.Equal(t, "\nhello\n", out.String())
	assert.NotContains(t, out.String(), "Agent:")
}

func TestFollowAgentSessionDoesNotEchoUserMessages(t *testing.T) {
	var out bytes.Buffer

	_, err := followAgentSessionWithSeen(context.Background(), &out, nil, func(context.Context) (*agentSessionDetail, error) {
		return &agentSessionDetail{
			Messages: []agentMessageRow{
				{ID: "user-1", Type: "user", Content: "typed text"},
				{ID: "assistant-1", Type: "assistant", Content: "reply text"},
			},
		}, nil
	})

	require.NoError(t, err)
	assert.NotContains(t, out.String(), "You:")
	assert.NotContains(t, out.String(), "typed text")
	assert.NotContains(t, out.String(), "Agent:")
	assert.Contains(t, out.String(), "reply text")
}

func newAgentCommandRemoteServer(t *testing.T, assistantContent string) *remoteClient {
	t.Helper()

	sessionID := "session-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/agent/sessions":
			_ = json.NewEncoder(w).Encode(api.CreateAgentSessionResponse{
				SessionId: sessionID,
				Status:    "accepted",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/agent/sessions/"+sessionID:
			now := time.Now()
			_ = json.NewEncoder(w).Encode(api.AgentSessionDetailResponse{
				Session: api.AgentSession{
					Id:        sessionID,
					CreatedAt: now,
					UpdatedAt: now,
				},
				SessionState: api.AgentSessionState{
					SessionId: sessionID,
					Working:   false,
				},
				Messages: []api.AgentMessage{
					{
						Id:         "user-1",
						SessionId:  sessionID,
						SequenceId: 1,
						Type:       api.AgentMessageType("user"),
						Content:    stringPtrOrNil("typed text"),
						CreatedAt:  now,
					},
					{
						Id:         "assistant-1",
						SessionId:  sessionID,
						SequenceId: 2,
						Type:       api.AgentMessageType("assistant"),
						Content:    stringPtrOrNil(assistantContent),
						CreatedAt:  now,
					},
				},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	return &remoteClient{
		baseURL: srv.URL,
		client:  srv.Client(),
	}
}
