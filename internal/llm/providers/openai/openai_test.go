// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/require"
)

// A single SSE data line larger than bufio.Scanner's default 64 KiB limit must
// not terminate the stream.
func TestStreamResponse_LargeEvent(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("a", 200*1024)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"" + big + "\"}}]}\n\n" +
		"data: [DONE]\n\n"

	provider := &Provider{}
	events := make(chan llm.StreamEvent)
	go provider.streamResponse(context.Background(), io.NopCloser(strings.NewReader(stream)), events)

	var deltas []string
	done := false
	for event := range events {
		require.NoError(t, event.Error)
		if event.Done {
			done = true
			continue
		}
		deltas = append(deltas, event.Delta)
	}
	require.True(t, done)
	require.Equal(t, []string{big}, deltas)
}
