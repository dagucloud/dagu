// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// verifyPage accepts only the code 424242, so the page state shows whether the
// answer reached the browser that asked for it.
const verifyPage = `<!doctype html><html><head><title>Verify</title></head><body>
<h1>Verify your sign-in</h1>
<input id="code" aria-label="Code">
<button onclick="var c=document.getElementById('code').value;document.getElementById('result').textContent=(c==='424242'?'Verified '+c:'Rejected')">Verify</button>
<p id="result">Pending</p>
</body></html>`

var (
	browserElementPattern = regexp.MustCompile(`\[(\d+-\d+)\] (button|textbox): ([^\n]+)`)
	// The typed code is a variable, so it is masked in text sent to the model.
	browserVerifiedResult = regexp.MustCompile(`Verified|Rejected`)
)

// verifyModel answers the browser runtime like a model reading the verify
// page. Typing uses the %code% placeholder, which the browser fills in.
func verifyModel(w http.ResponseWriter, r *http.Request) {
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
	user := req.Messages[len(req.Messages)-1].Content
	instruction, page, _ := strings.Cut(user, "Accessibility Tree:")
	if page == "" {
		instruction, page, _ = strings.Cut(user, "DOM:")
	}
	schema := string(req.Tools[0].Function.Parameters)

	var answer string
	switch {
	case strings.Contains(schema, "elementId"):
		answer = `{"action":null,"twoStep":false}`
		for _, element := range browserElementPattern.FindAllStringSubmatch(page, -1) {
			if !strings.Contains(instruction, strings.TrimSpace(element[3])) {
				continue
			}
			method, arguments := "click", `[]`
			if element[2] == "textbox" {
				method, arguments = "fill", `["%code%"]`
			}
			answer = fmt.Sprintf(`{"action":{"elementId":%q,"description":%q,"method":%q,"arguments":%s},"twoStep":false}`,
				element[1], element[3], method, arguments)
			break
		}
	case strings.Contains(schema, `"completed"`):
		answer = `{"completed":true,"progress":"done"}`
	default:
		answer = fmt.Sprintf(`{"result":%q}`, browserVerifiedResult.FindString(page))
	}
	arguments, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"respond","arguments":`+string(arguments)+`}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
}

func requireBrowser(t *testing.T) {
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
	t.Skip("Chrome is not installed; set CHROME_PATH to run browser tests")
}

// An ask operation parks the step in Waiting; answering it through the API
// resumes the same browser, which receives the answer as a variable.
func TestBrowserAskResumesSameBrowser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ask operations are not supported on Windows")
	}
	requireBrowser(t)
	// The browser sandbox cannot start on every CI host.
	t.Setenv("DAGU_BROWSER_SANDBOX", "false")

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, verifyPage)
	}))
	t.Cleanup(page.Close)
	model := httptest.NewServer(http.HandlerFunc(verifyModel))
	t.Cleanup(model.Close)

	server := test.SetupServer(t)
	const dagName = "intg_browser_ask"
	spec := fmt.Sprintf(`llm:
  provider: local
  model: test-model
  base_url: %s
steps:
  - id: login
    action: browser.run
    with:
      url: %s
      do:
        - ask: {prompt: Enter the code sent to your phone, as: code}
        - act: Type %%code%% into the Code field
        - act: Click the Verify button
        - extract:
            instruction: The verification result
            schema:
              type: object
              properties:
                result: {type: string}
  - id: after
    depends: [login]
    run: echo "${steps.login.outputs.result}"
`, model.URL, page.URL)

	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	const runID = "browser-ask-run"
	runIDValue := runID
	server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{DagRunId: &runIDValue},
	).ExpectStatus(http.StatusOK).Send(t)

	var interactionID string
	waitForBrowserStep(t, server, dagName, runID, func(node *ir.Node) bool {
		session := node.AgentSession
		if node.Status != ir.NodeWaiting || session == nil || len(session.Interactions) == 0 {
			return false
		}
		interaction := session.Interactions[0]
		require.Equal(t, "Enter the code sent to your phone", interaction.Questions[0].Question)
		interactionID = interaction.ID
		return interaction.Status == ir.AgentInteractionPending
	})

	answers := [][]string{{"424242"}}
	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/steps/login/agent-interactions/%s/respond", dagName, runID, interactionID),
		api.AgentInteractionResponseRequest{Answers: &answers},
	).ExpectStatus(http.StatusOK).Send(t)

	status := waitForDAGRunStatus(t, server, dagName, runID, ir.Succeeded)
	after := nodeByName(t, status, "after")
	stdout, err := os.ReadFile(after.Stdout)
	require.NoError(t, err)
	require.Equal(t, "Verified", strings.TrimSpace(string(stdout)), "the page accepts only the answered code")

	login := nodeByName(t, status, "login")
	require.NotNil(t, login.AgentSession)
	require.Equal(t, ir.AgentSessionSucceeded, login.AgentSession.State)
	require.True(t, login.AgentSession.Interactions[0].Applied)
}

func waitForBrowserStep(t *testing.T, server test.Server, dagName, runID string, ready func(*ir.Node) bool) {
	t.Helper()
	deadline := time.Now().Add(intgTestTimeout(60 * time.Second))
	for time.Now().Before(deadline) {
		status, err := server.DAGRunMgr.GetSavedStatus(server.Context, ir.NewDAGRunRef(dagName, runID))
		if err == nil && status != nil {
			for _, node := range status.Nodes {
				if node.Step.Name == "login" && ready(node) {
					return
				}
			}
			require.NotEqual(t, ir.Failed, status.Status, "run failed before waiting for input")
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("step login did not wait for input within the timeout")
}
