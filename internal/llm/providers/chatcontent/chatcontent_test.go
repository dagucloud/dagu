// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatcontent_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/providers/chatcontent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContent(t *testing.T) {
	t.Parallel()

	t.Run("text only stays a string", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "hello", chatcontent.Content("hello", nil))
	})

	t.Run("images come before text", func(t *testing.T) {
		t.Parallel()
		content := chatcontent.Content("describe", []llm.Image{{MediaType: "image/png", Data: []byte{1, 2}}})
		body, err := json.Marshal(content)
		require.NoError(t, err)
		assert.JSONEq(t, `[
			{"type":"image_url","image_url":{"url":"data:image/png;base64,AQI="}},
			{"type":"text","text":"describe"}
		]`, string(body))
	})
}
