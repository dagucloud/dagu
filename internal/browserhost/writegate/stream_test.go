// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cutRequest struct {
	Params struct {
		Request struct {
			URL             string            `json:"url"`
			PostData        string            `json:"postData"`
			PostDataSize    int64             `json:"postDataSize"`
			PostDataEntries []json.RawMessage `json:"postDataEntries"`
		} `json:"request"`
		RequestID string `json:"requestId"`
	} `json:"params"`
}

func cut(t *testing.T, message string, limit int) cutRequest {
	t.Helper()
	data, err := cutMessage(strings.NewReader(message), limit)
	require.NoError(t, err)
	var decoded cutRequest
	require.NoError(t, json.Unmarshal(data, &decoded), "%s", data)
	return decoded
}

func TestCutMessageKeepsTheStartOfABody(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a", 100)
	message := `{"method":"Fetch.requestPaused","params":{"request":{"url":"https://x/postData","postData":"` + body +
		`","postDataEntries":[{"bytes":"YWFh"},{"bytes":"YmJi"}]},"requestId":"r1"}}`

	got := cut(t, message, 10)
	assert.Equal(t, "aaaaaaaaaa", got.Params.Request.PostData)
	assert.Equal(t, int64(100), got.Params.Request.PostDataSize)
	assert.Empty(t, got.Params.Request.PostDataEntries)
	assert.Equal(t, "r1", got.Params.RequestID, "what follows the body is kept")
	assert.Equal(t, "https://x/postData", got.Params.Request.URL, "a key's name inside a value is not a key")
}

func TestCutMessageNeverSplitsAnEscape(t *testing.T) {
	t.Parallel()

	message := `{"params":{"request":{"postData":"ab\"cé\\\ndone"}}}`

	assert.Equal(t, `ab"`, cut(t, message, 4).Params.Request.PostData)
	got := cut(t, message, 1000)
	assert.Equal(t, "ab\"cé\\\ndone", got.Params.Request.PostData)
	// An escape counts as one byte, and é as the two bytes it takes.
	assert.Equal(t, int64(12), got.Params.Request.PostDataSize)
}

func TestCutMessageLeavesOtherMessages(t *testing.T) {
	t.Parallel()

	message := `{"id":3,"result":{"data":"postData","nested":{"a":[1,{"b":"]"}]}}}`
	data, err := cutMessage(strings.NewReader(message), 1)
	require.NoError(t, err)
	assert.JSONEq(t, message, string(data))
}

func TestCutMessageRejectsATruncatedMessage(t *testing.T) {
	t.Parallel()

	_, err := cutMessage(strings.NewReader(`{"params":{"request":{"postData":"abc`), 10)
	assert.ErrorIs(t, err, errUnterminated)
}
