// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookRuntimeParams(t *testing.T) {
	t.Parallel()
	got := WebhookRuntimeParams(`{"event":"push"}`, `{"x-github-event":["push"]}`)
	assert.Equal(t, `WEBHOOK_PAYLOAD="{\"event\":\"push\"}" WEBHOOK_HEADERS="{\"x-github-event\":[\"push\"]}"`, got)
}

// Only the named headers are forwarded, never credentials, under lowercase
// names.
func TestWebhookHeaders(t *testing.T) {
	t.Parallel()
	header := http.Header{
		"X-Github-Event": {"push"},
		"Authorization":  {"Bearer secret"},
		"X-Multi":        {"a", "b"},
	}
	got, err := WebhookHeaders([]string{"X-GitHub-Event", "authorization", "x-multi", "x-missing"}, header.Values)
	require.NoError(t, err)
	assert.JSONEq(t, `{"x-github-event":["push"],"x-multi":["a","b"]}`, got)

	none, err := WebhookHeaders(nil, header.Values)
	require.NoError(t, err)
	assert.Equal(t, "{}", none)
}
