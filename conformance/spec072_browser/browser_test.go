// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec072_browser holds black-box conformance tests for the browser
// actions. They drive a real Chrome against a local page with a scripted
// OpenAI-compatible model, so no external service is needed.
package spec072_browser_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

const shopPage = `<!doctype html><html><head><title>Checkout</title></head><body>
<h1>Widget Checkout</h1>
<p id="status">Cart has 2 items</p>
<button onclick="document.getElementById('status').textContent='Order confirmed: A-100'">Place order</button>
<a href="/report.csv">Download report</a>
</body></html>`

// rowsPage greets with an alert while it loads and asks to confirm before
// it deletes the row.
const rowsPage = `<!doctype html><html><head><title>Rows</title></head><body>
<h1>Rows</h1>
<script>alert('Welcome back')</script>
<p id="status">1 row</p>
<button onclick="if (confirm('Delete the row?')) document.getElementById('status').textContent='Row deleted'">Delete row</button>
</body></html>`

const reportBody = "id,total\n1,10\n2,20\n"

// Model request kinds, told apart by the response schema the browser
// runtime asks for.
const (
	kindAct       = "act"
	kindStatement = "statement"
	kindExtract   = "extract"
	kindMetadata  = "metadata"
)

var (
	buttonPattern    = regexp.MustCompile(`\[(\d+-\d+)\] (?:button|link): ([^\n]+)`)
	quotedPattern    = regexp.MustCompile(`'([^']+)'`)
	orderPattern     = regexp.MustCompile(`Order confirmed: (\S+)`)
	headingPattern   = regexp.MustCompile(`heading: ([^\n]+)`)
	pageMarkerPrefix = regexp.MustCompile(`(?s)^(.*?)(Accessibility Tree:|DOM:)(.*)$`)
)

// scriptedModel answers like a model reading the page it is sent: it picks
// the button named in an act instruction, judges quoted text against the
// page, and extracts values from the page text.
type scriptedModel struct {
	mu     sync.Mutex
	counts map[string]int
}

func startModel(t *testing.T) (*scriptedModel, string) {
	t.Helper()
	model := &scriptedModel{counts: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(model.serve))
	t.Cleanup(server.Close)
	return model, server.URL
}

func (m *scriptedModel) count(kind string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[kind]
}

func (m *scriptedModel) total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, count := range m.counts {
		total += count
	}
	return total
}

func (m *scriptedModel) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Tools) == 0 {
		http.Error(w, "expected a tool request", http.StatusBadRequest)
		return
	}
	user := ""
	for _, message := range req.Messages {
		if message.Role == "user" {
			user = message.Content
		}
	}
	instruction, page := user, ""
	if match := pageMarkerPrefix.FindStringSubmatch(user); match != nil {
		instruction, page = match[1], match[3]
	}
	schema := string(req.Tools[0].Function.Parameters)

	var kind, answer string
	switch {
	case strings.Contains(schema, "elementId"):
		kind = kindAct
		answer = `{"action":null,"twoStep":false}`
		for _, button := range buttonPattern.FindAllStringSubmatch(page, -1) {
			if strings.Contains(instruction, strings.TrimSpace(button[2])) {
				answer = `{"action":{"elementId":"` + button[1] + `","description":"button","method":"click","arguments":[]},"twoStep":false}`
				break
			}
		}
	case strings.Contains(schema, `"completed"`):
		kind = kindMetadata
		answer = `{"completed":true,"progress":"done"}`
	case strings.Contains(schema, `"answer"`):
		kind = kindStatement
		holds := false
		if quoted := quotedPattern.FindStringSubmatch(instruction); quoted != nil {
			holds = strings.Contains(page, quoted[1])
		}
		answer = `{"answer":` + map[bool]string{true: "true", false: "false"}[holds] + `,"reason":"judged from the page text"}`
	case strings.Contains(schema, "order_number"):
		kind = kindExtract
		value := ""
		if match := orderPattern.FindStringSubmatch(page); match != nil {
			value = match[1]
		}
		answer = `{"order_number":"` + value + `"}`
	default:
		kind = kindExtract
		value := ""
		if match := headingPattern.FindStringSubmatch(page); match != nil {
			value = strings.TrimSpace(match[1])
		}
		answer = `{"heading":"` + value + `"}`
	}
	m.mu.Lock()
	m.counts[kind]++
	m.mu.Unlock()

	arguments, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"respond","arguments":`+string(arguments)+`}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
}

func startShop(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rows" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, rowsPage)
			return
		}
		if r.URL.Path == "/report.csv" {
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
			_, _ = io.WriteString(w, reportBody)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, shopPage)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// requireChrome skips when no Chrome is installed, except in CI, where the
// runners provide one.
func requireChrome(t *testing.T) {
	t.Helper()
	if os.Getenv("CHROME_PATH") != "" {
		return
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	case "windows":
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				candidates = append(candidates, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	default:
		for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium-browser", "chromium"} {
			if path, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, path)
			}
		}
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return
		}
	}
	if os.Getenv("CI") != "" {
		t.Fatal("Chrome is required in CI; set CHROME_PATH")
	}
	t.Skip("Chrome is not installed; set CHROME_PATH to run browser conformance tests")
}

type browserEnv struct {
	dagu  *harness.Runner
	model *scriptedModel
	env   []string
}

func newBrowserEnv(t *testing.T) *browserEnv {
	t.Helper()
	requireChrome(t)
	model, modelURL := startModel(t)
	env := []string{"SHOP_URL=" + startShop(t), "LLM_BASE_URL=" + modelURL}
	if runtime.GOOS == "windows" {
		// The harness points the profile folders at empty temporary paths;
		// Chrome on Windows needs the real ones to start.
		env = append(env, "USERPROFILE="+os.Getenv("USERPROFILE"), "APPDATA="+os.Getenv("APPDATA"))
	}
	return &browserEnv{
		dagu:  harness.NewRunner(t),
		model: model,
		env:   env,
	}
}

func TestBrowserRunCheckout(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "checkout.yaml").ExpectExitCode(0)

	b.dagu.ExpectTextFileContent("order.out", "A-100\n")
	b.dagu.ExpectFileContains("shots.out", "confirmation.png", "final.png")
	require.Equal(t, 1, b.model.count(kindAct))
}

// A file the page downloads is saved in the run artifacts before the step
// ends.
func TestBrowserDownload(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "download.yaml").ExpectExitCode(0)
	b.dagu.ExpectTextFileContent("report.out", reportBody)
}

// A page that opens dialogs keeps working: the alert shown while it loads
// and the confirm raised by an act are both accepted.
func TestBrowserDialogs(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "dialogs.yaml").ExpectExitCode(0)
	require.Equal(t, 1, b.model.count(kindAct))
}

func TestBrowserExtract(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "extract.yaml").ExpectExitCode(0)
	b.dagu.ExpectTextFileContent("heading.out", "Widget Checkout\n")
}

// A second run of the same step replays the recorded click without asking
// the model.
func TestBrowserReplayCache(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	env := append(b.env, "DAGU_HOME="+t.TempDir())
	b.dagu.RunWithEnv(env, "start", "click.yaml").ExpectExitCode(0)
	require.Equal(t, 1, b.model.count(kindAct))

	b.dagu.RunWithEnv(env, "start", "click.yaml").ExpectExitCode(0)
	require.Equal(t, 1, b.model.count(kindAct), "the replayed act makes no model request")
}

func TestBrowserSecretInInstruction(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	result := b.dagu.RunWithEnv(append(b.env, "SHOP_TOKEN=tok-12345"), "start", "secret_instruction.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("contains the value of secret SHOP_TOKEN")
	result.ExpectStderrNotContains("tok-12345")
	require.Zero(t, b.model.total())
}

func TestBrowserAllowedDomains(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	result := b.dagu.RunWithEnv(b.env, "start", "allowed_domains.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("outside browser.allowed_domains")
	require.Zero(t, b.model.total())
}

func TestBrowserValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ fixture, message string }{
		{"missing_llm.yaml", "browser actions need a model"},
		{"duplicate_output.yaml", `output "order" is already extracted by do[0]`},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			result := harness.NewRunner(t).Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}
