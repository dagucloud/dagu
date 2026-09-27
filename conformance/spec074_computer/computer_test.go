// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec074_computer holds black-box conformance tests for the computer
// actions. Tests that operate a real desktop run only on Windows with
// DAGU_DESKTOP_E2E=1, since they type into the session they run in.
package spec074_computer_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

// magenta is the color of the test window's text box, which the scripted
// model looks for in each screenshot.
var magenta = color.RGBA{R: 255, G: 0, B: 255, A: 255}

// minWindowPixels is how many magenta pixels count as the window being
// shown.
const minWindowPixels = 500

// scriptedModel answers generic-mode computer requests in the OpenAI chat
// format by reading the screenshot it is sent: it waits until the magenta
// text box appears, clicks it and types the greeting, then reports the task
// done. Extract requests return the greeting.
type scriptedModel struct {
	mu        sync.Mutex
	sawWindow bool
}

type chatRequest struct {
	Messages []struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		ToolCalls []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

func (m *scriptedModel) serve(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Tools) == 0 {
		http.Error(w, "expected a tool request", http.StatusBadRequest)
		return
	}
	var calls []toolCall
	switch {
	case req.Tools[0].Function.Name == "respond":
		calls = []toolCall{{"respond", map[string]any{"text": greeting}}}
	case req.typed():
		calls = []toolCall{{"done", map[string]any{"success": true, "summary": "Typed the greeting"}}}
	default:
		center, found, err := req.findWindow()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !found {
			calls = []toolCall{{"wait", map[string]any{"seconds": 1}}}
			break
		}
		m.mu.Lock()
		m.sawWindow = true
		m.mu.Unlock()
		calls = []toolCall{
			{"click", map[string]any{"x": center.X, "y": center.Y}},
			{"type", map[string]any{"text": greeting}},
		}
	}
	writeToolCalls(w, calls)
}

// typed reports whether an earlier turn typed the greeting.
func (req chatRequest) typed() bool {
	for _, message := range req.Messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name == "type" {
				return true
			}
		}
	}
	return false
}

// findWindow returns the center of the magenta text box in the latest
// screenshot, in its pixels.
func (req chatRequest) findWindow() (image.Point, bool, error) {
	var dataURL string
	for _, message := range req.Messages {
		var parts []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if json.Unmarshal(message.Content, &parts) != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == "image_url" {
				dataURL = part.ImageURL.URL
			}
		}
	}
	_, encoded, ok := strings.Cut(dataURL, ";base64,")
	if !ok {
		return image.Point{}, false, errors.New("no screenshot in the request")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return image.Point{}, false, err
	}
	screen, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return image.Point{}, false, err
	}
	var sum image.Point
	count := 0
	bounds := screen.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if near(screen.At(x, y), magenta) {
				sum = sum.Add(image.Pt(x, y))
				count++
			}
		}
	}
	if count < minWindowPixels {
		return image.Point{}, false, nil
	}
	return sum.Div(count), true, nil
}

// near reports whether a color is within a small distance of target,
// allowing for scaling that blends edge pixels.
func near(c color.Color, target color.RGBA) bool {
	r, g, b, _ := c.RGBA()
	return absDiff(r>>8, uint32(target.R)) < 40 && absDiff(g>>8, uint32(target.G)) < 40 && absDiff(b>>8, uint32(target.B)) < 40
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

type toolCall struct {
	name      string
	arguments map[string]any
}

func writeToolCalls(w http.ResponseWriter, calls []toolCall) {
	encoded := make([]map[string]any, 0, len(calls))
	for i, c := range calls {
		arguments, _ := json.Marshal(c.arguments)
		encoded = append(encoded, map[string]any{
			"id":       fmt.Sprintf("call_%d", i),
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

// The step launches a window, waits until the scripted model sees it,
// clicks it and types into it, then reads it into an output. The window
// writes its text to a file, which shows what reached it.
func TestComputerTypesIntoWindow(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 on an interactive Windows desktop to run")
	}

	dagu := harness.NewRunner(t).WithCommandTimeout(desktopCommandTimeout)
	// Registered after the runner's project directory, so the window is
	// closed before that directory is removed.
	t.Cleanup(func() { closeTestWindow(t) })
	// CI uploads the screenshots from this directory when the test fails.
	artifacts := os.Getenv("DESKTOP_E2E_ARTIFACTS")
	if artifacts == "" {
		artifacts = dagu.ProjectPath("artifacts")
	}
	model := &scriptedModel{}
	server := httptest.NewServer(http.HandlerFunc(model.serve))
	t.Cleanup(server.Close)

	result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + server.URL, "DESKTOP_ARTIFACTS=" + artifacts}, "start", "textbox.yaml")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("stdout:\n%s\nstderr:\n%s", result.Stdout(), result.Stderr())
		}
	})
	result.ExpectExitCode(0)

	dagu.ExpectTextFileContent("greeting.txt", greeting)
	dagu.ExpectTextFileContent("extracted.out", greeting+"\n")
	model.mu.Lock()
	defer model.mu.Unlock()
	require.True(t, model.sawWindow, "the model found the window in a screenshot")
}

const testWindowFilter = "WINDOWTITLE eq Dagu desktop test"

// closeTestWindow ends the test window and waits for its process to exit.
func closeTestWindow(t *testing.T) {
	t.Helper()
	_ = exec.Command("taskkill", "/F", "/FI", testWindowFilter).Run()
	require.Eventually(t, func() bool {
		out, err := exec.Command("tasklist", "/FI", testWindowFilter, "/NH").Output()
		return err == nil && !strings.Contains(strings.ToLower(string(out)), "powershell")
	}, 30*time.Second, 200*time.Millisecond, "the test window did not exit")
}
