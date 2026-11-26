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
	assert.EqualError(t,
		llm.StreamError("openai", json.RawMessage(`{"code":"server_error","message":"boom"}`)),
		llm.NewAPIError("openai", 0, "boom").Error())
	assert.EqualError(t,
		llm.StreamError("local", json.RawMessage(`"boom"`)),
		llm.NewAPIError("local", 0, "boom").Error())
}
