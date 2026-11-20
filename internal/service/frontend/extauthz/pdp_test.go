// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package extauthz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePDP is an AuthZEN PDP for tests. It allows the (subject, action,
// resource id) triples in allow and records the requests it receives.
type fakePDP struct {
	t     *testing.T
	mu    sync.Mutex
	allow map[string]bool
	calls []string
	auth  []string
	// answer, when set, replaces the computed answer body.
	answer string
	status int
	delay  time.Duration
}

func newFakePDP(t *testing.T, allowed ...string) (*fakePDP, *httptest.Server) {
	t.Helper()
	pdp := &fakePDP{t: t, allow: map[string]bool{}}
	for _, a := range allowed {
		pdp.allow[a] = true
	}
	srv := httptest.NewServer(pdp)
	t.Cleanup(srv.Close)
	return pdp, srv
}

func decisionKey(subject, action, resourceID string) string {
	return subject + "|" + action + "|" + resourceID
}

func (p *fakePDP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.calls = append(p.calls, r.URL.Path)
	p.auth = append(p.auth, r.Header.Get("Authorization"))
	answer, status, delay := p.answer, p.status, p.delay
	p.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if answer != "" {
		_, _ = w.Write([]byte(answer))
		return
	}

	switch r.URL.Path {
	case evaluationPath:
		var req evaluationRequest
		assert.NoError(p.t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(p.t, SubjectTypeUser, req.Subject.Type)
		decision := p.allow[decisionKey(req.Subject.ID, req.Action.Name, req.Resource.ID)]
		_ = json.NewEncoder(w).Encode(map[string]any{"decision": decision})
	case evaluationsPath:
		var req batchRequest
		assert.NoError(p.t, json.NewDecoder(r.Body).Decode(&req))
		var out []map[string]any
		for _, e := range req.Evaluations {
			out = append(out, map[string]any{"decision": p.allow[decisionKey(req.Subject.ID, req.Action.Name, e.Resource.ID)]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"evaluations": out})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (p *fakePDP) set(answer string, status int, delay time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer, p.status, p.delay = answer, status, delay
}

func (p *fakePDP) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

var alice = Subject{Type: SubjectTypeUser, ID: "alice"}

func TestEvaluateAllowAndDeny(t *testing.T) {
	t.Parallel()
	pdp, srv := newFakePDP(t, decisionKey("alice", "runs.start", "daily-report"))
	client := NewPDPClient(PDPConfig{BaseURL: srv.URL + "/", Token: "pdp-token", Timeout: time.Second})

	allowed, err := client.Evaluate(context.Background(), alice, ActionRunsStart, WorkflowResource("daily-report", ""))
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = client.Evaluate(context.Background(), alice, ActionRunsStop, WorkflowResource("daily-report", "r1"))
	require.NoError(t, err)
	require.False(t, allowed)

	pdp.mu.Lock()
	defer pdp.mu.Unlock()
	require.Equal(t, []string{evaluationPath, evaluationPath}, pdp.calls)
	require.Equal(t, []string{"Bearer pdp-token", "Bearer pdp-token"}, pdp.auth)
}

func TestEvaluateFailsOnBadAnswers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		answer string
		status int
		delay  time.Duration
	}{
		{name: "Timeout", delay: time.Second},
		{name: "ServerError", status: http.StatusInternalServerError},
		{name: "Unauthorized", status: http.StatusUnauthorized},
		{name: "NotJSON", answer: "allow"},
		{name: "MissingDecision", answer: `{"context":{}}`},
		{name: "StringDecision", answer: `{"decision":"true"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pdp, srv := newFakePDP(t, decisionKey("alice", "runs.start", "daily-report"))
			pdp.set(tt.answer, tt.status, tt.delay)
			client := NewPDPClient(PDPConfig{BaseURL: srv.URL, Timeout: 50 * time.Millisecond})

			allowed, err := client.Evaluate(context.Background(), alice, ActionRunsStart, WorkflowResource("daily-report", ""))
			require.Error(t, err)
			require.False(t, allowed)
		})
	}

	t.Run("Unreachable", func(t *testing.T) {
		t.Parallel()
		_, srv := newFakePDP(t)
		srv.Close()
		client := NewPDPClient(PDPConfig{BaseURL: srv.URL, Timeout: time.Second})
		_, err := client.Evaluate(context.Background(), alice, ActionRunsStart, WorkflowResource("daily-report", ""))
		require.Error(t, err)
	})
}

func TestEvaluateCache(t *testing.T) {
	t.Parallel()
	pdp, srv := newFakePDP(t, decisionKey("alice", "workflows.read", "a"))
	now := testNow
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	client := NewPDPClient(PDPConfig{BaseURL: srv.URL, Timeout: time.Second, CacheTTL: 5 * time.Second, Now: clock})
	ctx := context.Background()

	for range 3 {
		allowed, err := client.Evaluate(ctx, alice, ActionWorkflowsRead, WorkflowResource("a", ""))
		require.NoError(t, err)
		require.True(t, allowed)
		allowed, err = client.Evaluate(ctx, alice, ActionWorkflowsRead, WorkflowResource("b", ""))
		require.NoError(t, err)
		require.False(t, allowed)
	}
	require.Equal(t, 2, pdp.callCount(), "positive and negative decisions are cached")

	mu.Lock()
	now = now.Add(5 * time.Second)
	mu.Unlock()
	_, err := client.Evaluate(ctx, alice, ActionWorkflowsRead, WorkflowResource("a", ""))
	require.NoError(t, err)
	require.Equal(t, 3, pdp.callCount(), "an expired decision is asked again")
}

func TestEvaluateErrorsAreNotCached(t *testing.T) {
	t.Parallel()
	pdp, srv := newFakePDP(t, decisionKey("alice", "workflows.read", "a"))
	pdp.set("", http.StatusBadGateway, 0)
	client := NewPDPClient(PDPConfig{BaseURL: srv.URL, Timeout: time.Second, CacheTTL: time.Minute})

	_, err := client.Evaluate(context.Background(), alice, ActionWorkflowsRead, WorkflowResource("a", ""))
	require.Error(t, err)

	pdp.set("", 0, 0)
	allowed, err := client.Evaluate(context.Background(), alice, ActionWorkflowsRead, WorkflowResource("a", ""))
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestEvaluateBatch(t *testing.T) {
	t.Parallel()
	pdp, srv := newFakePDP(t,
		decisionKey("alice", "workflows.read", "a"),
		decisionKey("alice", "workflows.read", "c"),
	)
	client := NewPDPClient(PDPConfig{BaseURL: srv.URL, Timeout: time.Second, CacheTTL: time.Minute})
	resources := []Resource{WorkflowResource("a", ""), WorkflowResource("b", ""), WorkflowResource("c", "")}

	decisions, err := client.EvaluateBatch(context.Background(), alice, ActionWorkflowsRead, resources)
	require.NoError(t, err)
	require.Equal(t, []bool{true, false, true}, decisions)
	require.Equal(t, 1, pdp.callCount())

	// Cached decisions are not asked again.
	decisions, err = client.EvaluateBatch(context.Background(), alice, ActionWorkflowsRead, resources)
	require.NoError(t, err)
	require.Equal(t, []bool{true, false, true}, decisions)
	require.Equal(t, 1, pdp.callCount())
}

func TestEvaluateBatchChunks(t *testing.T) {
	t.Parallel()
	pdp, srv := newFakePDP(t)
	client := NewPDPClient(PDPConfig{BaseURL: srv.URL, Timeout: time.Second})
	resources := make([]Resource, maxBatchSize+1)
	for i := range resources {
		resources[i] = WorkflowResource(string(rune('a'+i%26))+time.Duration(i).String(), "")
	}

	decisions, err := client.EvaluateBatch(context.Background(), alice, ActionWorkflowsRead, resources)
	require.NoError(t, err)
	require.Len(t, decisions, len(resources))
	require.Equal(t, 2, pdp.callCount())
}

func TestEvaluateBatchRejectsWrongLength(t *testing.T) {
	t.Parallel()
	pdp, srv := newFakePDP(t)
	pdp.set(`{"evaluations":[{"decision":true}]}`, 0, 0)
	client := NewPDPClient(PDPConfig{BaseURL: srv.URL, Timeout: time.Second})

	_, err := client.EvaluateBatch(context.Background(), alice, ActionWorkflowsRead,
		[]Resource{WorkflowResource("a", ""), WorkflowResource("b", "")})
	require.Error(t, err)
}
