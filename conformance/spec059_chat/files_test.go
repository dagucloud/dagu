// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec059_chat_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The files a step names go to the model with its prompt: a text file's
// text ahead of the prompt, and an image as an image part.
func TestChatCompletionFiles(t *testing.T) {
	t.Parallel()

	requests := make(chan json.RawMessage, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		select {
		case requests <- body:
		default:
			t.Error("unexpected extra request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"9,800 yen"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)

	dagu := harness.NewRunner(t)
	dagu.WriteFile("note.txt", "Pay 9,800 yen by Friday.\n")
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	require.NoError(t, os.WriteFile(dagu.ProjectPath("receipt.png"), picture.Bytes(), 0o600))

	result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + srv.URL}, "start", "files.yaml")
	srv.Close()
	close(requests)
	result.ExpectExitCode(0)

	data, err := os.ReadFile(dagu.ProjectPath("result.out"))
	require.NoError(t, err)
	assert.Equal(t, "9,800 yen\n", string(data))

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string            `json:"type"`
				Text     string            `json:"text"`
				ImageURL map[string]string `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(<-requests, &req))
	require.Len(t, req.Messages, 1)
	require.Len(t, req.Messages[0].Content, 2)
	assert.Equal(t, "image_url", req.Messages[0].Content[0].Type)
	assert.True(t, strings.HasPrefix(req.Messages[0].Content[0].ImageURL["url"], "data:image/png;base64,"))
	assert.Equal(t, "text", req.Messages[0].Content[1].Type)
	assert.Equal(t, "<file name=\"note.txt\">\nPay 9,800 yen by Friday.\n</file>\n\n[Image 1 is receipt.png.]\n\nWhat is due?", req.Messages[0].Content[1].Text)
}

// A file the step cannot read fails the step before any request.
func TestChatCompletionFilesMissing(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.WriteFile("note.txt", "Pay 9,800 yen by Friday.\n")

	result := dagu.RunWithEnv([]string{"LLM_BASE_URL=http://127.0.0.1:9"}, "start", "files.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("receipt.png does not exist")
}
