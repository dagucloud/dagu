// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMailerConfigFromSMTP(t *testing.T) {
	t.Parallel()

	config, err := mailerConfigFromSMTP(&ir.SMTPConfig{
		Username: "sender@example.com",
		OAuth: &oauthconfig.Config{
			Provider: oauthconfig.ProviderMicrosoft, TenantID: "tenant",
			ClientID: "client", ClientSecret: "secret",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "smtp.office365.com", config.Host)
	assert.Equal(t, "587", config.Port)
	assert.Equal(t, "sender@example.com", config.Username)
	assert.NotNil(t, config.Token)
}

func TestErrorString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "NilError",
			err:      nil,
			expected: "",
		},
		{
			name:     "SimpleError",
			err:      errors.New("test error"),
			expected: "test error",
		},
		{
			name:     "WrappedError",
			err:      errors.New("outer: inner error"),
			expected: "outer: inner error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := errorString(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPanicToError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		panicObj    any
		expectedMsg string
	}{
		{
			name:        "WithError",
			panicObj:    errors.New("panic error"),
			expectedMsg: "panic error",
		},
		{
			name:        "WithString",
			panicObj:    "string panic",
			expectedMsg: "panic: string panic",
		},
		{
			name:        "WithInt",
			panicObj:    42,
			expectedMsg: "panic: 42",
		},
		{
			name:        "WithNil",
			panicObj:    nil,
			expectedMsg: "panic: <nil>",
		},
		{
			name:        "WithStruct",
			panicObj:    struct{ msg string }{msg: "test"},
			expectedMsg: "panic: {test}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := panicToError(tt.panicObj)
			assert.Equal(t, tt.expectedMsg, result.Error())
		})
	}
}

func TestCleanupManagedAgentSessionsDeletesOnlyOwnedSessions(t *testing.T) {
	deleted := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deleted <- r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	t.Setenv("_DAGU_INTERNAL_OPENCODE_URL", server.URL)
	t.Setenv("_DAGU_INTERNAL_OPENCODE_USERNAME", "opencode")
	t.Setenv("_DAGU_INTERNAL_OPENCODE_PASSWORD", "secret")
	t.Setenv("_DAGU_INTERNAL_OPENCODE_INSTANCE_ID", "host-1")

	status := &ir.DAGRunStatus{Nodes: []*ir.Node{
		{AgentSession: &ir.AgentSession{
			Provider: "opencode", SessionID: "owned", SessionOwned: true, CleanupPending: true,
			HostInstanceID: "host-1", Directory: "/workspace", Generation: 1,
		}},
		{AgentSession: &ir.AgentSession{
			Provider: "opencode", SessionID: "external", CleanupPending: true,
			HostInstanceID: "host-1", Directory: "/workspace", Generation: 1,
		}},
	}}

	assert.True(t, cleanupManagedAgentSessions(context.Background(), status))
	assert.Equal(t, "/session/owned", <-deleted)
	assert.Empty(t, status.Nodes[0].AgentSession.SessionID)
	assert.False(t, status.Nodes[0].AgentSession.CleanupPending)
	assert.Equal(t, "external", status.Nodes[1].AgentSession.SessionID)
}
