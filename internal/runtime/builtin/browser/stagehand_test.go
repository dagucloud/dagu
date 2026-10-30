// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	stagehand "github.com/browserbase/stagehand/packages/sdk-go/v4"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/test/chrometest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive a real Chrome through the Stagehand SDK with a scripted
// model. They pin the SDK behavior the executor depends on: extraction with
// a schema chosen at run time, replay of recorded actions without a model
// call, and reattaching to a browser left running by an exited process.

const detachHelperEnv = "DAGU_BROWSER_DETACH_HELPER"

// sleeperHelperEnv selects the role of TestSleeperHelper in a child process.
const sleeperHelperEnv = "DAGU_BROWSER_SLEEPER_HELPER"

const shopPage = `<!doctype html><html><head><title>Shop</title></head><body>
<h1>Widget Shop</h1>
<ul><li>Alpha - $3.50</li><li>Beta - $7.25</li></ul>
<p id="status">open</p>
<button onclick="document.getElementById('status').textContent='clicked'">Submit</button>
</body></html>`

// testBrowserDir keeps what test browsers learn about the executable, so
// only the first headless launch of a test process learns its user agent.
var testBrowserDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dagu-browser-state-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testBrowserDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// elementIDPattern finds a node ID in the accessibility tree the runtime
// sends to the model, such as "[0-23] button: Submit".
var elementIDPattern = regexp.MustCompile(`\[(\d+-\d+)\] button: Submit`)

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
	chrometest.Require(t)
	ctx := t.Context()
	opts.Executable = cmp.Or(opts.Executable, chrometest.Path())
	opts.Headless = true
	if opts.UserDataDir == "" {
		opts.UserDataDir = browserProfileDir(t)
	}
	opts.NoSandbox = true
	opts.BrowserDir = cmp.Or(opts.BrowserDir, testBrowserDir)
	var eng engine
	err := withStartupSlot(func() (err error) {
		eng, err = stagehandLauncher{}.Launch(ctx, opts)
		return err
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = eng.Close(context.WithoutCancel(ctx)) })
	return eng
}

// startupSlot lets one test browser start at a time. The runtime gives its
// extension a fixed minute to start, which parallel Chrome startups on a
// loaded CI host can exceed together.
var startupSlot = make(chan struct{}, 1)

// withStartupSlot runs start, which launches or wakes a browser runtime,
// while holding startupSlot.
func withStartupSlot(start func() error) error {
	startupSlot <- struct{}{}
	defer func() { <-startupSlot }()
	return start()
}

// browserProfileDir returns a new browser profile directory. Chrome keeps
// profile files open for a moment after it exits, so the directory is
// removed with retries instead of by t.TempDir. Call it before launching the
// browser, so the removal runs after the browser is closed.
func browserProfileDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dagu-browser-test-")
	require.NoError(t, err)
	t.Cleanup(func() {
		var removeErr error
		removed := assert.Eventually(t, func() bool {
			removeErr = os.RemoveAll(dir)
			return removeErr == nil
		}, 10*time.Second, 200*time.Millisecond, "remove the browser profile")
		if !removed {
			t.Logf("last removal error: %v\n%s", removeErr, profileHolders(dir))
		}
	})
	return dir
}

// TestSleeperHelper runs in a child process and waits to be ended. As
// "tree" it first starts a "sleeper" child and prints its process ID, so a
// test gets a process tree whose child outlives its parent.
func TestSleeperHelper(t *testing.T) {
	role := os.Getenv(sleeperHelperEnv)
	if role == "" {
		t.Skip("helper process")
	}
	if role == "tree" {
		child := exec.Command(os.Args[0], "-test.run=^TestSleeperHelper$")
		child.Env = append(os.Environ(), sleeperHelperEnv+"=sleeper")
		require.NoError(t, child.Start())
		fmt.Printf("SLEEPER %d\n", child.Process.Pid)
	}
	time.Sleep(time.Minute)
}

// startSleeper starts a process that stands in for a running browser and
// ends it when the test ends.
func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSleeperHelper$")
	cmd.Env = append(os.Environ(), sleeperHelperEnv+"=sleeper")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// startSleeperTree starts a process that stands in for a browser and a child
// of it that stands in for a helper, and returns the child's process ID.
func startSleeperTree(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSleeperHelper$")
	cmd.Env = append(os.Environ(), sleeperHelperEnv+"=tree")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	var helperPID int
	_, err = fmt.Fscanf(bufio.NewReader(stdout), "SLEEPER %d\n", &helperPID)
	require.NoError(t, err)
	t.Cleanup(func() {
		if helper, err := os.FindProcess(helperPID); err == nil {
			_ = helper.Kill()
		}
	})
	return cmd, helperPID
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

	outcome, err := eng.Act(t.Context(), "Click the Submit button", nil, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	require.NotEmpty(t, outcome.Actions)

	requests := model.requestCount()
	for _, action := range outcome.Actions {
		replayed, err := eng.Replay(t.Context(), action, nil, time.Minute)
		require.NoError(t, err)
		assert.True(t, replayed)
	}
	assert.Equal(t, requests, model.requestCount(), "replay makes no model call")
}

// A recorded element still on the page but hidden, such as a field in a
// closed dialog, fails the replay. The runtime would otherwise type into it
// and report success.
func TestStagehandReplayHiddenElement(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	require.NoError(t, eng.Goto(t.Context(), `data:text/html,<dialog><input></dialog><input>`, time.Minute))
	typeInto := func(selector string) recordedAction {
		return recordedAction{Selector: selector, Method: "type", Arguments: []string{"10"}}
	}

	replayed, err := eng.Replay(t.Context(), typeInto("xpath=/html/body/dialog[1]/input[1]"), nil, time.Minute)
	require.NoError(t, err)
	assert.False(t, replayed, "the field in the closed dialog is hidden")

	replayed, err = eng.Replay(t.Context(), typeInto("xpath=/html/body/input[1]"), nil, time.Minute)
	require.NoError(t, err)
	assert.True(t, replayed, "the field outside the dialog is visible")
}

// treeModel answers act requests with method and arguments on the element
// find finds in the tree the runtime sends.
func treeModel(find func(roots []*outlineNode) *outlineNode, method string, arguments ...string) generateFunc {
	return func(_ context.Context, req generateRequest) (generateResponse, error) {
		if !strings.Contains(string(req.Schema), "elementId") {
			return generateResponse{JSON: json.RawMessage(`{"completed":true,"progress":"done"}`)}, nil
		}
		text := ""
		for _, message := range req.Messages {
			text += message.Text + "\n"
		}
		node := find(parseSnapshotTree(text))
		if node == nil {
			return generateResponse{}, fmt.Errorf("no element to act on in:\n%s", text)
		}
		action, err := json.Marshal(map[string]any{
			"elementId": node.id, "description": node.role, "method": method, "arguments": append([]string{}, arguments...),
		})
		if err != nil {
			return generateResponse{}, err
		}
		return generateResponse{JSON: json.RawMessage(`{"action":` + string(action) + `,"twoStep":false}`)}, nil
	}
}

// where finds the first node pick accepts.
func where(pick func(*outlineNode) bool) func([]*outlineNode) *outlineNode {
	return func(roots []*outlineNode) *outlineNode { return nodeWhere(roots, pick) }
}

func nodeWhere(nodes []*outlineNode, pick func(*outlineNode) bool) *outlineNode {
	for _, n := range nodes {
		if pick(n) {
			return n
		}
		if found := nodeWhere(n.children, pick); found != nil {
			return found
		}
	}
	return nil
}

func buttonNamed(name string) func(*outlineNode) bool {
	return func(n *outlineNode) bool { return n.role == "button" && n.name == name }
}

func selectOffering(option string) func(*outlineNode) bool {
	return func(n *outlineNode) bool {
		return slices.Contains(choiceRoles, n.role) && slices.ContainsFunc(optionsOf(n), func(o *outlineNode) bool { return o.name == option })
	}
}

// statusPage is a page whose elements report what was done to them in its
// status line, which starts as "idle".
func statusPage(body string) string {
	return "data:text/html;charset=utf-8," + url.PathEscape(`<p id="status">idle</p>`+body+
		`<script>function report(s) { document.getElementById("status").textContent = s; }</script>`)
}

func requireStatus(t *testing.T, eng engine, want string) {
	t.Helper()
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	require.Equal(t, want, strings.SplitN(text, "\n", 2)[0])
}

// savePage has a Save button. A banner puts a Dismiss button before it, at
// the location Save had.
func savePage(banner bool) string {
	dismiss := ""
	if banner {
		dismiss = `<button onclick="report('dismissed')">Dismiss</button>`
	}
	return statusPage(`<div>` + dismiss + `<button onclick="report('saved')">Save</button></div>`)
}

// A replay acts only on the element it recorded. When the layout moves
// another element to the recorded location, the replay misses instead of
// acting on it.
func TestStagehandReplayMissesShiftedElement(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: treeModel(where(buttonNamed("Save")), "click")})
	require.NoError(t, eng.Goto(t.Context(), savePage(false), time.Minute))
	outcome, err := eng.Act(t.Context(), "Click Save", nil, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	require.Len(t, outcome.Actions, 1)
	save := outcome.Actions[0]

	require.NoError(t, eng.Goto(t.Context(), savePage(true), time.Minute))
	replayed, err := eng.Replay(t.Context(), save, nil, time.Minute)
	require.NoError(t, err)
	assert.False(t, replayed, "Dismiss is where Save was")
	requireStatus(t, eng, "idle")

	require.NoError(t, eng.Goto(t.Context(), savePage(false), time.Minute))
	replayed, err = eng.Replay(t.Context(), save, nil, time.Minute)
	require.NoError(t, err)
	assert.True(t, replayed)
	requireStatus(t, eng, "saved")
}

// priceForm has unlabeled lower and upper price selects. A banner puts a
// sort select before them, so the upper price's location holds the lower.
func priceForm(banner bool) string {
	sort := ""
	if banner {
		sort = `<select onchange="report(this.value)"><option>新着順</option><option>価格順</option></select>`
	}
	return statusPage(`<div>` + sort +
		`<select onchange="report(this.value)"><option>下限なし</option><option>7000万円以上</option></select>` +
		`<select onchange="report(this.value)"><option>上限なし</option><option>7000万円未満</option></select></div>`)
}

// A replayed select acts only on a select offering the recorded option. The
// runtime would otherwise reset another select at the recorded location to
// its first option and report success.
func TestStagehandReplayMissesShiftedSelect(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: treeModel(where(selectOffering("7000万円未満")), "selectOptionFromDropdown", "7000万円未満")})
	require.NoError(t, eng.Goto(t.Context(), priceForm(false), time.Minute))
	outcome, err := eng.Act(t.Context(), "Choose 7000万円未満 as the upper price", nil, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	require.Len(t, outcome.Actions, 1)
	upper := outcome.Actions[0]

	require.NoError(t, eng.Goto(t.Context(), priceForm(true), time.Minute))
	replayed, err := eng.Replay(t.Context(), upper, nil, time.Minute)
	require.NoError(t, err)
	assert.False(t, replayed, "the lower price select is where the upper was")
	requireStatus(t, eng, "idle")

	require.NoError(t, eng.Goto(t.Context(), statusPage(`<div><select><option>下限なし</option></select><select onchange="report(this.value)"></select></div>`), time.Minute))
	replayed, err = eng.Replay(t.Context(), upper, nil, time.Minute)
	require.NoError(t, err)
	assert.False(t, replayed, "the upper price select has no options yet")

	require.NoError(t, eng.Goto(t.Context(), priceForm(false), time.Minute))
	replayed, err = eng.Replay(t.Context(), upper, nil, time.Minute)
	require.NoError(t, err)
	assert.True(t, replayed)
	requireStatus(t, eng, "7000万円未満")
}

// deleteList has a Delete button in each row.
func deleteList(rows ...string) string {
	body := "<ul>"
	for _, row := range rows {
		body += fmt.Sprintf(`<li>%[1]s <button onclick="report('deleted %[1]s')">Delete</button></li>`, row)
	}
	return statusPage(body + "</ul>")
}

// A replayed action on one of several lookalike elements, such as the Delete
// button of a row, acts only on the one in the row it recorded.
func TestStagehandReplayMissesShiftedRow(t *testing.T) {
	t.Parallel()

	betaDelete := func(roots []*outlineNode) *outlineNode {
		row := nodeWhere(roots, func(n *outlineNode) bool { return n.role == "listitem" && strings.Contains(textOf(n), "Beta") })
		if row == nil {
			return nil
		}
		return nodeWhere(row.children, buttonNamed("Delete"))
	}
	eng := launchBrowser(t, launchOptions{Generate: treeModel(betaDelete, "click")})
	require.NoError(t, eng.Goto(t.Context(), deleteList("Alpha", "Beta"), time.Minute))
	outcome, err := eng.Act(t.Context(), "Delete Beta", nil, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	require.Len(t, outcome.Actions, 1)
	deleteBeta := outcome.Actions[0]

	require.NoError(t, eng.Goto(t.Context(), deleteList("Gamma", "Alpha", "Beta"), time.Minute))
	replayed, err := eng.Replay(t.Context(), deleteBeta, nil, time.Minute)
	require.NoError(t, err)
	assert.False(t, replayed, "Alpha's Delete is where Beta's was")
	requireStatus(t, eng, "idle")

	require.NoError(t, eng.Goto(t.Context(), deleteList("Alpha", "Beta"), time.Minute))
	replayed, err = eng.Replay(t.Context(), deleteBeta, nil, time.Minute)
	require.NoError(t, err)
	assert.True(t, replayed)
	requireStatus(t, eng, "deleted Beta")

	// A Delete alone in its list when recorded has no row to be told apart
	// by, so a row added before it makes the replay miss.
	require.NoError(t, eng.Goto(t.Context(), deleteList("Beta"), time.Minute))
	outcome, err = eng.Act(t.Context(), "Delete Beta", nil, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	require.NoError(t, eng.Goto(t.Context(), deleteList("Alpha", "Beta"), time.Minute))
	replayed, err = eng.Replay(t.Context(), outcome.Actions[0], nil, time.Minute)
	require.NoError(t, err)
	assert.False(t, replayed, "Alpha's Delete is where the lone one was")
	requireStatus(t, eng, "idle")
}

// An act whose model picks an option the select does not offer does not
// complete, though the runtime reports success for it. Its message names
// the value the option's variable was given, unless that value is secret.
func TestStagehandActMissingOption(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: treeModel(where(selectOffering("7000万円未満")), "selectOptionFromDropdown", "%upper%")})
	require.NoError(t, eng.Goto(t.Context(), priceForm(false), time.Minute))
	upper := map[string]string{"upper": "8000万円未満"}
	outcome, err := eng.Act(t.Context(), "Choose %upper% as the upper price", upper, func(string) bool { return false }, time.Minute)
	require.NoError(t, err)
	assert.False(t, outcome.Success)
	assert.Equal(t, `the select offers no option "8000万円未満", the value of "%upper%"; it offers "上限なし", "7000万円未満"`, outcome.Message)

	outcome, err = eng.Act(t.Context(), "Choose %upper% as the upper price", upper, func(string) bool { return true }, time.Minute)
	require.NoError(t, err)
	assert.False(t, outcome.Success)
	assert.NotContains(t, outcome.Message, "8000万円未満")
}

// servePerPage serves a list whose results-per-page select reloads the page
// when an option is chosen. The reloaded list shows its results a moment
// after its first bytes. The change handler holds the page's script for a
// moment after starting the reload, so the new document replaces the select
// before the runtime is done with it, as on a page that answers fast.
func servePerPage(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<select onchange="location.search = '?per_page=' + this.value;
const end = Date.now() + 300; while (Date.now() < end) {}">
<option value="20">20</option><option value="100">100</option></select>`)
		if perPage := r.URL.Query().Get("per_page"); perPage != "" {
			w.(http.Flusher).Flush()
			time.Sleep(time.Second)
			_, _ = fmt.Fprintf(w, "<p>Showing %s results</p>", perPage)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// An act that chooses an option which reloads the page completes, and the
// next operation reads the reloaded page.
func TestStagehandActSelectThatReloads(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: treeModel(where(selectOffering("100")), selectOptionMethod, "100")})
	require.NoError(t, eng.Goto(t.Context(), servePerPage(t), time.Minute))
	document, err := eng.DocumentID(t.Context())
	require.NoError(t, err)

	op := &operator{eng: eng, warn: func(_ int, message string) { t.Log(message) }}
	outcome, err := op.performAct(t.Context(), 0, "Show 100 results per page", document, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "Showing 100 results")
}

// servePager serves a page that draws its list from a request it makes on
// load and again for each press of Next, as pages paged by buttons do. Each
// answer sends its headers at once and its body a moment later.
func servePager(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rows" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			time.Sleep(800 * time.Millisecond)
			_, _ = fmt.Fprintf(w, "Row of page %s", r.URL.Query().Get("page"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<p id="rows">Loading</p><button id="next">Next</button><script>
let page = 1;
function load() { fetch('/rows?page=' + page).then(r => r.text()).then(t => { document.getElementById('rows').textContent = t; }); }
document.getElementById('next').onclick = () => { page++; load(); };
load();
</script>`)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// A page that draws what it fetches shows it once going there or pressing a
// button returns, so the next operation reads it rather than what came
// before, even when the press is replayed without a model call.
func TestStagehandWaitsForWhatThePageFetches(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	require.NoError(t, eng.Goto(t.Context(), servePager(t), time.Minute))
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "Row of page 1", "the list the page fetched on load")

	replayed, err := eng.Replay(t.Context(), recordedAction{Selector: "xpath=/html/body/button[1]", Method: "click"}, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, replayed)
	text, err = eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "Row of page 2", "the list the press fetched")
}

// serveSignIn serves a page whose Sign in button sends a request and then
// moves to a home page, as signing in does. The home page draws its greeting
// from a request it makes after loading.
func serveSignIn(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			time.Sleep(300 * time.Millisecond)
		case "/greeting":
			time.Sleep(800 * time.Millisecond)
			_, _ = io.WriteString(w, "Welcome back")
		case "/home":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<p id="greeting">Loading</p><script>
fetch('/greeting').then(r => r.text()).then(t => { document.getElementById('greeting').textContent = t; });
</script>`)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<button id="sign-in">Sign in</button><script>
document.getElementById('sign-in').onclick = () => fetch('/session', {method: 'POST'}).then(() => location.assign('/home'));
</script>`)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// A press that leaves for another page only once its own request answers
// waits for that page as going there does, so the next operation reads what
// the new page fetches.
func TestStagehandWaitsForThePageAPressOpens(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	require.NoError(t, eng.Goto(t.Context(), serveSignIn(t), time.Minute))

	replayed, err := eng.Replay(t.Context(), recordedAction{Selector: "xpath=/html/body/button[1]", Method: "click"}, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, replayed)
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "Welcome back", "the greeting the home page fetched")
}

// menuPage adds an Approve button once its menu is opened.
const menuPage = `<p id="status">pending</p>
<button onclick="openMenu()">Open menu</button>
<script>
function openMenu() {
	const approve = document.createElement("button");
	approve.textContent = "Approve";
	approve.onclick = () => { document.getElementById("status").textContent = "approved"; };
	document.body.append(approve);
}
</script>`

var menuButtonPattern = regexp.MustCompile(`\[(\d+-\d+)\] button: (Open menu|Approve)`)

// An act performs one action even when the model asks to follow it with
// another, which the runtime would otherwise ask for and perform as well.
func TestStagehandActPerformsOneAction(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	// The model clicks Approve once the page shows it, and Open menu before,
	// and always asks for a second action.
	model := func(_ context.Context, req generateRequest) (generateResponse, error) {
		requests.Add(1)
		text := ""
		for _, message := range req.Messages {
			text += message.Text
		}
		buttons := map[string]string{}
		for _, match := range menuButtonPattern.FindAllStringSubmatch(text, -1) {
			buttons[match[2]] = match[1]
		}
		id, ok := buttons["Approve"]
		if !ok {
			id = buttons["Open menu"]
		}
		answer := fmt.Sprintf(`{"action":{"elementId":%q,"description":"menu button","method":"click","arguments":[]},"twoStep":true}`, id)
		return generateResponse{JSON: json.RawMessage(answer)}, nil
	}
	eng := launchBrowser(t, launchOptions{Generate: model})
	require.NoError(t, eng.Goto(t.Context(), "data:text/html,"+url.PathEscape(menuPage), 30*time.Second))

	outcome, err := eng.Act(t.Context(), "Open the menu", nil, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	assert.Len(t, outcome.Actions, 1)
	assert.EqualValues(t, 1, requests.Load(), "no model request for a second action")
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "pending", "Approve is not clicked")
}

// The runtime reports a lost page, a detached session or a script context a
// new document replaced, in a failed act result when the action failed, and
// as an RPC error when the work around it did. Other failures, including the
// SDK's own connection errors, are not a lost page.
func TestSessionLost(t *testing.T) {
	t.Parallel()

	failed := func(message string) stagehand.ActResult {
		return stagehand.ActResult{Data: stagehand.ActResultData{Message: message}}
	}
	for name, tc := range map[string]struct {
		result  stagehand.ActResult
		err     error
		lost    bool
		context bool
	}{
		"failed action": {result: failed("Failed to perform act: -32001 Session with given id not found."), lost: true},
		"replaced context": {
			result: failed("Failed to perform act: -32000 Cannot find context with specified id"),
			lost:   true, context: true,
		},
		"rpc error": {
			err:  fmt.Errorf("act: %w", &stagehand.RPCError{Code: -32603, Message: "No Page found for target closed before CDP response (sessionId=s1, targetId=t1)"}),
			lost: true,
		},
		"no element":      {result: failed("Failed to perform act: No action found")},
		"other rpc error": {err: &stagehand.RPCError{Code: -32603, Message: "Element not visible (no box model)"}},
		"sdk error":       {err: errors.New("-32001 Session with given id not found.")},
	} {
		lost := sessionLost(tc.result, tc.err)
		assert.Equal(t, tc.lost, errors.Is(lost, errPageSessionLost), name)
		assert.Equal(t, tc.context, errors.Is(lost, errPageContextLost), name)
	}
}

// TestStagehandDetachHelper runs in a child process: it opens a page, leaves
// the browser running, prints the handle, and exits.
func TestStagehandDetachHelper(t *testing.T) {
	if os.Getenv(detachHelperEnv) == "" {
		t.Skip("helper process")
	}
	eng, err := stagehandLauncher{}.Launch(t.Context(), launchOptions{
		Executable:  chrometest.Path(),
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
	chrometest.Require(t)

	userDataDir := browserProfileDir(t)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStagehandDetachHelper$", "-test.v")
	cmd.Env = append(os.Environ(), detachHelperEnv+"="+userDataDir)
	var output []byte
	err := withStartupSlot(func() (err error) {
		output, err = cmd.CombinedOutput()
		return err
	})
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
	var eng engine
	err = withStartupSlot(func() (err error) {
		eng, err = stagehandLauncher{}.Reattach(t.Context(), handle, launchOptions{Generate: model.generate})
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, handle.BrowserPID, eng.Handle().BrowserPID, "the reattached browser keeps its process")
	assert.Equal(t, handle.BrowserStartedAt, eng.Handle().BrowserStartedAt)
	pageURL, err := eng.CurrentURL(t.Context())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(pageURL, "data:text/html"), pageURL)

	outcome, err := eng.Act(t.Context(), "Click the Submit button", nil, nil, time.Minute)
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
	chrometest.Require(t)

	downloads := t.TempDir()
	eng := launchBrowser(t, launchOptions{DownloadsDir: downloads, Generate: reportModel})
	require.NoError(t, eng.Goto(t.Context(), serveReport(t), time.Minute))

	outcome, err := eng.Act(t.Context(), "Click the Download report link", nil, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)

	names, err := eng.WaitForDownloads(t.Context(), 3*time.Second, time.Minute)
	require.NoError(t, err)
	require.Equal(t, []string{"report.csv"}, names)
	data, err := os.ReadFile(filepath.Join(downloads, "report.csv"))
	require.NoError(t, err)
	assert.Equal(t, reportBody, string(data))
}

// serveLoads serves a page that names its path, the report as a download,
// an answer with no content, and an answer that closes the connection
// unanswered, for which the browser shows its error page.
func serveLoads(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>Loads</title><p>page "+r.URL.Path+"</p>")
	})
	mux.HandleFunc("/report.csv", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
		_, _ = io.WriteString(w, reportBody)
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/unanswered", func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

// A goto ends when its load does. One that brings no document, a download
// or an answer with no content, returns as soon as the browser reports it
// and leaves the page where it was; the download is saved. One that brings
// a document returns once it is parsed, the browser's error page for an
// address that cannot be reached included.
func TestStagehandGotoEndsWithItsLoad(t *testing.T) {
	t.Parallel()

	downloads := t.TempDir()
	eng := launchBrowser(t, launchOptions{DownloadsDir: downloads, Generate: reportModel})
	site := serveLoads(t)
	// A load that never reports would wait out this timeout and fail.
	const timeout = 30 * time.Second
	require.NoError(t, eng.Goto(t.Context(), site+"/start", timeout))

	for path, want := range map[string][]string{"/report.csv": {"report.csv"}, "/empty": {}} {
		began := time.Now()
		require.NoError(t, eng.Goto(t.Context(), site+path, timeout), path)
		assert.Less(t, time.Since(began), timeout/2, path)
		current, err := eng.CurrentURL(t.Context())
		require.NoError(t, err)
		assert.Equal(t, site+"/start", current, "%s leaves the page where it was", path)
		// Without a grace, only a download already seen to begin is waited
		// for, as a session waits after a goto.
		names, err := eng.WaitForDownloads(t.Context(), 0, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, want, names, path)
	}

	for target, code := range map[string]string{
		"http://dagu-test.invalid/": "ERR_NAME_NOT_RESOLVED",
		site + "/unanswered":        "ERR_EMPTY_RESPONSE",
	} {
		require.NoError(t, eng.Goto(t.Context(), target, timeout), target)
		page, err := eng.PageOutcome(t.Context())
		require.NoError(t, err)
		assert.Equal(t, code, page.Code, target)
	}

	require.NoError(t, eng.Goto(t.Context(), site+"/next", timeout))
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "page /next")
	// A move to a fragment keeps the document.
	require.NoError(t, eng.Goto(t.Context(), site+"/next#part", timeout))
	current, err := eng.CurrentURL(t.Context())
	require.NoError(t, err)
	assert.Equal(t, site+"/next#part", current)
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

// A document keeps its ID while it stays loaded, and loading the same URL
// again gives a new ID, as a form that posts back to its own page does.
func TestStagehandDocumentID(t *testing.T) {
	t.Parallel()

	eng := launchShop(t, &shopModel{})
	first, err := eng.DocumentID(t.Context())
	require.NoError(t, err)
	again, err := eng.DocumentID(t.Context())
	require.NoError(t, err)
	assert.Equal(t, first, again)

	pageURL, err := eng.CurrentURL(t.Context())
	require.NoError(t, err)
	require.NoError(t, eng.Goto(t.Context(), pageURL, time.Minute))
	reloaded, err := eng.DocumentID(t.Context())
	require.NoError(t, err)
	assert.NotEqual(t, first, reloaded)
}

// The process ID a launched browser reports is the browser itself: ending it
// closes the browser. Ending any other process would leave the browser
// answering. A browser that was ended refuses connections, or, on Windows
// while the process is still being torn down, accepts them without
// answering, so a refusal or a timeout counts; any reply does not.
func TestStagehandReportsBrowserProcess(t *testing.T) {
	t.Parallel()

	eng := launchShop(t, &shopModel{})
	handle := eng.Handle()
	require.Positive(t, handle.BrowserPID)
	process, err := os.FindProcess(handle.BrowserPID)
	require.NoError(t, err)
	require.NoError(t, process.Kill())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		err := browserhost.Probe(context.Background(), handle.CDPURL)
		var netErr net.Error
		stopped := errors.Is(err, browserhost.ErrUnreachable) || (errors.As(err, &netErr) && netErr.Timeout())
		assert.True(c, stopped, "the browser stops answering; last probe: %v", err)
	}, 10*time.Second, 200*time.Millisecond)
}

// A sign-in a browser makes stays in its profile when the browser closes,
// so the next browser on the profile is still signed in.
func TestStagehandCloseKeepsProfileCookies(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "member", Path: "/", MaxAge: 3600, HttpOnly: true})
			_, _ = io.WriteString(w, "<p>Signed in</p>")
			return
		}
		if cookie, err := r.Cookie("sid"); err == nil && cookie.Value == "member" {
			_, _ = io.WriteString(w, "<p>Welcome back</p>")
			return
		}
		_, _ = io.WriteString(w, "<p>Please sign in</p>")
	}))
	t.Cleanup(server.Close)
	ctx := t.Context()
	profile := browserProfileDir(t)

	first := launchBrowser(t, launchOptions{UserDataDir: profile})
	require.NoError(t, first.Goto(ctx, server.URL+"/login", time.Minute))
	require.NoError(t, first.Close(context.WithoutCancel(ctx)))

	second := launchBrowser(t, launchOptions{UserDataDir: profile})
	require.NoError(t, second.Goto(ctx, server.URL+"/", time.Minute))
	text, err := second.PageText(ctx)
	require.NoError(t, err)
	require.Contains(t, text, "Welcome back", "the profile keeps the session cookie the first browser received")
}

// A browser that stops answering DevTools does not hold up its close: the
// request to exit gives up, so the runtime can end the browser.
func TestAskToExitGivesUpOnASilentBrowser(t *testing.T) {
	t.Parallel()

	started := time.Now()
	err := askToExit(context.Background(), serveSilentBrowser(t))
	require.ErrorIs(t, err, errBrowserUnresponsive)
	require.Less(t, time.Since(started), exitRequestTimeout+10*time.Second)
}

// A browser asked to exit stops answering DevTools before it has finished
// writing its profile. Exiting a launched browser returns only once its
// process has gone, so the runtime does not end it partway through.
func TestExitLaunchedBrowserWaitsForProcess(t *testing.T) {
	t.Parallel()

	browser := startSleeper(t)
	reaped := make(chan struct{})
	go func() {
		_ = browser.Wait()
		close(reaped)
	}()
	var finished atomic.Bool
	cdpURL := serveExitingBrowser(t, func() {
		// The browser takes a while to write its profile before it exits.
		time.Sleep(500 * time.Millisecond)
		finished.Store(true)
		_ = browser.Process.Kill()
	})
	startedAt, _ := procutil.StartTime(browser.Process.Pid)

	exitLaunchedBrowser(t.Context(), browserHandle{
		CDPURL:           cdpURL,
		BrowserPID:       browser.Process.Pid,
		BrowserStartedAt: startedAt,
	})
	assert.True(t, finished.Load(), "returned while the browser was still exiting")
	<-reaped
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
	require.NoError(t, eng.Goto(t.Context(), "data:text/html,"+url.PathEscape("<p>busy</p>"), 30*time.Second))
	// The page starts spinning only once the navigation has returned, so a
	// slow host cannot make the navigation itself wait on the busy page.
	busy := eng.(*stagehandEngine)
	_, err := busy.evaluate(t.Context(), "setTimeout(() => { for (;;) {} }, 0)")
	require.NoError(t, err)

	busy.pageCallTimeout = 2 * time.Second
	began := time.Now()
	_, err = eng.Screenshot(t.Context())
	require.ErrorContains(t, err, "the browser did not respond within 2s")
	assert.Less(t, time.Since(began), 10*time.Second)
}

// A runtime close that fails while the browser is still running is
// reported. The browser's start time is unknown here, so closing relies on
// the runtime alone.
func TestCloseBrowserReportsRuntimeError(t *testing.T) {
	t.Parallel()

	browser := startSleeper(t)
	closeErr := errors.New("close failed")
	err := closeBrowser(t.Context(), browser.Process.Pid, 0, func(context.Context) error { return closeErr })
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
		Executable:  chrometest.Path(),
		Headless:    true,
		UserDataDir: t.TempDir(),
	})
	require.ErrorContains(t, err, "because CI is set")
	require.ErrorContains(t, err, "DAGU_BROWSER_SANDBOX=false")
}

// A browser that starts but whose runtime cannot start in it is ended by the
// failed launch, so it does not keep running with its profile open. On
// Windows, removing a profile that a running browser holds stalls cleanup.
func TestStagehandFailedLaunchEndsBrowser(t *testing.T) {
	t.Parallel()
	chrometest.Require(t)
	if runtime.GOOS == "windows" {
		t.Skip("the browser wrapper is a shell script")
	}
	if testing.Short() {
		t.Skip("waits out the runtime's fixed startup minute")
	}
	// The wrapper records the browser's arguments and disables extensions,
	// so the runtime extension never starts.
	wrapper := filepath.Join(t.TempDir(), "chrome")
	chrome := "'" + strings.ReplaceAll(chrometest.Path(), "'", `'\''`) + "'"
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > \"$0.args\"\nexec %s \"$@\" --disable-extensions\n", chrome)
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o700))

	// A kept user agent spares the probe, which would wait out the same
	// minute first.
	browserDir := t.TempDir()
	require.NoError(t, keepUserAgent(browserDir, headedUserAgent{Executable: wrapper, Product: "Chrome/1.0.0.0", UserAgent: "Mozilla/5.0 Chrome/1.0.0.0"}))
	err := withStartupSlot(func() error {
		_, err := stagehandLauncher{}.Launch(t.Context(), launchOptions{
			Executable:  wrapper,
			Headless:    true,
			UserDataDir: browserProfileDir(t),
			NoSandbox:   true,
			BrowserDir:  browserDir,
			Generate:    (&shopModel{}).generate,
		})
		return err
	})
	require.Error(t, err)

	args, err := os.ReadFile(wrapper + ".args")
	require.NoError(t, err, "the browser started")
	port := regexp.MustCompile(`--remote-debugging-port=(\d+)`).FindSubmatch(args)
	require.NotNil(t, port, string(args))
	require.Eventually(t, func() bool {
		return errors.Is(browserhost.Probe(context.Background(), "http://127.0.0.1:"+string(port[1])), browserhost.ErrUnreachable)
	}, 10*time.Second, 200*time.Millisecond, "the failed launch ends the browser")
}

// serveUserAgent serves a page showing the user agent its request came
// with.
func serveUserAgent(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, r.UserAgent())
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// pageUserAgent reads the user agent a page's request came with, and the
// one its scripts see, with the brands it reports.
func pageUserAgent(t *testing.T, eng engine, pageURL string) (header, script string, brands []string) {
	t.Helper()
	require.NoError(t, eng.Goto(t.Context(), pageURL, time.Minute))
	header, err := eng.PageText(t.Context())
	require.NoError(t, err)
	raw, err := eng.(*stagehandEngine).evaluate(t.Context(), `({agent: navigator.userAgent, brands: navigator.userAgentData.brands.map((b) => b.brand)})`)
	require.NoError(t, err)
	var seen struct {
		Agent  string   `json:"agent"`
		Brands []string `json:"brands"`
	}
	require.NoError(t, json.Unmarshal(raw, &seen))
	return strings.TrimSpace(header), seen.Agent, seen.Brands
}

// A browser without a window sends the user agent it sends with one, so a
// site treats a step as it treats a person.
func TestStagehandLooksHeaded(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	header, script, brands := pageUserAgent(t, eng, serveUserAgent(t))
	assert.Equal(t, header, script, "the request and the page's scripts see the same user agent")
	assert.Contains(t, header, headedProduct)
	assert.NotContains(t, header, "Headless")
	assert.NotContains(t, brands, "HeadlessChrome")
}

// A browser without a window reports a desktop's screen, one that holds its
// window, so a page that sizes itself by the screen lays out as it does for
// a person.
func TestStagehandScreenHoldsWindow(t *testing.T) {
	t.Parallel()

	for name, size := range map[string]*viewport{"default size": nil, "large viewport": {Width: 2400, Height: 1300}} {
		eng := launchBrowser(t, launchOptions{Viewport: size, Generate: (&shopModel{}).generate})
		require.NoError(t, eng.Goto(t.Context(), "data:text/html,<p>screen</p>", time.Minute))
		raw, err := eng.(*stagehandEngine).evaluate(t.Context(), `[screen.width, screen.height, outerWidth, outerHeight]`)
		require.NoError(t, err)
		var sizes [4]int
		require.NoError(t, json.Unmarshal(raw, &sizes), name)
		screenWidth, screenHeight, windowWidth, windowHeight := sizes[0], sizes[1], sizes[2], sizes[3]
		t.Logf("%s: screen %dx%d, window %dx%d", name, screenWidth, screenHeight, windowWidth, windowHeight)
		assert.GreaterOrEqual(t, screenWidth, max(windowWidth, headlessScreenWidth), name)
		assert.GreaterOrEqual(t, screenHeight, max(windowHeight, headlessScreenHeight), name)
	}
}

// loggingWrapper returns an executable that runs Chrome and logs each start
// to its .log file: the user agent the start was given, or none. With
// failProbe, a start on a probe's profile exits at once and logs probe.
func loggingWrapper(t *testing.T, failProbe bool) string {
	t.Helper()
	chrometest.Require(t)
	if runtime.GOOS == "windows" {
		t.Skip("the browser wrapper is a shell script")
	}
	probe := ""
	if failProbe {
		probe = `--user-data-dir=*dagu-browser-probe-*) echo probe >> "$0.log"; exit 1;; `
	}
	wrapper := filepath.Join(t.TempDir(), "chrome")
	chrome := "'" + strings.ReplaceAll(chrometest.Path(), "'", `'\''`) + "'"
	script := fmt.Sprintf("#!/bin/sh\nagent=none\nfor arg in \"$@\"; do case \"$arg\" in %s--user-agent=*) agent=\"${arg#--user-agent=}\";; esac; done\n"+
		"printf '%%s\\n' \"$agent\" >> \"$0.log\"\nexec %s \"$@\"\n", probe, chrome)
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o700))
	return wrapper
}

// loggedStarts returns the starts wrapper logged after the first skip.
func loggedStarts(t *testing.T, wrapper string, skip int) []string {
	t.Helper()
	data, err := os.ReadFile(wrapper + ".log")
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(data)), "\n")[skip:]
}

// The user agent an executable sends with a window is learned once, by a
// browser started for it alone, and asked again only once the executable
// reports another version, starting the browser again only when the user
// agent changed with it.
func TestStagehandLearnsUserAgentOnce(t *testing.T) {
	t.Parallel()

	wrapper := loggingWrapper(t, false)
	browserDir := t.TempDir()
	pageURL := serveUserAgent(t)
	logged := 0
	// launch starts and closes a browser, and returns the user agents its
	// starts were given and the one its page's request came with.
	launch := func() ([]string, string) {
		t.Helper()
		eng := launchBrowser(t, launchOptions{Executable: wrapper, BrowserDir: browserDir, Generate: (&shopModel{}).generate})
		header, _, _ := pageUserAgent(t, eng, pageURL)
		require.NoError(t, eng.Close(t.Context()))
		starts := loggedStarts(t, wrapper, logged)
		logged += len(starts)
		return starts, header
	}

	starts, header := launch()
	require.Len(t, starts, 2, "a probe, then the browser")
	assert.Equal(t, "none", starts[0], "the probe sends its own user agent")
	assert.Equal(t, header, starts[1])
	assert.NotContains(t, header, "Headless")
	headed := header

	starts, _ = launch()
	assert.Equal(t, []string{headed}, starts, "the kept user agent needs no probe")

	kept, ok := loadUserAgent(browserDir, wrapper)
	require.True(t, ok)
	updated := kept
	updated.Product = "Chrome/1.0.0.0"
	require.NoError(t, keepUserAgent(browserDir, updated))
	starts, _ = launch()
	assert.Equal(t, []string{headed, "none"}, starts, "another version is probed, and the same user agent keeps the browser")

	updated.UserAgent = "Mozilla/5.0 Chrome/1.0.0.0"
	require.NoError(t, keepUserAgent(browserDir, updated))
	starts, header = launch()
	assert.Equal(t, []string{updated.UserAgent, "none", headed}, starts, "a changed user agent starts the browser again")
	assert.Equal(t, headed, header)
	kept, ok = loadUserAgent(browserDir, wrapper)
	require.True(t, ok)
	assert.Equal(t, headed, kept.UserAgent)
}

// A probe that cannot start the browser only warns: the browser starts, its
// own user agent tells the one it sends with a window, and it starts again
// sending that.
func TestStagehandLaunchesWhenTheProbeFails(t *testing.T) {
	t.Parallel()

	wrapper := loggingWrapper(t, true)
	var warnings []string
	eng := launchBrowser(t, launchOptions{
		Executable: wrapper, BrowserDir: t.TempDir(), Generate: (&shopModel{}).generate,
		Warn: func(message string) { warnings = append(warnings, message) },
	})
	header, _, _ := pageUserAgent(t, eng, serveUserAgent(t))
	assert.NotContains(t, header, "Headless")
	assert.Equal(t, []string{"probe", "none", header}, loggedStarts(t, wrapper, 0))
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "learn the user agent the browser sends with a window: start the browser")
}

// serveRefusals serves a page behind a bot check, answered as Cloudflare
// answers, and a forbidden page without a body.
func serveRefusals(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checked":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "<title>Just a moment...</title><p>Checking your browser</p>")
		case "/forbidden":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<p>Welcome</p>")
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// A page a site refused tells how: a bot check, or a refusal the browser
// shows its own error page for.
func TestStagehandTellsRefusedPages(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	// The check here never passes.
	eng.(*stagehandEngine).botCheckWait = time.Second
	site := serveRefusals(t)
	host := urlHost(site)
	for path, want := range map[string]refusal{
		"/checked":   {Host: host, Status: http.StatusForbidden, BotCheck: browserhost.BotCheckCloudflare},
		"/forbidden": {Host: host, Status: http.StatusForbidden},
	} {
		// A browser that shows its own error page for the answer reports
		// the navigation as failed.
		_ = eng.Goto(t.Context(), site+path, time.Minute)
		page, err := eng.PageOutcome(t.Context())
		require.NoError(t, err, path)
		refused, ok := refusalOf(page)
		require.True(t, ok, "%s: %+v", path, page)
		assert.Equal(t, want, refused, path)
	}
	require.NoError(t, eng.Goto(t.Context(), site, time.Minute))
	page, err := eng.PageOutcome(t.Context())
	require.NoError(t, err)
	_, refused := refusalOf(page)
	assert.False(t, refused, "%+v", page)
}

// A wait that ends while it reads the page leaves the browser answering: a
// read whose context had ended as it wrote to the browser runtime would
// close the runtime's connection to the browser.
func TestStagehandWaitEndingMidReadKeepsBrowser(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/checked" {
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "<title>Just a moment...</title><p>Checking your browser</p>")
			return
		}
		// A page that changes all the time never settles, and one that keeps
		// its main thread busy reads slowly, as on a loaded host.
		_, _ = io.WriteString(w, `<p id="clock"></p><script>setInterval(() => {
  const end = Date.now() + 30; while (Date.now() < end) {}
  clock.textContent = Date.now();
}, 40);</script>`)
	}))
	t.Cleanup(server.Close)
	for name, tc := range map[string]struct {
		path string
		// limit sets the wait's limit, and wait waits.
		limit func(*stagehandEngine, time.Duration)
		wait  func(*stagehandEngine, context.Context)
	}{
		"bot check": {
			path:  "/checked",
			limit: func(e *stagehandEngine, d time.Duration) { e.botCheckWait = d },
			wait:  (*stagehandEngine).awaitBotCheck,
		},
		"settle": {
			path:  "/changing",
			limit: func(e *stagehandEngine, d time.Duration) { e.settleWait = d },
			wait: func(e *stagehandEngine, ctx context.Context) {
				e.settleDocument(ctx, time.Now().Add(e.settleWait))
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
			e := eng.(*stagehandEngine)
			tc.limit(e, 0)
			require.NoError(t, eng.Goto(t.Context(), server.URL+tc.path, time.Minute))
			require.Equal(t, tc.path == "/checked", e.showsBotCheck(t.Context()))
			// Waits a few milliseconds apart end at every moment of a read.
			for i := range 80 {
				limit := time.Duration(i) * 3 * time.Millisecond
				tc.limit(e, limit)
				tc.wait(e, t.Context())
				_, err := e.DocumentID(t.Context())
				require.NoError(t, err, "after a wait of %s", limit)
			}
		})
	}
}

// serveCheckThatPasses serves a page behind a bot check that passes on its
// own a moment after it loads, as one that needs no person does: it marks
// the browser and loads the page again.
func serveCheckThatPasses(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if _, err := r.Cookie("passed"); err == nil {
			_, _ = io.WriteString(w, "<p>Welcome</p>")
			return
		}
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<title>Just a moment...</title><p>Checking your browser</p>
<script>setTimeout(() => { document.cookie = "passed=1; path=/"; location.reload(); }, 1000);</script>`)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// Going to a page behind a bot check that passes on its own returns once it
// passed, so the next operation reads the page and not the check.
func TestStagehandWaitsForBotCheckToPass(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	require.NoError(t, eng.Goto(t.Context(), serveCheckThatPasses(t), time.Minute))
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "Welcome")
	page, err := eng.PageOutcome(t.Context())
	require.NoError(t, err)
	_, refused := refusalOf(page)
	assert.False(t, refused, "%+v", page)
}

// A real page's snapshot renders as the outline the renderer is built for,
// so a change in the tree the browser runtime reports shows up here.
func TestStagehandSnapshotOutlinesThePage(t *testing.T) {
	t.Parallel()
	chrometest.Require(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<html><head><title>Sign in</title></head><body>
<h1>Portal</h1>
<form><label>Login ID <input name="id" value="typed-id"></label>
<label>Status <select><option>All</option><option selected>Open</option></select></label>
<button type="submit">Sign in</button></form>
<a href="/help">Help</a></body></html>`)
	}))
	t.Cleanup(server.Close)
	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	require.NoError(t, eng.Goto(t.Context(), server.URL+"/login", time.Minute))

	snap, err := eng.Snapshot(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Sign in", snap.Title)
	assert.Equal(t, server.URL+"/login", snap.URL)
	text, _, _ := renderOutline(snap, outlineOptions{})
	assert.Equal(t, `heading: Portal
form
  [] textbox "Login ID"
  [] select "Status" = Open; options: All, Open
  [] button "Sign in"
[] link "Help" -> /help`, regexp.MustCompile(`\[[^\]]+\]`).ReplaceAllString(text, "[]"))

	// The ID before an element finds it again: an element operation acts on
	// it by its XPath, as a replayed action does.
	selected, help, err := elementAct(snap, elementOperation{kind: opSelect, element: elementRef(t, text, `select "Status"`), text: "All"}, nil)
	require.NoError(t, err)
	assert.Equal(t, `Select "All" in the "Status" dropdown`, selected)
	done, err := eng.Replay(t.Context(), help, nil, time.Minute)
	require.NoError(t, err)
	assert.True(t, done, "the select takes the option")
	_, click, err := elementAct(snap, elementOperation{kind: opClick, element: elementRef(t, text, `link "Help"`)}, nil)
	require.NoError(t, err)
	done, err = eng.Replay(t.Context(), click, nil, time.Minute)
	require.NoError(t, err)
	assert.True(t, done)
	assert.Eventually(t, func() bool {
		current, err := eng.CurrentURL(t.Context())
		return err == nil && current == server.URL+"/help"
	}, 10*time.Second, 100*time.Millisecond, "clicking the link by its ID follows it")
}

// elementRef is the ID the outline shows before the line holding what.
func elementRef(t *testing.T, outline, what string) string {
	t.Helper()
	for line := range strings.SplitSeq(outline, "\n") {
		if strings.Contains(line, what) {
			id, _, _ := strings.Cut(strings.TrimSpace(line), "] ")
			return strings.TrimPrefix(id, "[")
		}
	}
	t.Fatalf("no line holds %s in:\n%s", what, outline)
	return ""
}

// A model call still running when the engine detaches ends with it, so the
// kept browser's runtime closes and the next engine reattaches at once.
func TestStagehandDetachEndsAModelCallItWaitsOn(t *testing.T) {
	t.Parallel()
	hanging := func(ctx context.Context, _ generateRequest) (generateResponse, error) {
		<-ctx.Done()
		return generateResponse{}, ctx.Err()
	}
	eng := launchBrowser(t, launchOptions{Generate: hanging})
	require.NoError(t, eng.Goto(t.Context(), "data:text/html,"+strings.ReplaceAll(shopPage, "#", "%23"), time.Minute))
	_, err := eng.Act(t.Context(), "Click Add to cart", nil, nil, 2*time.Second)
	require.Error(t, err, "the act waits on a model that never answers")
	require.NoError(t, eng.Detach(t.Context()), "the runtime closes once its model call ends")

	var reattached engine
	err = withStartupSlot(func() (err error) {
		reattached, err = stagehandLauncher{}.Reattach(t.Context(), eng.Handle(), launchOptions{Generate: (&shopModel{}).generate})
		return err
	})
	require.NoError(t, err)
	require.NoError(t, reattached.Goto(t.Context(), "data:text/html,"+strings.ReplaceAll(shopPage, "#", "%23"), time.Minute))
}

// notesPage saves through a request when Submit is pressed.
const notesPage = `<!doctype html><html><head><title>Notes</title></head><body>
<p id="status">ready</p>
<button onclick="fetch('/save', {method: 'POST', body: new URLSearchParams({note: 'hello'})}).then(() => { document.getElementById('status').textContent = 'saved'; }, () => { document.getElementById('status').textContent = 'not saved'; })">Submit</button>
</body></html>`

// A browser launched with a write gate has every write decided from its
// first page on: an act's save is blocked and reported, and the act still
// completes.
func TestStagehandWriteGateDecidesFromTheFirstPage(t *testing.T) {
	t.Parallel()
	chrometest.Require(t)

	var saves atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			saves.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, notesPage)
	}))
	t.Cleanup(server.Close)
	launcher := stagehandLauncher{startGate: func(ctx context.Context, _ string, cfg writegate.Config) (*writegate.Handle, error) {
		ready := make(chan writegate.Ready, 1)
		failed := make(chan error, 1)
		go func() {
			failed <- writegate.Serve(context.WithoutCancel(ctx), cfg, func(r writegate.Ready) { ready <- r })
		}()
		select {
		case r := <-ready:
			// No process to end: the gate ends with its browser.
			return &writegate.Handle{URL: fmt.Sprintf("http://127.0.0.1:%d/%s", r.Port, cfg.Token)}, nil
		case err := <-failed:
			return nil, err
		}
	}}
	model := &shopModel{}
	var eng engine
	err := withStartupSlot(func() (err error) {
		eng, err = launcher.Launch(t.Context(), launchOptions{
			Executable: chrometest.Path(), Headless: true, UserDataDir: browserProfileDir(t), NoSandbox: true, BrowserDir: testBrowserDir,
			Generate: model.generate, WriteGate: &writeGateOptions{Mode: writegate.ModeRehearse},
		})
		return err
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = eng.Close(context.WithoutCancel(t.Context())) })
	require.NotNil(t, eng.Handle().WriteGate)

	require.NoError(t, eng.Goto(t.Context(), server.URL, time.Minute))
	outcome, err := eng.Act(t.Context(), "Click the Submit button", nil, nil, time.Minute)
	require.NoError(t, err)
	require.True(t, outcome.Success, outcome.Message)
	text, err := eng.PageText(t.Context())
	require.NoError(t, err)
	assert.Contains(t, text, "not saved")
	assert.Zero(t, saves.Load())

	writes, err := eng.Writes(t.Context(), 0)
	require.NoError(t, err)
	require.Len(t, writes, 1)
	assert.True(t, writes[0].Consequential())
	assert.Equal(t, "POST "+server.URL+"/save", writes[0].String())
	assert.Equal(t, []string{"note"}, writes[0].Fields)
}
