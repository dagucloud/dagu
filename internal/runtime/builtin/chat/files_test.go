// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestFiles(t *testing.T) (dir, note, receipt string) {
	t.Helper()
	dir = t.TempDir()
	note = filepath.Join(dir, "note.txt")
	require.NoError(t, os.WriteFile(note, []byte("Pay 9,800 yen by Friday.\n"), 0o600))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	receipt = filepath.Join(dir, "receipt.png")
	require.NoError(t, os.WriteFile(receipt, buf.Bytes(), 0o600))
	return dir, note, receipt
}

func TestAttachFilesPutsTextBeforeThePromptAndImagesWithIt(t *testing.T) {
	t.Parallel()

	_, note, receipt := writeTestFiles(t)
	messages := []ir.LLMMessage{
		{Role: ir.LLMRoleSystem, Content: "Be brief."},
		{Role: ir.LLMRoleUser, Content: "What is due?"},
	}

	require.NoError(t, attachFiles(chatRuntimeContext(t, nil), []string{note + "\n" + receipt}, messages))

	assert.Equal(t, "Be brief.", messages[0].Content)
	assert.Equal(t, "<file name=\"note.txt\">\nPay 9,800 yen by Friday.\n</file>\n\n[Image 1 is receipt.png.]\n\nWhat is due?", messages[1].Content)
	require.Len(t, messages[1].Images, 1)
	assert.Equal(t, "image/png", messages[1].Images[0].MediaType)
	assert.Empty(t, messages[0].Images)
}

func TestAttachFilesResolvesReferences(t *testing.T) {
	t.Parallel()

	_, note, _ := writeTestFiles(t)
	messages := []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "Read it."}}

	require.NoError(t, attachFiles(chatRuntimeContext(t, []string{"NOTE=" + note}), []string{"${params.NOTE}"}, messages))

	assert.Contains(t, messages[0].Content, "Pay 9,800 yen by Friday.")
}

func TestAttachFilesRefuses(t *testing.T) {
	t.Parallel()

	dir, note, _ := writeTestFiles(t)
	ctx := chatRuntimeContext(t, nil)

	err := attachFiles(ctx, []string{note}, []ir.LLMMessage{{Role: ir.LLMRoleSystem, Content: "only system"}})
	require.ErrorContains(t, err, "files needs a user prompt")

	err = attachFiles(ctx, []string{filepath.Join(dir, "missing.pdf")}, []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "?"}})
	require.ErrorContains(t, err, "does not exist")

	require.NoError(t, attachFiles(ctx, nil, []ir.LLMMessage{{Role: ir.LLMRoleSystem, Content: "no files, no prompt needed"}}))
}

func TestExecutionMessagesAttachFilesOnceNotOnPushBack(t *testing.T) {
	t.Parallel()

	_, note, _ := writeTestFiles(t)
	executor := &Executor{
		step:     ir.Step{LLM: &ir.LLMConfig{Files: []string{note}}},
		messages: []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "What is due?"}},
	}
	ctx := chatRuntimeContext(t, nil)

	messages, err := executor.executionMessages(ctx)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "Pay 9,800 yen by Friday.")

	executor.contextMessages = messages
	executor.SetPushBackContext(map[string]string{"FEEDBACK": "say the day"}, 1)
	messages, err = executor.executionMessages(ctx)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Contains(t, messages[0].Content, "Pay 9,800 yen by Friday.", "the first request's text stays in the conversation")
	assert.NotContains(t, messages[1].Content, "Pay 9,800 yen", "the feedback is sent without the files again")
}

func TestImagesGoWithTheRequestAndNotWithTheRun(t *testing.T) {
	t.Parallel()

	_, _, receipt := writeTestFiles(t)
	stream := false
	cfg := &ir.LLMConfig{Provider: "openai", Model: "gpt-4o", Stream: &stream, Files: []string{receipt}}
	var stdout bytes.Buffer
	executor := &Executor{
		stdout:   stdoutWriter(&stdout),
		step:     ir.Step{LLM: cfg},
		messages: []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "What does the receipt say?"}},
	}
	var sent []llmpkg.Message
	provider := &mockProvider{
		chatFunc: func(_ context.Context, req *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
			sent = req.Messages
			return &llmpkg.ChatResponse{Content: "9,800 yen", FinishReason: "stop"}, nil
		},
	}

	messages, err := executor.executionMessages(chatRuntimeContext(t, nil))
	require.NoError(t, err)
	require.NoError(t, executor.runSimpleForModel(context.Background(), provider, messages, cfg))

	require.Len(t, sent, 1)
	require.Len(t, sent[0].Images, 1, "the request carries the image")
	assert.Equal(t, "image/png", sent[0].Images[0].MediaType)
	assert.Contains(t, sent[0].Content, "[Image 1 is receipt.png.]")
	saved := executor.GetMessages()
	require.Len(t, saved, 2)
	assert.Empty(t, saved[0].Images, "the run keeps the text, not the image")
	assert.Contains(t, saved[0].Content, "[Image 1 is receipt.png.]")
}
