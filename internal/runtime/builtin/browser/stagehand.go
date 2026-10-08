// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	stagehand "github.com/browserbase/stagehand/packages/sdk-go/v4"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

// extractBatchSource runs an extract with a schema chosen at run time. The
// source is constant; every caller value travels through the batch input.
const extractBatchSource = `async (batch, input) => (await batch.extract(input.instruction, input.schema, input.options))`

const telemetryPath = "/v1/traces"

// actResponseFormat names the model answer that picks the element an act
// instruction describes. The answer's twoStep field makes the runtime ask
// the model for a second action and perform it too.
const actResponseFormat = "Act"

const (
	// pageCallTimeout bounds a page read or screenshot, so a page that stops
	// responding fails the step instead of hanging it.
	pageCallTimeout = 30 * time.Second
	// callTimeoutSlack lets the runtime report its own timeout before the
	// call is abandoned.
	callTimeoutSlack = 5 * time.Second
	// exitPollInterval spaces the checks for a closing browser's exit.
	exitPollInterval = 100 * time.Millisecond
	// unstartedCallTimeout bounds each request that ends a browser whose
	// runtime failed to start, so a slow request cannot use up the next one's
	// time.
	unstartedCallTimeout = 5 * time.Second
	// settleQuiet is how long a page goes without a request in flight or a
	// change to its document before what an action started counts as done;
	// settleTimeout bounds the wait, and settlePoll spaces its checks.
	settleQuiet   = 500 * time.Millisecond
	settleTimeout = 5 * time.Second
	settlePoll    = 100 * time.Millisecond
)

// watchExpression counts the page's requests in flight and notes when its
// document last changed, once per document, so the end of what an action
// starts on the page, such as a list the page fetches and draws, can be
// told.
const watchExpression = `(() => {
  if (window.__daguSettle) return true;
  const s = window.__daguSettle = {pending: 0, changed: Date.now()};
  const start = () => { s.pending++; s.changed = Date.now(); };
  const done = () => { s.pending--; s.changed = Date.now(); };
  const fetch = window.fetch;
  if (fetch) window.fetch = function (...args) { start(); return fetch.apply(window, args).finally(done); };
  const send = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.send = function (...args) { start(); this.addEventListener('loadend', done, {once: true}); return send.apply(this, args); };
  new MutationObserver(() => { s.changed = Date.now(); }).observe(document, {subtree: true, childList: true, attributes: true, characterData: true});
  return true;
})()`

// quietExpression reports whether the page has gone settleQuiet without a
// request in flight or a change to its document.
var quietExpression = fmt.Sprintf(`(() => { const s = window.__daguSettle; return !s || (s.pending <= 0 && Date.now() - s.changed >= %d); })()`, settleQuiet.Milliseconds())

// pageTextExpression reads the text a person sees on the page.
const pageTextExpression = `document.body ? document.body.innerText : ""`

// documentExpression reads the time the page's document began loading, which
// differs for every document the page loads.
const documentExpression = `String(performance.timeOrigin)`

// sessionLostMarkers are the texts by which the browser runtime reports a
// command whose page session was detached: the browser's answer to a command
// sent after the detach, and the runtime's own rejection of a command still
// in flight.
var sessionLostMarkers = []string{
	"Session with given id not found",
	"target closed before CDP",
}

// selectorVisibleExpression reports whether the selector, a JSON string
// literal substituted for %s, matches any rendered, visible element.
const selectorVisibleExpression = `Array.from(document.querySelectorAll(%s)).some((element) => {
	const box = element.getBoundingClientRect();
	const style = getComputedStyle(element);
	return box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none";
})`

var errImageInput = errors.New("browser: image input to the model is not supported")

// sandboxHint suggests how to let the browser sandbox start, or turn it off,
// when a launch fails where the sandbox is a likely cause: on Linux, with the
// sandbox still on.
func sandboxHint(noSandbox bool) string {
	if goruntime.GOOS != "linux" || noSandbox {
		return ""
	}
	return "; if the browser cannot use its sandbox here, as under Docker's default seccomp profile, " +
		"run the container with a profile that allows user namespaces, such as " +
		"/usr/share/dagu/seccomp-chromium.json in the dev image, or turn the sandbox off " +
		"with browser.sandbox: false in the Dagu config or DAGU_BROWSER_SANDBOX=false"
}

// sandboxOverride reports why the browser runtime would turn off the sandbox
// whatever the host setting says, or "" when it keeps it.
func sandboxOverride() string {
	if os.Getenv("CI") != "" {
		return "because CI is set"
	}
	if goruntime.GOOS == "linux" && os.Geteuid() == 0 {
		return "when running as root"
	}
	return ""
}

// stagehandLauncher runs sessions through the Stagehand Go SDK.
type stagehandLauncher struct{}

var _ launcher = stagehandLauncher{}

func (stagehandLauncher) Launch(ctx context.Context, opts launchOptions) (engine, error) {
	if reason := sandboxOverride(); reason != "" && !opts.NoSandbox {
		return nil, fmt.Errorf("launch browser: the browser sandbox is on, but the browser runtime turns it off %s; "+
			"set browser.sandbox: false in the Dagu config or DAGU_BROWSER_SANDBOX=false to run without it", reason)
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, err
	}
	launch := &stagehand.LocalBrowserLaunchOptions{
		ExecutablePath: opts.Executable,
		Headless:       opts.Headless,
		Port:           port,
		UserDataDir:    opts.UserDataDir,
		KeepAlive:      true,
	}
	launch.ChromiumSandbox = new(!opts.NoSandbox)
	if opts.Viewport != nil {
		launch.Viewport = &stagehand.LocalViewport{Width: opts.Viewport.Width, Height: opts.Viewport.Height}
	}
	if opts.Proxy != "" {
		launch.Proxy = &stagehand.LocalProxyConfig{Server: opts.Proxy}
	}
	cdpURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	browser, err := stagehand.LaunchLocalBrowser(ctx, launch)
	if err != nil {
		launchErr := fmt.Errorf("launch browser: %w%s", err, sandboxHint(opts.NoSandbox))
		return nil, errors.Join(launchErr, endUnstartedBrowser(ctx, nil, cdpURL, opts.UserDataDir))
	}
	eng, err := startEngine(ctx, browser, cdpURL, opts)
	if err != nil {
		return nil, errors.Join(err, endUnstartedBrowser(ctx, browser, cdpURL, opts.UserDataDir))
	}
	extension, err := browserhost.StagehandExtension(ctx, cdpURL)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("locate browser runtime extension: %w", err), eng.Close(context.WithoutCancel(ctx)))
	}
	eng.handle.ExtensionID = extension.ID
	eng.handle.ExtensionDir = extension.Path
	// Without a process ID, a browser that stops answering cannot be ended.
	eng.handle.BrowserPID, _ = browserhost.BrowserProcessID(ctx, cdpURL)
	eng.handle.BrowserStartedAt, _ = procutil.StartTime(eng.handle.BrowserPID)
	if err := eng.handleDownloads(ctx, opts.DownloadsDir); err != nil {
		return nil, errors.Join(err, eng.Close(context.WithoutCancel(ctx)))
	}
	return eng, nil
}

func (stagehandLauncher) Reattach(ctx context.Context, handle browserHandle, opts launchOptions) (engine, error) {
	browser, err := stagehand.ConnectLocalBrowser(ctx, stagehand.LocalBrowserConnectOptions{
		CDPURL:      handle.CDPURL,
		ExtensionID: handle.ExtensionID,
	})
	if err != nil {
		return nil, fmt.Errorf("reattach browser: %w", err)
	}
	eng, err := startEngine(ctx, browser, handle.CDPURL, opts)
	if err != nil {
		return nil, errors.Join(err, browser.Close(context.WithoutCancel(ctx)))
	}
	eng.handle.ExtensionID = handle.ExtensionID
	eng.handle.ExtensionDir = handle.ExtensionDir
	// Closing waits for the browser's process tree, which only the recorded
	// process identifies.
	eng.handle.BrowserPID = handle.BrowserPID
	eng.handle.BrowserStartedAt = handle.BrowserStartedAt
	if err := eng.handleDownloads(ctx, opts.DownloadsDir); err != nil {
		return nil, errors.Join(err, eng.Close(context.WithoutCancel(ctx)))
	}
	return eng, nil
}

// endUnstartedBrowser ends the browser launched with profileDir at cdpURL
// after its runtime failed to start. The SDK leaves a kept-alive browser
// running then, and browser, when known, may no longer reach it, so the
// browser is asked to exit over DevTools. The debugging port was only free
// when chosen, so the browser is closed only when it uses profileDir.
func endUnstartedBrowser(ctx context.Context, browser *stagehand.Browser, cdpURL, profileDir string) error {
	ctx = context.WithoutCancel(ctx)
	var err error
	if profileDir != "" {
		owned, ownErr := boundCall(ctx, unstartedCallTimeout, func(ctx context.Context) (bool, error) {
			return browserhost.UsesProfile(ctx, cdpURL, profileDir)
		})
		switch {
		case errors.Is(ownErr, browserhost.ErrUnreachable):
		case ownErr != nil:
			err = fmt.Errorf("identify browser: %w", ownErr)
		case owned:
			_, err = boundCall(ctx, unstartedCallTimeout, func(ctx context.Context) (struct{}, error) {
				return struct{}{}, browserhost.CloseBrowser(ctx, cdpURL)
			})
		}
	}
	if browser != nil {
		_, closeErr := boundCall(ctx, unstartedCallTimeout, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, browser.Close(ctx)
		})
		err = errors.Join(err, closeErr)
	}
	return err
}

func startEngine(ctx context.Context, browser *stagehand.Browser, cdpURL string, opts launchOptions) (*stagehandEngine, error) {
	sink, err := startTelemetrySink()
	if err != nil {
		return nil, err
	}
	off := false
	// Model calls end with the engine: the runtime closes only after the
	// call it waits on answers, so one left running would keep a kept
	// browser's runtime busy after the engine is gone.
	calls, endCalls := context.WithCancel(context.Background())
	client, err := stagehand.Create(ctx, stagehand.CreateOptions{
		Browser:  browser,
		Generate: stagehandGenerate(endingWith(calls, opts.Generate)),
		Cache:    new(stagehand.CacheEnabled(false)),
		SelfHeal: &off,
		// The SDK writes every enabled log line to the process stderr,
		// including filled-in variable values; the step keeps its own log.
		Logging: &stagehand.StagehandClientLoggingConfig{Level: stagehand.StagehandClientLogLevelOff},
		// Traces cannot be disabled, so they go to a loopback sink.
		Telemetry: stagehand.TelemetryConfig{Traces: stagehand.TelemetryTraces{Endpoint: sink.url}},
	})
	if err != nil {
		endCalls()
		sink.close()
		return nil, fmt.Errorf("start browser runtime: %w", err)
	}
	eng := &stagehandEngine{
		browser:         browser,
		client:          client,
		endCalls:        endCalls,
		sink:            sink,
		handle:          browserHandle{CDPURL: cdpURL},
		pageCallTimeout: pageCallTimeout,
	}
	// An unanswered dialog blocks its page, and the runtime does not answer
	// dialogs itself.
	if eng.dialogs, err = browserhost.WatchDialogs(ctx, cdpURL); err != nil {
		return nil, errors.Join(fmt.Errorf("watch dialogs: %w", err), eng.release(context.WithoutCancel(ctx)))
	}
	if len(opts.AllowedDomains) > 0 {
		// Blocked requests leave a broken page and a later, unrelated
		// failure, so they are reported by host.
		if eng.blocked, err = browserhost.WatchBlockedRequests(ctx, cdpURL); err != nil {
			return nil, errors.Join(fmt.Errorf("watch blocked requests: %w", err), eng.release(context.WithoutCancel(ctx)))
		}
		browserContext, err := browser.Context()
		if err == nil {
			err = browserContext.SetDomainPolicy(ctx, &stagehand.DomainPolicy{AllowedDomains: opts.AllowedDomains})
		}
		if err != nil {
			return nil, errors.Join(fmt.Errorf("apply allowed_domains: %w", err), eng.release(context.WithoutCancel(ctx)))
		}
	}
	return eng, nil
}

// stagehandEngine is an engine backed by one Stagehand client.
type stagehandEngine struct {
	browser *stagehand.Browser
	client  *stagehand.Stagehand
	// endCalls ends the model calls the runtime is waiting on.
	endCalls context.CancelFunc
	sink     *telemetrySink
	handle   browserHandle
	// downloads saves downloads into the step's artifacts; nil when
	// downloads are refused.
	downloads *browserhost.DownloadWatcher
	dialogs   *browserhost.DialogWatcher
	// blocked counts requests allowed_domains blocks; nil without
	// allowed_domains.
	blocked *browserhost.BlockedRequestWatcher
	// pageCallTimeout bounds calls that take no timeout of their own.
	pageCallTimeout time.Duration
}

// handleDownloads saves downloads into dir, or refuses them when dir is
// empty.
func (e *stagehandEngine) handleDownloads(ctx context.Context, dir string) error {
	if dir == "" {
		return browserhost.DenyDownloads(ctx, e.handle.CDPURL)
	}
	watcher, err := browserhost.WatchDownloads(ctx, e.handle.CDPURL, dir)
	if err != nil {
		return fmt.Errorf("watch downloads: %w", err)
	}
	e.downloads = watcher
	return nil
}

func (e *stagehandEngine) page(ctx context.Context) (*stagehand.Page, error) {
	browserContext, err := e.browser.Context()
	if err != nil {
		return nil, err
	}
	return browserContext.ActivePage(ctx)
}

func (e *stagehandEngine) Goto(ctx context.Context, url string, timeout time.Duration) error {
	_, err := boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (struct{}, error) {
		page, err := e.page(ctx)
		if err != nil {
			return struct{}{}, err
		}
		_, err = page.Goto(ctx, url, &stagehand.PageNavigationOptions{Timeout: new(int(timeout.Milliseconds()))})
		return struct{}{}, err
	})
	if err == nil {
		e.settle(ctx, "")
	}
	return err
}

func (e *stagehandEngine) Act(ctx context.Context, instruction string, variables map[string]string, timeout time.Duration) (actOutcome, error) {
	document := e.watch(ctx)
	result, err := boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (stagehand.ActResult, error) {
		return e.client.Act(ctx, stagehand.ActInstruction(instruction), actOptions(variables, timeout))
	})
	if lost := sessionLost(result, err); lost != nil {
		return actOutcome{}, lost
	}
	if err != nil {
		return actOutcome{}, err
	}
	if result.Data.Success {
		e.settle(ctx, document)
	}
	outcome := actOutcome{Message: result.Data.Message, Success: result.Data.Success}
	for _, action := range result.Data.Actions {
		recorded := recordedAction{
			Selector:    action.Selector,
			Description: action.Description,
			Arguments:   action.Arguments,
		}
		if action.Method != nil {
			recorded.Method = *action.Method
		}
		outcome.Actions = append(outcome.Actions, recorded)
	}
	return outcome, nil
}

func (e *stagehandEngine) Replay(ctx context.Context, recorded recordedAction, variables map[string]string, timeout time.Duration) (bool, error) {
	// The runtime types into or fills a hidden element without failing, so
	// a recorded element that is now hidden, such as a field in a closed
	// dialog, counts as a miss.
	if !e.targetVisible(ctx, recorded.Selector) {
		return false, ctx.Err()
	}
	action := stagehand.Action{
		Selector:    recorded.Selector,
		Description: recorded.Description,
		Arguments:   recorded.Arguments,
	}
	if recorded.Method != "" {
		action.Method = new(recorded.Method)
	}
	document := e.watch(ctx)
	result, err := boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (stagehand.ActResult, error) {
		return e.client.Act(ctx, stagehand.ObservedAction(action), actOptions(variables, timeout))
	})
	if lost := sessionLost(result, err); lost != nil {
		return false, lost
	}
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	if result.Data.Success {
		e.settle(ctx, document)
	}
	return result.Data.Success, nil
}

// watch readies the page to tell when what an action starts has finished,
// and returns the document the page shows before it.
func (e *stagehandEngine) watch(ctx context.Context) string {
	document, _ := e.DocumentID(ctx)
	_, _ = boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (json.RawMessage, error) {
		page, err := e.page(ctx)
		if err != nil {
			return nil, err
		}
		return page.Evaluate(ctx, watchExpression)
	})
	return document
}

// settle waits, up to settleTimeout, for what an action started to finish
// before the next operation reads the page: a new document until its network
// is idle, and the document the action began on until its requests end and
// it stops changing. A page that does not settle in time is left as it is.
func (e *stagehandEngine) settle(ctx context.Context, document string) {
	ctx, cancel := context.WithTimeout(ctx, settleTimeout)
	defer cancel()
	page, err := e.page(ctx)
	if err != nil {
		return
	}
	if now, err := e.DocumentID(ctx); err != nil || now != document {
		_ = page.WaitForLoadState(ctx, stagehand.LoadStateNetworkIdle, new(int(settleTimeout.Milliseconds())))
		return
	}
	for {
		quiet, err := page.Evaluate(ctx, quietExpression)
		if err != nil || string(quiet) == "true" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(settlePoll):
		}
	}
}

// targetVisible reports whether selector, resolved as a replayed action
// resolves it, matches a visible element. It reports false when the page
// cannot tell, including when the page was lost: no action has run yet, so
// a lost page is a miss, never an action that may have taken effect.
func (e *stagehandEngine) targetVisible(ctx context.Context, selector string) bool {
	visible, err := boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (bool, error) {
		page, err := e.page(ctx)
		if err != nil {
			return false, err
		}
		return page.Locator(selector).IsVisible(ctx)
	})
	return err == nil && visible
}

// sessionLost returns an error wrapping errPageSessionLost when an act call
// that returned result and err failed because the page's session was
// detached, or nil. The runtime reports that detach in a failed result when
// the action itself failed, and as an RPC error when the work around the
// action did.
func sessionLost(result stagehand.ActResult, err error) error {
	message := result.Data.Message
	var rpcErr *stagehand.RPCError
	switch {
	case errors.As(err, &rpcErr):
		message = rpcErr.Message
	case err != nil || result.Data.Success:
		return nil
	}
	for _, marker := range sessionLostMarkers {
		if strings.Contains(message, marker) {
			return fmt.Errorf("%w: %s", errPageSessionLost, message)
		}
	}
	return nil
}

func (e *stagehandEngine) DocumentID(ctx context.Context) (string, error) {
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (string, error) {
		page, err := e.page(ctx)
		if err != nil {
			return "", err
		}
		origin, err := page.Evaluate(ctx, documentExpression)
		if err != nil {
			return "", err
		}
		return page.PageID() + ":" + string(origin), nil
	})
}

func (e *stagehandEngine) Extract(ctx context.Context, instruction string, schema json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	return boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (json.RawMessage, error) {
		page, err := e.page(ctx)
		if err != nil {
			return nil, err
		}
		var result struct {
			Data json.RawMessage `json:"data"`
		}
		err = e.client.ExperimentalBatch(ctx, extractBatchSource,
			map[string]any{"instruction": instruction, "schema": schema, "options": map[string]any{}},
			&result, stagehand.ExperimentalBatchOptions{Timeout: timeout, Page: page})
		if err != nil {
			return nil, err
		}
		return result.Data, nil
	})
}

func (e *stagehandEngine) WaitForSelector(ctx context.Context, selector string, timeout time.Duration) error {
	matched, err := boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (bool, error) {
		page, err := e.page(ctx)
		if err != nil {
			return false, err
		}
		state := stagehand.PageWaitForSelectorOptionsStateVisible
		return page.WaitForSelector(ctx, selector, &stagehand.PageWaitForSelectorOptions{
			State:   &state,
			Timeout: new(int(timeout.Milliseconds())),
		})
	})
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("selector %q did not appear within %s", selector, timeout)
	}
	return nil
}

func (e *stagehandEngine) Screenshot(ctx context.Context) ([]byte, error) {
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) ([]byte, error) {
		page, err := e.page(ctx)
		if err != nil {
			return nil, err
		}
		return page.Screenshot(ctx, nil)
	})
}

func (e *stagehandEngine) CurrentURL(ctx context.Context) (string, error) {
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (string, error) {
		page, err := e.page(ctx)
		if err != nil {
			return "", err
		}
		return page.URL(ctx)
	})
}

func (e *stagehandEngine) PageText(ctx context.Context) (string, error) {
	raw, err := e.evaluate(ctx, pageTextExpression)
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", fmt.Errorf("read page text: %w", err)
	}
	return text, nil
}

func (e *stagehandEngine) SelectorVisible(ctx context.Context, selector string) (bool, error) {
	// The selector is embedded as a JSON string literal, never as code.
	literal, err := json.Marshal(selector)
	if err != nil {
		return false, err
	}
	raw, err := e.evaluate(ctx, fmt.Sprintf(selectorVisibleExpression, literal))
	if err != nil {
		return false, err
	}
	var visible bool
	if err := json.Unmarshal(raw, &visible); err != nil {
		return false, fmt.Errorf("check selector %q: %w", selector, err)
	}
	return visible, nil
}

func (e *stagehandEngine) Snapshot(ctx context.Context) (pageSnapshot, error) {
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (pageSnapshot, error) {
		page, err := e.page(ctx)
		if err != nil {
			return pageSnapshot{}, err
		}
		// Without options the tree covers iframes, as an act's does.
		snapshot, err := page.Snapshot(ctx, nil)
		if err != nil {
			return pageSnapshot{}, err
		}
		pageURL, err := page.URL(ctx)
		if err != nil {
			return pageSnapshot{}, err
		}
		title, err := page.Title(ctx)
		if err != nil {
			return pageSnapshot{}, err
		}
		return pageSnapshot{Tree: snapshot.FormattedTree, URLs: snapshot.URLMap, XPaths: snapshot.XPathMap, URL: pageURL, Title: title}, nil
	})
}

// evaluate runs a JavaScript expression in the active page.
func (e *stagehandEngine) evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (json.RawMessage, error) {
		page, err := e.page(ctx)
		if err != nil {
			return nil, err
		}
		return page.Evaluate(ctx, expression)
	})
}

func (e *stagehandEngine) TakeDialogs() []dialog {
	if e.dialogs == nil {
		return nil
	}
	accepted := e.dialogs.Take()
	dialogs := make([]dialog, 0, len(accepted))
	for _, d := range accepted {
		dialogs = append(dialogs, dialog{Type: d.Type, Message: d.Message})
	}
	return dialogs
}

func (e *stagehandEngine) TakeBlockedRequests() (map[string]int, error) {
	if e.blocked == nil {
		return nil, nil
	}
	return e.blocked.Take()
}

func (e *stagehandEngine) Handle() browserHandle {
	return e.handle
}

func (e *stagehandEngine) WaitForDownloads(ctx context.Context, grace, timeout time.Duration) ([]string, error) {
	if e.downloads == nil {
		return nil, nil
	}
	downloads, err := e.downloads.Wait(ctx, grace, timeout)
	names := make([]string, 0, len(downloads))
	for _, download := range downloads {
		names = append(names, download.Name)
	}
	return names, err
}

// Detach leaves the browser running. Closing the SDK browser handle would
// terminate it and delete the unpacked extension a later reattach needs.
func (e *stagehandEngine) Detach(ctx context.Context) error {
	return e.release(ctx)
}

func (e *stagehandEngine) Close(ctx context.Context) error {
	return errors.Join(e.release(ctx), closeBrowser(ctx, e.handle.BrowserPID, e.handle.BrowserStartedAt, e.browser.Close))
}

// closeBrowser terminates a launched browser with closeRuntime. The browser's
// process tree is recorded before closing from process ID pid and its start
// time startedAt. Besides the browser, closeRuntime waits for every process
// that inherited the browser's output, such as the Chrome updater on macOS,
// which can outlive the browser by minutes; where the tree exiting ends the
// close, that wait continues in the background once the tree has exited.
// Once closeRuntime returns, the tree is given time to finish exiting where
// helpers outlive the browser. Without a trackable process, closing waits
// for closeRuntime alone.
func closeBrowser(ctx context.Context, pid int, startedAt int64, closeRuntime func(context.Context) error) error {
	var tree *browserProcessTree
	if pid > 0 {
		tree = recordBrowserProcessTree(pid, startedAt)
	}
	if tree != nil {
		defer tree.release()
	}
	closed := make(chan error, 1)
	go func() { closed <- closeRuntime(ctx) }()
	var exitChecks <-chan time.Time
	if tree != nil && tree.exitEndsClose() {
		ticker := time.NewTicker(exitPollInterval)
		defer ticker.Stop()
		exitChecks = ticker.C
	}
	for {
		select {
		case err := <-closed:
			if tree == nil {
				return err
			}
			return errors.Join(err, tree.awaitExit(ctx))
		case <-ctx.Done():
			return ctx.Err()
		case <-exitChecks:
			if tree.exited() {
				return nil
			}
		}
	}
}

func (e *stagehandEngine) release(ctx context.Context) error {
	var errs []error
	if e.downloads != nil {
		errs = append(errs, e.downloads.Close())
		e.downloads = nil
	}
	if e.dialogs != nil {
		errs = append(errs, e.dialogs.Close())
		e.dialogs = nil
	}
	if e.blocked != nil {
		errs = append(errs, e.blocked.Close())
		e.blocked = nil
	}
	// The runtime closes once the model call it may be waiting on answers.
	e.endCalls()
	// The runtime ends its session over the page connection, which an
	// unresponsive page blocks; the client is released either way.
	_, err := boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, e.client.Close(ctx)
	})
	errs = append(errs, err)
	e.sink.close()
	return errors.Join(errs...)
}

// errBrowserUnresponsive reports a browser that did not answer in time.
var errBrowserUnresponsive = errors.New("the browser did not respond")

// boundCall runs call with a deadline of limit and reports when the browser
// did not answer in time, so an unresponsive page cannot hang the step.
func boundCall[T any](ctx context.Context, limit time.Duration, call func(context.Context) (T, error)) (T, error) {
	callCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	result, err := call(callCtx)
	if err != nil && ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("%w within %s: %w", errBrowserUnresponsive, limit, err)
	}
	return result, err
}

func actOptions(variables map[string]string, timeout time.Duration) *stagehand.StagehandClientActOptions {
	options := &stagehand.StagehandClientActOptions{Timeout: new(float64(timeout.Milliseconds()))}
	if len(variables) > 0 {
		options.Variables = make(stagehand.Variables, len(variables))
		for name, value := range variables {
			options.Variables[name] = stagehand.PrimitiveVariable(stagehand.StringVariable(value))
		}
	}
	return options
}

// stagehandGenerate adapts the runtime's model requests to generate.
// endingWith makes generate's calls end when calls ends.
func endingWith(calls context.Context, generate generateFunc) generateFunc {
	return func(ctx context.Context, req generateRequest) (generateResponse, error) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(calls, cancel)()
		return generate(ctx, req)
	}
}

func stagehandGenerate(generate generateFunc) stagehand.LLMGenerateFunc {
	return func(ctx context.Context, params stagehand.LLMGenerateParams) (stagehand.LLMGenerateResult, error) {
		structured, ok := params.AsStructured()
		if !ok {
			return stagehand.LLMGenerateResult{}, errors.New("browser: only structured model requests are supported")
		}
		req := generateRequest{
			SchemaName:  structured.ResponseFormat.Name,
			Schema:      structured.ResponseFormat.Schema,
			Temperature: structured.Temperature,
		}
		if structured.SystemPrompt != nil {
			req.System = *structured.SystemPrompt
		}
		for _, message := range structured.Messages {
			text, err := messageText(message.Content)
			if err != nil {
				return stagehand.LLMGenerateResult{}, err
			}
			req.Messages = append(req.Messages, generateMessage{Role: string(message.Role), Text: text})
		}
		resp, err := generate(ctx, req)
		if err != nil {
			return stagehand.LLMGenerateResult{}, err
		}
		answer := resp.JSON
		if req.SchemaName == actResponseFormat {
			if answer, err = singleStep(answer); err != nil {
				return stagehand.LLMGenerateResult{}, err
			}
		}
		return stagehand.StructuredGenerateResult(stagehand.LLMStructuredGenerateResult{
			Role: stagehand.LLMRoleAssistant,
			Content: stagehand.LLMMessageContent{
				stagehand.TextContentBlock(stagehand.LLMTextContent{Type: "text", Text: string(answer)}),
			},
			StructuredContent: answer,
			Usage: &stagehand.LLMUsage{
				InputTokens:  resp.Usage.Input,
				OutputTokens: resp.Usage.Output,
				TotalTokens:  resp.Usage.total(),
			},
		}), nil
	}
}

// singleStep turns off the second action an act answer asks for, so an act
// performs only the one action its instruction describes. An answer that is
// not an object is returned unchanged for the runtime to reject.
func singleStep(answer json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(answer, &fields); err != nil {
		return answer, nil
	}
	if _, ok := fields["twoStep"]; !ok {
		return answer, nil
	}
	fields["twoStep"] = json.RawMessage("false")
	single, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode act answer: %w", err)
	}
	return single, nil
}

func messageText(content stagehand.LLMMessageContent) (string, error) {
	var text strings.Builder
	for _, block := range content {
		if part, ok := block.AsText(); ok {
			text.WriteString(part.Text)
			continue
		}
		if _, ok := block.AsImage(); ok {
			return "", errImageInput
		}
	}
	return text.String(), nil
}

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("reserve browser debugging port: %w", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// telemetrySink accepts and discards trace exports on loopback.
type telemetrySink struct {
	url       string
	server    *http.Server
	closeOnce sync.Once
}

func startTelemetrySink() (*telemetrySink, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start telemetry sink: %w", err)
	}
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		}),
	}
	go func() { _ = server.Serve(listener) }()
	return &telemetrySink{url: "http://" + listener.Addr().String() + telemetryPath, server: server}, nil
}

func (s *telemetrySink) close() {
	s.closeOnce.Do(func() { _ = s.server.Close() })
}
