// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"time"

	stagehand "github.com/browserbase/stagehand/packages/sdk-go/v4"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/sysproxy"
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
	// browserExitTimeout is how long a browser asked to close gets to finish
	// writing its profile before it is ended. A graceful Chrome shutdown can
	// take tens of seconds on a loaded machine.
	browserExitTimeout = 20 * time.Second
	// unstartedCallTimeout bounds each request that ends a browser whose
	// runtime failed to start, so a slow request cannot use up the next one's
	// time.
	unstartedCallTimeout = 5 * time.Second
	// exitRequestTimeout bounds the request that asks a launched browser to
	// exit, so one that stops answering DevTools is ended by the runtime
	// instead of holding up the close.
	exitRequestTimeout = 2 * time.Second
	// settleQuiet is how long a page goes without a request in flight or a
	// change to its document before what an action started counts as done;
	// settleTimeout bounds the wait, and settlePoll spaces its checks.
	settleQuiet   = 500 * time.Millisecond
	settleTimeout = 5 * time.Second
	settlePoll    = 100 * time.Millisecond
	// writeGrace is how much longer than the wait it is part of a page read
	// may take; see waitContext.
	writeGrace = time.Second
	// botCheckWait bounds how long an action waits for a bot check the site
	// answered with to pass. A check that needs no person passes within
	// seconds by loading the page it stood in for, as it would for a
	// person waiting in front of the window.
	botCheckWait = 10 * time.Second
	// headlessScreenWidth and headlessScreenHeight size the screen a browser
	// without a window reports: a common desktop's, or its window's when
	// that is larger. Chrome otherwise reports 800x600, smaller than its
	// window, which pages that size themselves by the screen read as a
	// phone.
	headlessScreenWidth  = 1920
	headlessScreenHeight = 1080
)

// watchExpression counts the page's requests in flight, including reads of
// their answers, and notes when its document last changed, once per document,
// so the end of what an action starts on the page, such as a list the page
// fetches and draws, can be told.
const watchExpression = `(() => {
  if (window.__daguSettle) return true;
  const s = window.__daguSettle = {pending: 0, changed: Date.now()};
  const start = () => { s.pending++; s.changed = Date.now(); };
  const done = () => { s.pending--; s.changed = Date.now(); };
  const fetch = window.fetch;
  if (fetch) window.fetch = function (...args) { start(); return fetch.apply(window, args).finally(done); };
  for (const name of ['arrayBuffer', 'blob', 'bytes', 'formData', 'json', 'text']) {
    const read = Response.prototype[name];
    if (read) Response.prototype[name] = function (...args) { start(); return read.apply(this, args).finally(done); };
  }
  const send = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.send = function (...args) {
    start();
    this.addEventListener('loadend', done, {once: true});
    try { return send.apply(this, args); } catch (e) { done(); throw e; }
  };
  new MutationObserver(() => { s.changed = Date.now(); }).observe(document, {subtree: true, childList: true, attributes: true, characterData: true});
  return true;
})()`

// quietExpression reports whether the page has gone settleQuiet without a
// request in flight or a change to its document, or null when the document
// was never watched, as one an action navigated to is not.
var quietExpression = fmt.Sprintf(`(() => { const s = window.__daguSettle; return s ? s.pending <= 0 && Date.now() - s.changed >= %d : null; })()`, settleQuiet.Milliseconds())

// shownDocumentsFunction declares shownDocuments, which returns a document
// and the documents of the frames it shows, nested ones too, each with the
// XPath of the frame holding it as the browser runtime writes it across
// frames, so that path followed by an XPath in the document names an
// element. A frame from another origin cannot be read and is left out.
const shownDocumentsFunction = `function xpathOf(element) {
	const steps = [];
	for (let node = element; node && node.nodeType === Node.ELEMENT_NODE; node = node.parentNode) {
		let index = 1;
		for (let sibling = node.previousElementSibling; sibling; sibling = sibling.previousElementSibling) {
			if (sibling.localName === node.localName) index++;
		}
		steps.unshift(node.localName + "[" + index + "]");
	}
	return "/" + steps.join("/");
}
function shownDocuments(doc, path = "") {
	const documents = [{doc, path}];
	for (const frame of doc.querySelectorAll("frame, iframe")) {
		const box = frame.getBoundingClientRect();
		const shown = box.width > 0 && box.height > 0 && frame.checkVisibility({visibilityProperty: true});
		if (frame.contentDocument && shown) documents.push(...shownDocuments(frame.contentDocument, path + xpathOf(frame)));
	}
	return documents;
}`

// pageTextExpression reads the text a person sees on the page, in the
// frames it shows too. A body that is not rendered shows nothing, though
// its innerText is the text it holds.
const pageTextExpression = `(() => {
	` + shownDocumentsFunction + `
	return shownDocuments(document).map(({doc}) => doc.body && doc.body.checkVisibility() ? doc.body.innerText : "").filter(Boolean).join("\n");
})()`

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

// contextLostMarker is the browser's answer to a command on a script context
// that a new document replaced, in the page or in one of its frames.
const contextLostMarker = "Cannot find context with specified id"

// selectorVisibleExpression reports whether the selector, a JSON string
// literal substituted for %s, matches any rendered, visible element of the
// page or of the frames it shows, inside their open shadow roots too.
const selectorVisibleExpression = `(() => {
	` + shownDocumentsFunction + `
	const matching = (root) => [...root.querySelectorAll(%s)].concat(
		...[...root.querySelectorAll("*")].filter((element) => element.shadowRoot).map((element) => matching(element.shadowRoot)));
	return shownDocuments(document).some(({doc}) => matching(doc).some((element) => {
		const box = element.getBoundingClientRect();
		const style = doc.defaultView.getComputedStyle(element);
		return box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none";
	}));
})()`

// pageOutcomeExpression describes what the browser shows. On its error page
// that is the network error code and the host of the page that did not
// load, which Chrome uses as the error page's title, with the status of an
// HTTP error answer without a body, which the page shows as "HTTP ERROR
// 403" in every language; on a page, its host, HTTP status, title, and the
// start of its text.
var pageOutcomeExpression = fmt.Sprintf(`(() => {
	const text = ((document.body && document.body.innerText) || "").slice(0, 500);
	if (location.protocol === "chrome-error:") {
		const code = document.querySelector(".error-code");
		const shown = (code && code.textContent) || text;
		const status = shown.match(/HTTP ERROR (\d{3})/);
		if (status) return {code: %q, host: document.title, status: Number(status[1]), title: "", text: ""};
		const match = shown.match(/ERR_[A-Z0-9_]+/);
		return {code: match ? match[0] : "ERR_FAILED", host: document.title, status: 0, title: "", text: ""};
	}
	const entry = performance.getEntriesByType("navigation")[0];
	return {code: "", host: location.hostname, status: (entry && entry.responseStatus) || 0, title: document.title, text};
})()`, httpErrorCode)

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

// headlessScreen returns the flag that gives a browser without a window, at
// viewport v or the runtime's default size, a screen that holds its window.
func headlessScreen(v *viewport) string {
	width, height := headlessScreenWidth, headlessScreenHeight
	if v != nil {
		width, height = max(width, v.Width), max(height, v.Height)
	}
	return fmt.Sprintf("--screen-info={%dx%d}", width, height)
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
type stagehandLauncher struct {
	// startProxy starts the proxy presenting client certificates; nil
	// starts it as a process of the Dagu executable.
	startProxy func(ctx context.Context, executable string, cfg certproxy.Config) (*certproxy.Handle, error)
	// startGate starts the write gate; nil starts it as a process of the
	// Dagu executable.
	startGate func(ctx context.Context, executable string, cfg writegate.Config) (*writegate.Handle, error)
}

var _ launcher = stagehandLauncher{}

func (l stagehandLauncher) Launch(ctx context.Context, opts launchOptions) (engine, error) {
	if reason := sandboxOverride(); reason != "" && !opts.NoSandbox {
		return nil, fmt.Errorf("launch browser: the browser sandbox is on, but the browser runtime turns it off %s; "+
			"set browser.sandbox: false in the Dagu config or DAGU_BROWSER_SANDBOX=false to run without it", reason)
	}
	slot, err := browserhost.TakeSlot(ctx, opts.BrowserDir, opts.MaxOpen, opts.Waiting)
	if err != nil {
		return nil, err
	}
	eng, err := l.launch(ctx, opts)
	if err != nil {
		slot.Release()
		return nil, err
	}
	if eng.handle.BrowserPID <= 0 {
		eng.slot = slot
		return eng, nil
	}
	if err := slot.Hold(eng.handle.BrowserPID, eng.handle.BrowserStartedAt); err != nil {
		eng.slot = slot
		return nil, errors.Join(err, eng.Close(context.WithoutCancel(ctx)))
	}
	return eng, nil
}

// launch starts the browser opts describe and connects to it.
func (l stagehandLauncher) launch(ctx context.Context, opts launchOptions) (*stagehandEngine, error) {
	opts.Warn = warnOnce(opts.Warn)
	key := userAgentKey(opts.Executable)
	known := knownUserAgent(ctx, opts, key)
	sent := ""
	if opts.Headless {
		sent = known.UserAgent
	}
	started, err := l.start(ctx, opts, sent)
	if err != nil {
		return nil, err
	}
	if started, err = l.confirmUserAgent(ctx, opts, key, known, sent, started); err != nil {
		return nil, err
	}
	cdpURL := started.cdpURL
	// The gate decides the browser's requests from before the runtime
	// loads its first page.
	if opts.WriteGate != nil {
		if started.gate, err = l.startWriteGate(ctx, cdpURL, opts); err != nil {
			return nil, errors.Join(err, started.end(ctx))
		}
	}
	eng, err := startEngine(ctx, started.browser, cdpURL, opts)
	if err != nil {
		return nil, errors.Join(err, endUnstartedBrowser(ctx, started.browser, cdpURL, opts.UserDataDir), killCertProxy(started.proxy), killWriteGate(started.gate))
	}
	eng.handle.CertProxy = started.proxy
	eng.handle.WriteGate = started.gate
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

// start launches the browser opts describe, with the certificate proxy it
// needs, sending userAgent when it is set.
func (l stagehandLauncher) start(ctx context.Context, opts launchOptions, userAgent string) (startedBrowser, error) {
	port, err := freeLoopbackPort()
	if err != nil {
		return startedBrowser{}, err
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
	if userAgent != "" {
		launch.Args = append(launch.Args, "--user-agent="+userAgent)
	}
	if opts.Headless {
		launch.Args = append(launch.Args, headlessScreen(opts.Viewport))
	}
	cdpURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if opts.WriteGate != nil {
		// A service worker a site registered in an earlier run can start
		// with the browser, before the gate, to send a pending background
		// sync. The site registers its worker again when visited.
		if err := os.RemoveAll(filepath.Join(opts.UserDataDir, "Default", "Service Worker")); err != nil {
			return startedBrowser{}, fmt.Errorf("clear the profile's service workers: %w", err)
		}
	}
	var proxy *certproxy.Handle
	if len(opts.ClientCertificates) > 0 || len(opts.TrustedCAs) > 0 || opts.Guard {
		if proxy, err = l.startCertProxy(ctx, cdpURL, opts); err != nil {
			return startedBrowser{}, err
		}
		// The proxy routes the browser's requests, through browser.proxy
		// where one is set. The debugging port stays direct.
		launch.Proxy = nil
		proxy.DebugPort = port
		launch.Args = append(launch.Args, proxy.ChromeArgs()...)
	}
	browser, err := stagehand.LaunchLocalBrowser(ctx, launch)
	if err != nil {
		launchErr := fmt.Errorf("launch browser: %w%s", err, sandboxHint(opts.NoSandbox))
		return startedBrowser{}, errors.Join(launchErr, endUnstartedBrowser(ctx, nil, cdpURL, opts.UserDataDir), killCertProxy(proxy))
	}
	return startedBrowser{browser: browser, cdpURL: cdpURL, proxy: proxy}, nil
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
	eng.handle.CertProxy = handle.CertProxy
	eng.handle.WriteGate = handle.WriteGate
	// What the proxy reported before the browser was reattached belongs to
	// the process that drove it then.
	_, _ = eng.TakeCertificateEvents(ctx)
	if err := eng.handleDownloads(ctx, opts.DownloadsDir); err != nil {
		return nil, errors.Join(err, eng.Close(context.WithoutCancel(ctx)))
	}
	return eng, nil
}

// startCertProxy starts the proxy that presents opts.ClientCertificates for
// the browser at cdpURL and routes every other request the way this device
// does.
func (l stagehandLauncher) startCertProxy(ctx context.Context, cdpURL string, opts launchOptions) (*certproxy.Handle, error) {
	pac, warnings, err := sysproxy.Detect(ctx, opts.Proxy)
	if err != nil {
		return nil, fmt.Errorf("browser.proxy: %w", err)
	}
	for _, warning := range warnings {
		if opts.Warn != nil {
			opts.Warn(warning)
		}
	}
	start := l.startProxy
	if start == nil {
		start = certproxy.Start
	}
	proxy, err := start(ctx, cmnconfig.GetConfig(ctx).Paths.Executable, certproxy.Config{
		Token:      rand.Text(),
		CDPURL:     cdpURL,
		SystemPAC:  pac,
		Entries:    opts.ClientCertificates,
		TrustedCAs: opts.TrustedCAs,
		Guard:      opts.Guard,
	})
	if err != nil {
		return nil, fmt.Errorf("start the certificate proxy: %w", err)
	}
	return proxy, nil
}

// startWriteGate starts the gate deciding the writes of the browser at
// cdpURL.
func (l stagehandLauncher) startWriteGate(ctx context.Context, cdpURL string, opts launchOptions) (*writegate.Handle, error) {
	start := l.startGate
	if start == nil {
		start = writegate.Start
	}
	gate, err := start(ctx, cmnconfig.GetConfig(ctx).Paths.Executable, writegate.Config{
		Token:        rand.Text(),
		CDPURL:       cdpURL,
		Mode:         opts.WriteGate.Mode,
		Policy:       opts.WriteGate.Policy,
		Secrets:      opts.WriteGate.Secrets,
		ExtensionDir: filepath.Join(opts.UserDataDir, writeGateExtensionDir),
	})
	if err != nil {
		return nil, fmt.Errorf("start the write gate: %w", err)
	}
	return gate, nil
}

// writeGateExtensionDir is where in the browser's profile the write gate
// keeps the extension that blocks sockets.
const writeGateExtensionDir = "DaguWriteGate"

// killWriteGate ends the write gate of a browser that is gone, if it has
// one. A gate ended while its browser runs would let a request it paused
// through.
func killWriteGate(gate *writegate.Handle) error {
	if gate == nil {
		return nil
	}
	return gate.Kill()
}

// killCertProxy ends the certificate proxy of a browser that did not start,
// if it has one.
func killCertProxy(proxy *certproxy.Handle) error {
	if proxy == nil {
		return nil
	}
	return proxy.Kill()
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
		settleWait:      settleTimeout,
		botCheckWait:    botCheckWait,
	}
	// An unanswered dialog blocks its page, and the runtime does not answer
	// dialogs itself.
	if eng.dialogs, err = browserhost.WatchDialogs(ctx, cdpURL); err != nil {
		return nil, errors.Join(fmt.Errorf("watch dialogs: %w", err), eng.release(context.WithoutCancel(ctx)))
	}
	// A site that refuses the browser answers with a status or a bot check
	// the page itself may not show.
	if eng.responses, err = browserhost.WatchPageResponses(ctx, cdpURL); err != nil {
		return nil, errors.Join(fmt.Errorf("watch page responses: %w", err), eng.release(context.WithoutCancel(ctx)))
	}
	// A frame a form posts into keeps its document until the answer loads,
	// which only the browser tells.
	if eng.loads, err = browserhost.WatchFrameLoads(ctx, cdpURL); err != nil {
		return nil, errors.Join(fmt.Errorf("watch frame loads: %w", err), eng.release(context.WithoutCancel(ctx)))
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
	responses *browserhost.PageResponseWatcher
	loads     *browserhost.FrameLoadWatcher
	// acted is when the last action or goto began; zero before the first.
	acted time.Time
	// holds holds the open windows heldWindows found held, by target ID,
	// those it let go too.
	holds map[string]windowHold
	// blocked counts requests allowed_domains blocks; nil without
	// allowed_domains.
	blocked *browserhost.BlockedRequestWatcher
	// pageCallTimeout bounds calls that take no timeout of their own.
	pageCallTimeout time.Duration
	// settleWait bounds the wait for a document to settle.
	settleWait time.Duration
	// botCheckWait bounds the wait for a bot check to pass.
	botCheckWait time.Duration
	// certificateEventsSeen is the last certificate proxy event taken.
	certificateEventsSeen int64
	// slot is the open browser slot this process keeps for the browser
	// until it closes, when the browser cannot hold it itself; nil when the
	// browser holds its slot or there is no limit.
	slot *browserhost.Slot
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

// errNoPage reports a browser that shows no page, as for a moment after
// the window in front closes.
var errNoPage = errors.New("the browser shows no page")

// page returns the page operations work on: the page in front, or, when
// that is a held window, as heldWindows tells, the page that opened it.
func (e *stagehandEngine) page(ctx context.Context) (*stagehand.Page, error) {
	browserContext, err := e.browser.Context()
	if err != nil {
		return nil, err
	}
	page, err := browserContext.ActivePage(ctx)
	if err != nil {
		return nil, err
	}
	if page == nil {
		return nil, errNoPage
	}
	if opener, held := e.heldWindows()[page.PageID()]; held {
		pages, err := browserContext.Pages(ctx)
		if err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(pages, func(p *stagehand.Page) bool { return p.PageID() == opener }); i >= 0 {
			page = pages[i]
		}
	}
	// The caller's next call writes to the browser runtime, which closes its
	// connection to the browser when that call's context has ended.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return page, nil
}

// windowHold is a window held behind the page that opened it.
type windowHold struct {
	opener string
	// began is when the opener began the load the window was first held
	// for.
	began time.Time
	// released is set once the window is let go, for good.
	released bool
}

// heldWindows returns the windows operations look past, each with the
// target ID of the page that opened it. A window is held when the last
// action both opened it and made its opener load a document, as a search
// does that opens a "searching" window and posts into a frame whose answer
// closes the window. It stays held, through later actions too, while the
// opener loads and for settleQuiet after, which a window the answer closes
// takes to go; a window still open then is a page in its own right, such
// as a form to fill in, and is never held again, even by a load its
// opener begins after that quiet period.
func (e *stagehandEngine) heldWindows() map[string]string {
	holds := map[string]windowHold{}
	openers := map[string]string{}
	for _, window := range e.loads.Windows() {
		load := e.loads.PageLoad(window.Opener)
		hold, known := e.holds[window.TargetID]
		if !known {
			if e.acted.IsZero() || window.Opened.Before(e.acted) || load.Began.Before(e.acted) {
				continue
			}
			hold = windowHold{opener: window.Opener, began: load.Began}
		}
		if !hold.released {
			// A load begun a quiet period after the held one ended is
			// another.
			anotherLoad := load.Ended.After(hold.began) && load.Began.Sub(load.Ended) >= settleQuiet
			hold.released = anotherLoad || !load.Loading && time.Since(load.Ended) >= settleQuiet
		}
		holds[window.TargetID] = hold
		if !hold.released {
			openers[window.TargetID] = hold.opener
		}
	}
	e.holds = holds
	return openers
}

// Goto loads url over the page's own DevTools connection: the browser
// runtime's goto waits for a document even when the load brings none, and
// so fails a download only once its timeout ends.
func (e *stagehandEngine) Goto(ctx context.Context, url string, timeout time.Duration) error {
	limit := time.Now().Add(timeout)
	page, err := boundCall(ctx, e.pageCallTimeout, e.page)
	if err != nil {
		return err
	}
	e.acted = time.Now()
	loadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	downloads := 0
	if e.downloads != nil {
		downloads = e.downloads.Begun()
	}
	load, err := browserhost.Navigate(loadCtx, e.handle.CDPURL, page.PageID(), url)
	// A download is reported with the goto that began it, so the goto ends
	// once the download is seen to begin.
	if err == nil && load.Download && e.downloads != nil {
		err = e.downloads.AwaitBegun(loadCtx, downloads)
	}
	if err != nil {
		if ctx.Err() == nil && errors.Is(loadCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("the page did not load within %s", timeout)
		}
		return err
	}
	if load.Document {
		e.settle(ctx, limit)
		return nil
	}
	// A load that brought no new document has nothing to settle. A call
	// to the page returns only after the browser runtime has taken in what
	// the browser reported before it, such as a move to a fragment.
	_, _ = e.evaluate(ctx, "location.href")
	return nil
}

func (e *stagehandEngine) Act(ctx context.Context, instruction string, variables map[string]string, secret func(string) bool, timeout time.Duration) (actOutcome, error) {
	limit := time.Now().Add(timeout)
	// The page before the act identifies the element it acts on, which the
	// act may rename or remove. Without it, the actions go unidentified.
	before, beforeErr := e.Snapshot(ctx)
	e.watch(ctx)
	result, err := boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (stagehand.ActResult, error) {
		page, err := e.page(ctx)
		if err != nil {
			return stagehand.ActResult{}, err
		}
		return e.client.Act(ctx, stagehand.ActInstruction(instruction), actOptions(page, variables, timeout))
	})
	if lost := sessionLost(result, err); lost != nil {
		// The act may have loaded a new document before the page was lost.
		e.settle(ctx, limit)
		return actOutcome{}, lost
	}
	if err != nil {
		return actOutcome{}, err
	}
	if result.Data.Success {
		e.settle(ctx, limit)
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
	if beforeErr == nil {
		identifyOn(before, outcome.Actions, variables)
	}
	if message := uncheckedOption(before, beforeErr, outcome.Actions, variables, secret); outcome.Success && message != "" {
		outcome.Success, outcome.Message = false, message
	}
	return outcome, nil
}

// uncheckedOption says why an option an action picked is not known to be
// on its select: snap, the page before the actions ran, shows the select
// lacks it, or the page could not be read, as snapErr says. It returns ""
// otherwise. The runtime resets a select told to pick an option it lacks
// to its first option and reports success. A lacking select is reported
// with the option as compared and the options it offers; secret is as for
// engine.Act.
func uncheckedOption(snap pageSnapshot, snapErr error, actions []recordedAction, variables map[string]string, secret func(string) bool) string {
	for _, action := range actions {
		if action.Method != selectOptionMethod {
			continue
		}
		if snapErr != nil {
			return "the page could not be read to check the option picked: " + snapErr.Error()
		}
		if target, ok := targetOf(snap, action.Selector, true); ok && !offersOption(target.node(), action, variables) {
			node := target.node()
			return fmt.Sprintf("the %s offers no option %s; %s", withName(node.role, node.name),
				pickedOption(action.Arguments[0], variables, secret), offeredOptions(node))
		}
	}
	return ""
}

func (e *stagehandEngine) Replay(ctx context.Context, recorded recordedAction, variables map[string]string, timeout time.Duration) (bool, error) {
	limit := time.Now().Add(timeout)
	// The runtime types into or fills a hidden element without failing, so
	// a recorded element that is now hidden, such as a field in a closed
	// dialog, counts as a miss.
	if !e.targetVisible(ctx, recorded.Selector) {
		return false, ctx.Err()
	}
	// A layout change can put another element at the recorded location,
	// which the runtime acts on without failing.
	if recorded.Target != nil || recorded.Method == selectOptionMethod {
		snap, err := e.Snapshot(ctx)
		if err != nil || !stillTargets(recorded, snap, variables) {
			return false, ctx.Err()
		}
	}
	action := stagehand.Action{
		Selector:    recorded.Selector,
		Description: recorded.Description,
		Arguments:   recorded.Arguments,
	}
	if recorded.Method != "" {
		action.Method = new(recorded.Method)
	}
	e.watch(ctx)
	result, err := boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (stagehand.ActResult, error) {
		page, err := e.page(ctx)
		if err != nil {
			return stagehand.ActResult{}, err
		}
		return e.client.Act(ctx, stagehand.ObservedAction(action), actOptions(page, variables, timeout))
	})
	if lost := sessionLost(result, err); lost != nil {
		// The action may have loaded a new document before the page was lost.
		e.settle(ctx, limit)
		return false, lost
	}
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	if result.Data.Success {
		e.settle(ctx, limit)
	}
	return result.Data.Success, nil
}

// watch readies the document the page shows to tell when what an action
// starts on it has finished, and notes when the action began.
func (e *stagehandEngine) watch(ctx context.Context) {
	e.acted = time.Now()
	_, _ = boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (json.RawMessage, error) {
		page, err := e.page(ctx)
		if err != nil {
			return nil, err
		}
		return page.Evaluate(ctx, watchExpression)
	})
}

// settle waits for what an action started to finish before the next
// operation reads the page: for the document it shows to settle, and for a
// bot check the site answered with to pass. limit is when the action's own
// time ends, up to which a held window is waited out; zero waits out none.
func (e *stagehandEngine) settle(ctx context.Context, limit time.Time) {
	e.settleDocument(ctx, time.Now().Add(e.settleWait), limit)
	e.awaitBotCheck(ctx)
}

// awaitBotCheck waits, up to botCheckWait, while the page shows a bot check.
// A check passes by loading another document, which settles in turn, and
// may ask again. A check still shown when the wait ends is left to the
// operations that follow.
func (e *stagehandEngine) awaitBotCheck(ctx context.Context) {
	if !e.showsBotCheck(ctx) {
		return
	}
	deadline := time.Now().Add(e.botCheckWait)
	if !time.Now().Before(deadline) {
		return
	}
	ctx, cancel := waitContext(ctx, deadline)
	defer cancel()
	document, _ := e.DocumentID(ctx)
	for pause(ctx, settlePoll, deadline) {
		current, err := e.DocumentID(ctx)
		if err != nil || current == document {
			continue
		}
		settled := time.Now().Add(e.settleWait)
		if settled.After(deadline) {
			settled = deadline
		}
		e.settleDocument(ctx, settled, time.Time{})
		if !time.Now().Before(deadline) || !e.showsBotCheck(ctx) {
			return
		}
		document = current
	}
}

// showsBotCheck reports whether the page shows a bot check, reading it no
// longer than a page settles, so a page that does not answer delays nothing.
func (e *stagehandEngine) showsBotCheck(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, e.settleWait)
	defer cancel()
	page, err := e.PageOutcome(ctx)
	return err == nil && page.BotCheck != ""
}

// settleDocument waits, until deadline, for the document the page shows to
// settle: every frame of the browser's pages, a page's own included, until
// the document it loads has loaded, a new document until its network is
// idle, and the document an action began on until its requests end and it
// stops changing. The page is watched for settleQuiet at least, since a
// navigation an action starts may begin a moment after the action returns.
// While a window is held, as heldWindows tells, the wait goes on until none
// is, up to limit when that is later than deadline. A page that does not
// settle in time is left as it is.
func (e *stagehandEngine) settleDocument(ctx context.Context, deadline, limit time.Time) {
	// A held window holds the wait until the later of the two.
	heldEnd := deadline
	if limit.After(heldEnd) {
		heldEnd = limit
	}
	ctx, cancel := waitContext(ctx, heldEnd)
	defer cancel()
	watched := time.Now().Add(settleQuiet)
	for {
		held := len(e.heldWindows()) > 0
		end := deadline
		if held {
			end = heldEnd
		}
		if ctx.Err() != nil || !time.Now().Before(end) {
			return
		}
		if !held && !e.loads.Loading() {
			// The page in front changes as the page opens or closes a window.
			page, err := e.page(ctx)
			if err != nil {
				return
			}
			quiet, err := page.Evaluate(ctx, quietExpression)
			switch {
			case err == nil && string(quiet) == "true":
				if !time.Now().Before(watched) {
					return
				}
			case err == nil && string(quiet) == "false":
				// The document still has requests in flight or changes.
			default:
				// A page that can't be read may be between documents, and one
				// that reports no quiet state shows a document watch never
				// readied; both wait as a newly loaded page does.
				if wait := time.Until(deadline); wait > 0 {
					_ = page.WaitForLoadState(ctx, stagehand.LoadStateNetworkIdle, new(int(wait.Milliseconds())))
				}
				return
			}
		}
		pause(ctx, settlePoll, end)
	}
}

// waitContext returns the context for the page reads of a wait that ends at
// deadline. A read starts only before the deadline and may take writeGrace
// past it, so it never writes to the browser runtime on a context that has
// ended, which closes the runtime's connection to the browser.
func waitContext(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, deadline.Add(writeGrace))
}

// pause waits up to d, no later than deadline, and reports whether the
// wait it is part of goes on, which waitContext explains.
func pause(ctx context.Context, d time.Duration, deadline time.Time) bool {
	timer := time.NewTimer(min(d, time.Until(deadline)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	return time.Now().Before(deadline)
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
// detached, or errPageContextLost when its script context was replaced, or
// nil. The runtime reports that loss in a failed result when the action
// itself failed, and as an RPC error when the work around the action did.
func sessionLost(result stagehand.ActResult, err error) error {
	message := result.Data.Message
	var rpcErr *stagehand.RPCError
	switch {
	case errors.As(err, &rpcErr):
		message = rpcErr.Message
	case err != nil || result.Data.Success:
		return nil
	}
	if strings.Contains(message, contextLostMarker) {
		return fmt.Errorf("%w: %s", errPageContextLost, message)
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

func (e *stagehandEngine) Observe(ctx context.Context, instruction string, timeout time.Duration) ([]recordedAction, error) {
	result, err := boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (stagehand.ObserveResult, error) {
		page, err := e.page(ctx)
		if err != nil {
			return stagehand.ObserveResult{}, err
		}
		return e.client.Observe(ctx, &instruction, &stagehand.StagehandClientObserveOptions{Page: page, Timeout: new(float64(timeout.Milliseconds()))})
	})
	if err != nil {
		return nil, err
	}
	found := make([]recordedAction, 0, len(result.Data))
	for _, action := range result.Data {
		element := recordedAction{Selector: action.Selector, Description: action.Description, Arguments: action.Arguments}
		if action.Method != nil {
			element.Method = *action.Method
		}
		found = append(found, element)
	}
	return found, nil
}

func (e *stagehandEngine) WaitForSelector(ctx context.Context, selector string, timeout time.Duration) error {
	// The runtime finds a selector that hops into frames, or an XPath, as
	// written, but looks for a CSS selector in the page's own document only.
	if !strings.Contains(selector, ">>") && !strings.HasPrefix(selector, "xpath=") && !strings.HasPrefix(selector, "/") {
		return e.waitForVisible(ctx, selector, timeout)
	}
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

// waitForVisible waits up to timeout for the CSS selector to match a
// visible element, as SelectorVisible tells, reading the page as
// waitContext allows.
func (e *stagehandEngine) waitForVisible(ctx context.Context, selector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	reads, cancel := waitContext(ctx, deadline)
	defer cancel()
	for {
		visible, err := e.SelectorVisible(reads, selector)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err == nil && visible:
			return nil
		// A read the deadline cut short found nothing in time.
		case err != nil && time.Now().Before(deadline):
			return err
		}
		if !pause(reads, conditionPollInterval, deadline) {
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("selector %q did not appear within %s", selector, timeout)
		}
	}
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

func (e *stagehandEngine) Opener(ctx context.Context) (string, bool, error) {
	found := false
	title, err := boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (string, error) {
		opener, err := e.opener(ctx)
		if err != nil || opener == nil {
			return "", err
		}
		found = true
		return opener.Title(ctx)
	})
	return title, found && err == nil, err
}

func (e *stagehandEngine) OpenerScreenshot(ctx context.Context) ([]byte, error) {
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) ([]byte, error) {
		opener, err := e.opener(ctx)
		if err != nil {
			return nil, err
		}
		if opener == nil {
			return nil, errors.New("no open page opened the window operations work on")
		}
		return opener.Screenshot(ctx, nil)
	})
}

// opener returns the page that opened the window operations work on, while
// it is open, or nil when operations work on a page no open page opened.
func (e *stagehandEngine) opener(ctx context.Context) (*stagehand.Page, error) {
	page, err := e.page(ctx)
	if err != nil {
		return nil, err
	}
	windows := e.loads.Windows()
	i := slices.IndexFunc(windows, func(w browserhost.Window) bool { return w.TargetID == page.PageID() })
	if i < 0 {
		return nil, nil
	}
	openerID := windows[i].Opener
	browserContext, err := e.browser.Context()
	if err != nil {
		return nil, err
	}
	pages, err := browserContext.Pages(ctx)
	if err != nil {
		return nil, err
	}
	if j := slices.IndexFunc(pages, func(p *stagehand.Page) bool { return p.PageID() == openerID }); j >= 0 {
		return pages[j], nil
	}
	return nil, nil
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
		snap := pageSnapshot{Tree: snapshot.FormattedTree, URLs: snapshot.URLMap, XPaths: snapshot.XPathMap, URL: pageURL, Title: title}
		snap.Attributes = controlAttributes(ctx, page, snap)
		return snap, nil
	})
}

// controlAttributes reads the name or id attribute of each control the
// tree of snap leaves unnamed, by element ID. A control whose attributes
// cannot be read, as in a frame from another origin, is left out.
func controlAttributes(ctx context.Context, page *stagehand.Page, snap pageSnapshot) map[string]string {
	ids := unnamedControls(parseSnapshotTree(snap.Tree))
	if len(ids) == 0 {
		return nil
	}
	xpaths := make([]string, 0, len(ids))
	for _, id := range ids {
		xpaths = append(xpaths, snap.XPaths[id])
	}
	// The XPaths are embedded as a JSON array literal, never as code.
	literal, err := json.Marshal(xpaths)
	if err != nil {
		return nil
	}
	raw, err := page.Evaluate(ctx, fmt.Sprintf(controlAttributesExpression, literal))
	var values []string
	if err != nil || json.Unmarshal(raw, &values) != nil || len(values) != len(ids) {
		return nil
	}
	attributes := map[string]string{}
	for i, id := range ids {
		if values[i] != "" {
			attributes[id] = values[i]
		}
	}
	return attributes
}

// evaluate runs a JavaScript expression in the active page. A navigation an
// action starts may replace the page's script context after the action
// returned, so an expression that loses its context runs once more after the
// new document settles, as rereadOnNewDocument tells.
func (e *stagehandEngine) evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	read := func() (json.RawMessage, error) {
		return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (json.RawMessage, error) {
			page, err := e.page(ctx)
			if err != nil {
				return nil, err
			}
			return page.Evaluate(ctx, expression)
		})
	}
	settle := func() { e.settleDocument(ctx, time.Now().Add(e.settleWait), time.Time{}) }
	return rereadOnNewDocument(read, settle)
}

// rereadOnNewDocument returns what read returns, running read once more
// after settle when a new document replaced the script context it ran in.
// Its error wraps errPageContextLost when that run loses its context too.
func rereadOnNewDocument(read func() (json.RawMessage, error), settle func()) (json.RawMessage, error) {
	raw, err := read()
	if !contextLost(err) {
		return raw, err
	}
	settle()
	raw, err = read()
	if contextLost(err) {
		return nil, fmt.Errorf("%w: %w", errPageContextLost, err)
	}
	return raw, err
}

// contextLost reports whether a page call failed because a new document
// replaced the script context it ran in.
func contextLost(err error) bool {
	var rpcErr *stagehand.RPCError
	return errors.As(err, &rpcErr) && strings.Contains(rpcErr.Message, contextLostMarker)
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

func (e *stagehandEngine) TakeCertificateEvents(ctx context.Context) ([]certproxy.Event, error) {
	if e.handle.CertProxy == nil {
		return nil, nil
	}
	events, err := boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) ([]certproxy.Event, error) {
		return certproxy.Events(ctx, e.handle.CertProxy.URL, e.certificateEventsSeen)
	})
	if len(events) > 0 {
		e.certificateEventsSeen = events[len(events)-1].Seq
	}
	return events, err
}

func (e *stagehandEngine) PageOutcome(ctx context.Context) (pageOutcome, error) {
	raw, err := e.evaluate(ctx, pageOutcomeExpression)
	if err != nil {
		return pageOutcome{}, err
	}
	var outcome pageOutcome
	if err := json.Unmarshal(raw, &outcome); err != nil {
		return pageOutcome{}, fmt.Errorf("read the page: %w", err)
	}
	// An error page's title that is not a host name is not the failed
	// host.
	if hostpattern.Validate(outcome.Host) != nil || strings.Contains(outcome.Host, "*") {
		outcome.Host = ""
	}
	// The answer the host gave the page tells whether it was a bot check.
	// An answer of another status was to an earlier page.
	if e.responses != nil && outcome.Host != "" {
		if response, ok := e.responses.Response(outcome.Host); ok && response.Status == outcome.Status {
			outcome.BotCheck = response.BotCheck
		}
	}
	return outcome, nil
}

func (e *stagehandEngine) PendingConnections(ctx context.Context) ([]certproxy.Pending, error) {
	if e.handle.CertProxy == nil {
		return nil, nil
	}
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) ([]certproxy.Pending, error) {
		return certproxy.PendingConnections(ctx, e.handle.CertProxy.URL)
	})
}

func (e *stagehandEngine) CertificateVerified(ctx context.Context, host string) (bool, error) {
	if e.handle.CertProxy == nil {
		return false, nil
	}
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (bool, error) {
		return certproxy.Verified(ctx, e.handle.CertProxy.URL, host)
	})
}

func (e *stagehandEngine) Writes(ctx context.Context, after int64) ([]writegate.Event, error) {
	if e.handle.WriteGate == nil {
		return nil, nil
	}
	return boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) ([]writegate.Event, error) {
		return writegate.Events(ctx, e.handle.WriteGate.URL, after)
	})
}

func (e *stagehandEngine) BeginWrites(ctx context.Context, signatures []string, lease time.Duration) error {
	if e.handle.WriteGate == nil {
		return nil
	}
	_, err := boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, writegate.Begin(ctx, e.handle.WriteGate.URL, signatures, lease)
	})
	return err
}

func (e *stagehandEngine) EndWrites(ctx context.Context) error {
	if e.handle.WriteGate == nil {
		return nil
	}
	_, err := boundCall(ctx, e.pageCallTimeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, writegate.End(ctx, e.handle.WriteGate.URL)
	})
	return err
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

// Close terminates the browser, and then the proxy that presented its
// client certificates and the gate that decided its writes.
func (e *stagehandEngine) Close(ctx context.Context) error {
	defer e.slot.Release()
	releaseErr := e.release(ctx)
	// The runtime asks a browser it reattached to exit itself.
	if e.browser.Origin() == stagehand.BrowserOriginLaunched {
		exitLaunchedBrowser(ctx, e.handle)
	}
	return errors.Join(
		releaseErr,
		closeBrowser(ctx, e.handle.BrowserPID, e.handle.BrowserStartedAt, e.browser.Close),
		killCertProxy(e.handle.CertProxy),
		killWriteGate(e.handle.WriteGate),
	)
}

// exitLaunchedBrowser asks a browser it launched to exit over DevTools and
// waits up to browserExitTimeout for its processes to exit. Exiting on
// request, the browser first writes what its profile keeps, such as the
// cookies of a sign-in, and it stops answering DevTools before it is done.
// The runtime ends a launched browser's processes all at once, which loses
// what is not written yet, so it ends only a browser that does not exit on
// request or in time.
func exitLaunchedBrowser(ctx context.Context, handle browserHandle) {
	var tree *browserProcessTree
	if handle.BrowserPID > 0 {
		tree = recordBrowserProcessTree(handle.BrowserPID, handle.BrowserStartedAt)
	}
	if tree != nil {
		defer tree.release()
	}
	if askToExit(ctx, handle.CDPURL) != nil || tree == nil {
		return
	}
	ticker := time.NewTicker(exitPollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(browserExitTimeout)
	defer deadline.Stop()
	for !tree.exited() {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

// askToExit asks the browser at cdpURL to exit over DevTools, giving up on
// the request after exitRequestTimeout.
func askToExit(ctx context.Context, cdpURL string) error {
	_, err := boundCall(ctx, exitRequestTimeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, browserhost.CloseBrowser(ctx, cdpURL)
	})
	return err
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
	if e.responses != nil {
		errs = append(errs, e.responses.Close())
		e.responses = nil
	}
	if e.loads != nil {
		errs = append(errs, e.loads.Close())
		e.loads = nil
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
	// A call whose context has ended when it writes to the browser runtime
	// closes the runtime's connection to the browser.
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	callCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	result, err := call(callCtx)
	if err != nil && ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("%w within %s: %w", errBrowserUnresponsive, limit, err)
	}
	return result, err
}

func actOptions(page *stagehand.Page, variables map[string]string, timeout time.Duration) *stagehand.StagehandClientActOptions {
	options := &stagehand.StagehandClientActOptions{Page: page, Timeout: new(float64(timeout.Milliseconds()))}
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
