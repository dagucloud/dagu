// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// launchShop starts a browser on the shop page. Browsers in these tests run
// without the sandbox, which cannot start on every CI host.
func launchShop(t *testing.T, model *shopModel) engine {
	t.Helper()
	eng := launchBrowser(t, launchOptions{Generate: model.generate})
	require.NoError(t, eng.Goto(t.Context(), serveShop(t), time.Minute))
	return eng
}

// launchBrowser starts a headless test browser with opts, closed when the
// test ends.
func launchBrowser(t *testing.T, opts launchOptions) engine {
	t.Helper()
	requireChrome(t)
	ctx := t.Context()
	opts.Executable = chromePath()
	opts.Headless = true
	opts.UserDataDir = t.TempDir()
	opts.NoSandbox = true
	eng, err := stagehandLauncher{}.Launch(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = eng.Close(context.WithoutCancel(ctx)) })
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
		NoSandbox:   true,
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

	// Chrome keeps profile files open for a moment after it exits, so the
	// directory is removed with retries instead of by t.TempDir.
	userDataDir, err := os.MkdirTemp("", "dagu-browser-test-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.Eventually(t, func() bool { return os.RemoveAll(userDataDir) == nil },
			10*time.Second, 200*time.Millisecond, "remove the browser profile")
	})
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

const reportBody = "id,total\n1,10\n2,20\n"

// serveReport serves a page linking to a CSV that arrives in slow chunks, so
// the download is still running when the click returns.
func serveReport(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><html><body><a href="/report.csv">Download report</a></body></html>`)
	})
	mux.HandleFunc("/report.csv", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
		for _, line := range strings.SplitAfter(reportBody, "\n") {
			_, _ = io.WriteString(w, line)
			w.(http.Flusher).Flush()
			time.Sleep(300 * time.Millisecond)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

var reportLinkPattern = regexp.MustCompile(`\[(\d+-\d+)\] link: Download report`)

// reportModel clicks the report link and answers nothing else.
func reportModel(_ context.Context, req generateRequest) (generateResponse, error) {
	text := ""
	for _, message := range req.Messages {
		text += message.Text
	}
	match := reportLinkPattern.FindStringSubmatch(text)
	if match == nil {
		return generateResponse{}, fmt.Errorf("report link not found in prompt:\n%s", text)
	}
	answer := fmt.Sprintf(`{"action":{"elementId":%q,"description":"report link","method":"click","arguments":[]},"twoStep":false}`, match[1])
	return generateResponse{JSON: json.RawMessage(answer)}, nil
}

// A download started by an act is saved whole under its suggested name
// before the watcher reports it.
func TestStagehandWaitsForDownloads(t *testing.T) {
	t.Parallel()
	requireChrome(t)

	downloads := t.TempDir()
	eng, err := stagehandLauncher{}.Launch(t.Context(), launchOptions{
		Executable:   chromePath(),
		Headless:     true,
		UserDataDir:  t.TempDir(),
		DownloadsDir: downloads,
		NoSandbox:    true,
		Generate:     reportModel,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = eng.Close(context.WithoutCancel(t.Context())) })
	require.NoError(t, eng.Goto(t.Context(), serveReport(t), time.Minute))

	outcome, err := eng.Act(t.Context(), "Click the Download report link", nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)

	names, err := eng.WaitForDownloads(t.Context(), 3*time.Second, time.Minute)
	require.NoError(t, err)
	require.Equal(t, []string{"report.csv"}, names)
	data, err := os.ReadFile(filepath.Join(downloads, "report.csv"))
	require.NoError(t, err)
	assert.Equal(t, reportBody, string(data))
}

func TestStagehandPageChecks(t *testing.T) {
	t.Parallel()

	eng := launchShop(t, &shopModel{})
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "Widget Shop")

	visible, err := eng.SelectorVisible(t.Context(), "#status")
	require.NoError(t, err)
	assert.True(t, visible)
	visible, err = eng.SelectorVisible(t.Context(), `#missing, [data-x="1"]`)
	require.NoError(t, err)
	assert.False(t, visible)

	// The first match is hidden; a later one is visible.
	require.NoError(t, eng.Goto(t.Context(), `data:text/html,<p class="row" style="display:none">a</p><p class="row">b</p>`, time.Minute))
	visible, err = eng.SelectorVisible(t.Context(), ".row")
	require.NoError(t, err)
	assert.True(t, visible)
}

// The process ID a launched browser reports is the browser itself: ending it
// closes the browser.
func TestStagehandReportsBrowserProcess(t *testing.T) {
	t.Parallel()

	eng := launchShop(t, &shopModel{})
	handle := eng.Handle()
	require.Positive(t, handle.BrowserPID)
	process, err := os.FindProcess(handle.BrowserPID)
	require.NoError(t, err)
	require.NoError(t, process.Kill())
	require.Eventually(t, func() bool {
		return errors.Is(browserhost.Probe(context.Background(), handle.CDPURL), browserhost.ErrUnreachable)
	}, 10*time.Second, 200*time.Millisecond, "the browser stops answering")
}

// dialogPage opens an alert, a confirm, and a prompt while it loads, and
// shows how the confirm and the prompt were answered.
const dialogPage = `<p id="result"></p><script>
alert("Welcome");
const result = document.getElementById("result");
result.textContent = (confirm("Continue?") ? "confirmed" : "cancelled") + "/" + prompt("Name?", "guest");
</script>`

// A page that opens dialogs keeps loading: every dialog is accepted, a
// prompt with its default text, and reported.
func TestStagehandAcceptsDialogs(t *testing.T) {
	t.Parallel()

	eng := launchShop(t, &shopModel{})
	require.NoError(t, eng.Goto(t.Context(), "data:text/html,"+url.PathEscape(dialogPage), 30*time.Second))

	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "confirmed/guest")
	assert.Equal(t, []dialog{
		{Type: "alert", Message: "Welcome"},
		{Type: "confirm", Message: "Continue?"},
		{Type: "prompt", Message: "Name?"},
	}, eng.TakeDialogs())
}

// blockedPage loads images and a script from hosts outside the allowed
// domains. As a data: page it has no host of its own to block.
const blockedPage = `<img src="http://cdn.blocked.test/logo.png">
<img src="http://cdn.blocked.test/banner.png">
<script src="http://sso.blocked.test/login.js"></script>`

// Requests allowed_domains blocks are counted by host.
func TestStagehandReportsBlockedRequests(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{AllowedDomains: []string{"example.com"}, Generate: (&shopModel{}).generate})
	require.NoError(t, eng.Goto(t.Context(), "data:text/html,"+url.PathEscape(blockedPage), 30*time.Second))

	// The preload scanner can request a resource again, so counts are only
	// bounded below.
	blocked := map[string]int{}
	require.Eventually(t, func() bool {
		taken, err := eng.TakeBlockedRequests()
		assert.NoError(t, err)
		for host, count := range taken {
			blocked[host] += count
		}
		return blocked["cdn.blocked.test"] >= 2 && blocked["sso.blocked.test"] >= 1
	}, 10*time.Second, 100*time.Millisecond, "blocked so far: %v", blocked)
	assert.Len(t, blocked, 2, "only blocked hosts are counted")
}

// A page whose script never yields fails a screenshot after the page call
// timeout instead of hanging the step.
func TestStagehandBoundsUnresponsivePage(t *testing.T) {
	t.Parallel()

	eng := launchShop(t, &shopModel{})
	const busyPage = `<p>busy</p><script>setTimeout(() => { for (;;) {} }, 200)</script>`
	require.NoError(t, eng.Goto(t.Context(), "data:text/html,"+url.PathEscape(busyPage), 30*time.Second))
	time.Sleep(time.Second)

	eng.(*stagehandEngine).pageCallTimeout = 2 * time.Second
	began := time.Now()
	_, err := eng.Screenshot(t.Context())
	require.ErrorContains(t, err, "the browser did not respond within 2s")
	assert.Less(t, time.Since(began), 10*time.Second)
}

// A runtime close that fails while the browser is still running is
// reported.
func TestCloseBrowserReportsRuntimeError(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")
	err := closeBrowser(t.Context(), os.Getpid(), func(context.Context) error { return closeErr })
	require.ErrorIs(t, err, closeErr)
}

// On Linux a browser that exits at launch is often one that cannot use its
// sandbox, so the error names the seccomp profile that lets it start and the
// host setting that turns it off.
func TestLaunchFailureSuggestsBrowserFlags(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		t.Skip("the hint applies to non-root Linux users")
	}
	// With CI set the launch is refused before the browser starts.
	t.Setenv("CI", "")

	_, err := stagehandLauncher{}.Launch(t.Context(), launchOptions{
		Executable:  "/bin/false",
		Headless:    true,
		UserDataDir: t.TempDir(),
	})
	require.ErrorContains(t, err, "seccomp-chromium.json")
	require.ErrorContains(t, err, "DAGU_BROWSER_SANDBOX=false")
}

// With the sandbox on, a launch is refused where the browser runtime would
// turn the sandbox off anyway, so a browser never runs without it silently.
func TestLaunchRefusesSilentSandboxOff(t *testing.T) {
	t.Setenv("CI", "true")

	_, err := stagehandLauncher{}.Launch(t.Context(), launchOptions{
		Executable:  chromePath(),
		Headless:    true,
		UserDataDir: t.TempDir(),
	})
	require.ErrorContains(t, err, "because CI is set")
	require.ErrorContains(t, err, "DAGU_BROWSER_SANDBOX=false")
}
