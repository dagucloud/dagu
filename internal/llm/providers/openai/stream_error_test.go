// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An error sent after the 200 status must fail the stream, even when text has
// already arrived.
func TestStreamResponse_Error(t *testing.T) {
	t.Parallel()

	stream := `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n" +
		`data: {"error":{"code":503,"message":"upstream overloaded"}}` + "\n\n"

	provider := &Provider{}
	events := make(chan llm.StreamEvent)
	go provider.streamResponse(context.Background(), io.NopCloser(strings.NewReader(stream)), events)

	var got []llm.StreamEvent
	for event := range events {
		got = append(got, event)
	}

	require.Len(t, got, 2)
	assert.Equal(t, "partial", got[0].Delta)
	assert.True(t, got[1].Done)
	require.ErrorContains(t, got[1].Error, "upstream overloaded")
	assert.True(t, llm.IsRetryable(got[1].Error))
}
