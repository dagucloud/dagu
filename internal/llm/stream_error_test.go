// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
)

func TestStreamError(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "null", `""`} {
		assert.NoError(t, llm.StreamError("openai", json.RawMessage(raw)), "raw %q", raw)
	}

	// OpenAI sends string codes, and some local servers send a bare string.
	// Without a numeric status, the error is retried.
	for provider, raw := range map[string]string{
		"openai": `{"code":"server_error","message":"boom"}`,
		"local":  `"boom"`,
	} {
		err := llm.StreamError(provider, json.RawMessage(raw))
		assert.EqualError(t, err, llm.NewAPIError(provider, 0, "boom").Error())
		assert.True(t, llm.IsRetryable(err), "raw %s", raw)
	}

	// A numeric client error code is not retried.
	assert.False(t, llm.IsRetryable(
		llm.StreamError("gemini", json.RawMessage(`{"code":400,"message":"bad"}`))))
}
