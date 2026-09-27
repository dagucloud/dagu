// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec073_computer holds black-box conformance tests for the computer
// actions. Tests that operate a real desktop run only on Windows with
// DAGU_DESKTOP_E2E=1, since they type into the session they run in.
package spec073_computer_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

const greeting = "hello from dagu"

// desktopCommandTimeout bounds a command that operates the desktop through
// several model rounds.
const desktopCommandTimeout = 3 * time.Minute

func TestComputerValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ fixture, message string }{
		{"missing_llm.yaml", "computer actions need a model"},
		{"duplicate_output.yaml", `output "invoice" is already extracted by do[0]`},
		{"native_mode.yaml", `provider "openrouter" has no native computer use`},
		{"unknown_variable.yaml", "act references %password%"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			result := harness.NewRunner(t).Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}

// A secret written into an instruction fails the step before the desktop is
// touched or a model is asked.
func TestComputerSecretInInstruction(t *testing.T) {
	t.Parallel()

	result := harness.NewRunner(t).RunWithEnv([]string{"ERP_TOKEN=tok-12345"}, "start", "secret_instruction.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("contains the value of secret ERP_TOKEN")
	result.ExpectStderrNotContains("tok-12345")
}

func TestComputerUnsupportedPlatform(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("computer steps are supported on this platform")
	}

	result := harness.NewRunner(t).Run("start", "unsupported.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("desktop automation is supported on macOS and Windows only")
}

// scriptedModel answers generic-mode computer requests in the OpenAI chat
// format: the first act round types the greeting and saves it to the target
// path, the next reports the task done, and extract requests return the
// greeting.
type scriptedModel struct {
	target string
	mu     sync.Mutex
	images int
}

func (m *scriptedModel) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Tools) == 0 {
		http.Error(w, "expected a tool request", http.StatusBadRequest)
		return
	}
	rounds := 0
	for _, message := range req.Messages {
		if message.Role == "assistant" {
			rounds++
		}
		if strings.Contains(string(message.Content), `"image_url"`) {
			m.mu.Lock()
			m.images++
			m.mu.Unlock()
		}
	}

	type toolCall struct {
		name      string
		arguments map[string]any
	}
	var calls []toolCall
	switch {
	case req.Tools[0].Function.Name == "respond":
		calls = []toolCall{{"respond", map[string]any{"text": greeting}}}
	case rounds == 0:
		calls = []toolCall{
			{"type", map[string]any{"text": greeting}},
			{"key", map[string]any{"keys": "ctrl+s"}},
			{"wait", map[string]any{"seconds": 2}},
			{"type", map[string]any{"text": m.target}},
			{"key", map[string]any{"keys": "Return"}},
			{"wait", map[string]any{"seconds": 2}},
		}
	default:
		calls = []toolCall{{"done", map[string]any{"success": true, "summary": "Saved the greeting"}}}
	}
	encoded := make([]map[string]any, 0, len(calls))
	for i, c := range calls {
		arguments, _ := json.Marshal(c.arguments)
		encoded = append(encoded, map[string]any{
			"id":       fmt.Sprintf("call_%d_%d", rounds, i),
			"type":     "function",
			"function": map[string]any{"name": c.name, "arguments": string(arguments)},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "", "tool_calls": encoded},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
	})
}

// The step launches Notepad, types into it and saves the file through the
// model's actions, then reads the window into an output.
func TestComputerNotepad(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 on an interactive Windows desktop to run")
	}
	t.Cleanup(func() { _ = exec.Command("taskkill", "/IM", "notepad.exe", "/F").Run() })

	dagu := harness.NewRunner(t).WithCommandTimeout(desktopCommandTimeout)
	model := &scriptedModel{target: dagu.ProjectPath("greeting.txt")}
	server := httptest.NewServer(http.HandlerFunc(model.serve))
	t.Cleanup(server.Close)

	dagu.RunWithEnv([]string{"LLM_BASE_URL=" + server.URL}, "start", "notepad.yaml").ExpectExitCode(0)

	dagu.ExpectTextFileContent("greeting.txt", greeting)
	dagu.ExpectTextFileContent("extracted.out", greeting+"\n")
	model.mu.Lock()
	defer model.mu.Unlock()
	require.Positive(t, model.images, "the model is shown screenshots")
}
