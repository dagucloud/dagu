// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package webhooksig

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A request is named after its event the way its sender labels it.
func TestEvent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header http.Header
		body   string
		event  string
	}{
		{"GitHub with an action", http.Header{"X-Github-Event": {"issues"}}, `{"action":"opened"}`, "issues.opened"},
		{"GitHub without one", http.Header{"X-Github-Event": {"push"}}, `{"ref":"refs/heads/main"}`, "push"},
		{"Shopify", http.Header{"X-Shopify-Topic": {"orders/create"}}, `{}`, "orders/create"},
		{"Stripe", http.Header{}, `{"type":"payment_intent.succeeded"}`, "payment_intent.succeeded"},
		{"an event field", http.Header{}, `{"event":"form.submitted"}`, "form.submitted"},
		{"Slack", http.Header{}, `{"type":"event_callback","event":{"type":"app_mention","text":"hi"}}`, "app_mention"},
		{"form data", http.Header{}, `payload=%7B%7D`, ""},
		{"a long name", http.Header{"X-Github-Event": {strings.Repeat("e", 200)}}, `{}`, strings.Repeat("e", MaxEventLength)},
	} {
		assert.Equal(t, tc.event, Event(tc.header, []byte(tc.body)), tc.name)
	}
}

func TestIsGitHubPing(t *testing.T) {
	assert.True(t, IsGitHubPing(http.Header{"X-Github-Event": {"ping"}}))
	assert.False(t, IsGitHubPing(http.Header{"X-Github-Event": {"push"}}))
	assert.False(t, IsGitHubPing(http.Header{}))
}

func TestEvents(t *testing.T) {
	events, err := Events([]string{" issues ", "", "push", "issues"})
	require.NoError(t, err)
	assert.Equal(t, []string{"issues", "push"}, events)

	events, err = Events([]string{" "})
	require.NoError(t, err)
	assert.Nil(t, events, "no event named takes every event")

	_, err = Events([]string{strings.Repeat("e", MaxEventLength+1)})
	assert.Error(t, err)
	many := make([]string, MaxEvents+1)
	for i := range many {
		many[i] = strings.Repeat("e", i+1)
	}
	_, err = Events(many)
	assert.Error(t, err)
}

// A name takes its own event and the events under it.
func TestTakes(t *testing.T) {
	assert.True(t, Takes(nil, "anything"))
	assert.True(t, Takes([]string{"issues"}, "issues"))
	assert.True(t, Takes([]string{"issues"}, "issues.opened"))
	assert.False(t, Takes([]string{"issues"}, "issues_comment"))
	assert.False(t, Takes([]string{"issues"}, ""))
}
