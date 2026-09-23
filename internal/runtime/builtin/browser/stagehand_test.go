// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
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
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive a real Chrome through the Stagehand SDK with a scripted
// model. They pin the SDK behavior the executor depends on: extraction with
// a schema chosen at run time, replay of recorded actions without a model
// call, and reattaching to a browser left running by an exited process.

const detachHelperEnv = "DAGU_BROWSER_DETACH_HELPER"

const shopPage = `<!doctype html><html><head><title>Shop</title></head><body>
<h1>Widget Shop</h1>
<ul><li>Alpha - $3.50</li><li>Beta - $7.25</li></ul>
<p id="status">open</p>
<button onclick="document.getElementById('status').textContent='clicked'">Submit</button>
</body></html>`

// elementIDPattern finds a node ID in the accessibility tree the runtime
// sends to the model, such as "[0-23] button: Submit".
var elementIDPattern = regexp.MustCompile(`\[(\d+-\d+)\] button: Submit`)

// requireChrome skips when no Chrome is installed, except in CI, where the
// runners provide one and a missing browser is a setup failure.
func requireChrome(t *testing.T) {
	t.Helper()
	if chromePath() != "" {
		return
	}
	if os.Getenv("CI") != "" {
		t.Fatal("Chrome is required in CI; set CHROME_PATH")
	}
	t.Skip("Chrome is not installed; set CHROME_PATH to run browser tests")
}

func chromePath() string {
	if path := os.Getenv("CHROME_PATH"); path != "" {
		return path
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium"}
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
			return candidate
		}
	}
	return ""
}

func serveShop(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, shopPage)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// shopModel answers the runtime's model requests for the shop page.
type shopModel struct {
	mu       sync.Mutex
	requests []generateRequest
}

func (m *shopModel) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *shopModel) generate(_ context.Context, req generateRequest) (generateResponse, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	text := ""
	for _, message := range req.Messages {
		text += message.Text
	}
	schema := string(req.Schema)
	var answer string
	switch {
	case strings.Contains(schema, "elementId"):
		match := elementIDPattern.FindStringSubmatch(text)
		if match == nil {
			return generateResponse{}, fmt.Errorf("submit button not found in prompt:\n%s", text)
		}
		answer = fmt.Sprintf(`{"action":{"elementId":%q,"description":"Submit button","method":"click","arguments":[]},"twoStep":false}`, match[1])
	case strings.Contains(schema, "completed"):
		answer = `{"completed":true,"progress":"done"}`
	default:
		answer = `{"title":"Widget Shop","items":[{"name":"Alpha","price":3.5},{"name":"Beta","price":7.25}],"status":"open"}`
	}
	return generateResponse{JSON: json.RawMessage(answer), Usage: tokenUsage{Input: 10, Output: 2}}, nil
}

func launchShop(t *testing.T, model *shopModel) engine {
	t.Helper()
	requireChrome(t)
	ctx := t.Context()
	eng, err := stagehandLauncher{}.Launch(ctx, launchOptions{
		Executable:  chromePath(),
		Headless:    true,
		UserDataDir: t.TempDir(),
		Generate:    model.generate,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = eng.Close(context.WithoutCancel(ctx)) })
	require.NoError(t, eng.Goto(ctx, serveShop(t), time.Minute))
	return eng
}

func TestStagehandExtractWithRuntimeSchema(t *testing.T) {
	t.Parallel()

	eng := launchShop(t, &shopModel{})
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["title","items","status"],"properties":{"title":{"type":"string"},"items":{"type":"array","items":{"type":"object","required":["name","price"],"properties":{"name":{"type":"string"},"price":{"type":"number"}}}},"status":{"type":"string","enum":["open","closed"]}}}`)

	data, err := eng.Extract(t.Context(), "The shop title, items, and status", schema, time.Minute)
	require.NoError(t, err)
	assert.JSONEq(t, `{"title":"Widget Shop","items":[{"name":"Alpha","price":3.5},{"name":"Beta","price":7.25}],"status":"open"}`, string(data))
}

func TestStagehandActRecordsReplayableActions(t *testing.T) {
	t.Parallel()

	model := &shopModel{}
	eng := launchShop(t, model)

	outcome, err := eng.Act(t.Context(), "Click the Submit button", nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	require.NotEmpty(t, outcome.Actions)

	requests := model.requestCount()
	replayed, err := eng.Replay(t.Context(), outcome.Actions, nil, time.Minute)
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, requests, model.requestCount(), "replay makes no model call")
}

// TestStagehandDetachHelper runs in a child process: it opens a page, leaves
// the browser running, prints the handle, and exits.
func TestStagehandDetachHelper(t *testing.T) {
	if os.Getenv(detachHelperEnv) == "" {
		t.Skip("helper process")
	}
	eng, err := stagehandLauncher{}.Launch(t.Context(), launchOptions{
		Executable:  chromePath(),
		Headless:    true,
		UserDataDir: os.Getenv(detachHelperEnv),
		Generate:    (&shopModel{}).generate,
	})
	require.NoError(t, err)
	require.NoError(t, eng.Goto(t.Context(), "data:text/html,"+strings.ReplaceAll(shopPage, "#", "%23"), time.Minute))
	require.NoError(t, eng.Detach(t.Context()))
	handle, err := json.Marshal(eng.Handle())
	require.NoError(t, err)
	fmt.Printf("HANDLE %s\n", handle)
}

// A browser left running by an exited step process can be reattached after
// its extension's service worker has gone idle, keeps its page, and closes.
func TestStagehandReattachAfterProcessExit(t *testing.T) {
	t.Parallel()
	requireChrome(t)

	userDataDir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStagehandDetachHelper$", "-test.v")
	cmd.Env = append(os.Environ(), detachHelperEnv+"="+userDataDir)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	match := regexp.MustCompile(`HANDLE (\{.*\})`).FindSubmatch(output)
	require.NotNil(t, match, string(output))
	var handle browserHandle
	require.NoError(t, json.Unmarshal(match[1], &handle))
	t.Cleanup(func() {
		_ = browserhost.CloseBrowser(context.Background(), handle.CDPURL)
		_ = os.RemoveAll(handle.ExtensionDir)
	})

	// The extension's service worker stops after about 30 seconds idle;
	// reattaching must wake it.
	time.Sleep(35 * time.Second)
	require.NoError(t, browserhost.Probe(t.Context(), handle.CDPURL), "the browser outlives the process")

	model := &shopModel{}
	eng, err := stagehandLauncher{}.Reattach(t.Context(), handle, launchOptions{Generate: model.generate})
	require.NoError(t, err)
	pageURL, err := eng.CurrentURL(t.Context())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(pageURL, "data:text/html"), pageURL)

	outcome, err := eng.Act(t.Context(), "Click the Submit button", nil, time.Minute)
	require.NoError(t, err)
	assert.True(t, outcome.Success, outcome.Message)

	require.NoError(t, eng.Close(t.Context()))
	require.Eventually(t, func() bool {
		return browserhost.Probe(context.Background(), handle.CDPURL) != nil
	}, 10*time.Second, 200*time.Millisecond, "closing terminates the browser")
}
