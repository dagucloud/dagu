// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

const (
	// downloadGrace is how long a step waits, before it ends or pauses, for
	// a download the last operation started to begin.
	downloadGrace = 3 * time.Second
	kindDownload  = "download"
	kindDialog    = "dialog"
	// kindAllowedDomains labels the requests allowed_domains blocked.
	kindAllowedDomains = "allowed_domains"
	// conditionPollInterval spaces the retries of a fixed expect check.
	conditionPollInterval = 250 * time.Millisecond
	sweepBudget           = 5 * time.Second
	shutdownTimeout       = 30 * time.Second
	finalShotLabel        = "final"
	failureShotLabel      = "failure"
)

// noActionFoundMessage is how the browser runtime reports an act whose model
// chose no element.
const noActionFoundMessage = "No action found"

// statementSchema is the extract schema used to judge when and expect
// statements.
var statementSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["answer","reason"],"properties":{"answer":{"type":"boolean","description":"Whether the statement is true for the current page"},"reason":{"type":"string","description":"One sentence explaining the answer"}}}`)

// run executes one step attempt: a fresh browser session, or a session
// resumed after a person answered an ask operation.
type run struct {
	exec      *browserExecutor
	cfg       config
	dagName   string
	dagRunID  string
	stepName  string
	workerID  string
	store     *browserhost.Store
	browser   string
	secrets   map[string]string
	masker    *masking.Masker
	bridge    *modelBridge
	cache     *replayCache
	artifacts *artifactStore
	timeline  *timeline
	eng       engine
	record    browserhost.Record
	profile   *profileLease
	variables map[string]string
	// answers holds the values people gave to ask operations.
	answers map[string]string
	outputs map[string]any
	// blocked counts the requests allowed_domains blocked in this attempt,
	// by host.
	blocked map[string]int
	// blockedUncounted is set once blocked requests can no longer be
	// counted.
	blockedUncounted bool
	// downloadWindow is the longest timeout of the acts and gotos run so
	// far, which can start downloads; zero until one runs.
	downloadWindow time.Duration
}

func newRun(ctx context.Context, e *browserExecutor) (*run, error) {
	env := runtime.GetEnv(ctx)
	dataDir := cmnconfig.GetConfig(ctx).Paths.DataDir
	if dataDir == "" {
		return nil, errors.New("browser: the Dagu data directory is not configured")
	}
	var secrets map[string]string
	artifactsDir := ""
	if env.Scope != nil {
		secrets = env.Scope.AllSecrets()
		artifactsDir, _ = env.Scope.Get(runenv.EnvKeyDAGRunArtifactsDir)
	}
	if err := checkSecrets(e.cfg, secrets); err != nil {
		return nil, err
	}
	dagName := ""
	if env.DAG != nil {
		dagName = env.DAG.Name
	}
	stepKey := e.step.ID
	if stepKey == "" {
		stepKey = e.step.Name
	}
	masker := newMasker(secrets, nil)
	bridge, err := newModelBridge(ctx, e.step.LLM, masker, e.newProvider)
	if err != nil {
		return nil, err
	}
	browserDir := filepath.Join(dataDir, browserhost.DataDirName)
	r := &run{
		exec:      e,
		cfg:       e.cfg,
		dagName:   dagName,
		dagRunID:  env.DAGRunID,
		stepName:  e.step.Name,
		workerID:  env.WorkerID,
		store:     browserhost.NewStore(browserDir),
		browser:   browserDir,
		secrets:   secrets,
		masker:    masker,
		bridge:    bridge,
		artifacts: newArtifactStore(artifactsDir, stepKey),
		variables: maps.Clone(e.cfg.Variables),
		answers:   map[string]string{},
		outputs:   map[string]any{},
		blocked:   map[string]int{},
	}
	if r.variables == nil {
		r.variables = map[string]string{}
	}
	if e.cfg.cacheEnabled() {
		if r.cache, err = openReplayCache(browserDir, dagName, stepKey); err != nil {
			return nil, err
		}
	}
	r.timeline = &timeline{log: e.stderr, masker: masker, total: len(e.cfg.Do), update: e.updateSession}
	return r, nil
}

func (r *run) execute(ctx context.Context) error {
	if r.cfg.hasAsk() && !r.exec.askSupported {
		return errors.New("browser: ask operations are not supported on Windows, where the browser cannot outlive the step process")
	}
	sweepCtx, cancel := context.WithTimeout(ctx, sweepBudget)
	_ = browserhost.Sweep(sweepCtx, r.store, time.Now(), nil)
	cancel()

	start, err := r.startSession(ctx)
	r.reportDialogs(-1)
	r.reportBlocked(-1)
	if err == nil {
		err = r.checkPage(ctx)
	}
	if err != nil {
		return r.fail(ctx, -1, "", err)
	}
	for i := start; i < len(r.cfg.Do); i++ {
		op := r.cfg.Do[i]
		if op.When != nil {
			began, before := time.Now(), r.bridge.totals()
			holds, reason, err := r.await(ctx, *op.When, op.When.window(0), op.timeout())
			if err != nil {
				return r.fail(ctx, i, op.kind(), fmt.Errorf("evaluate when: %w", err))
			}
			if !holds {
				r.timeline.operation(operationReport{
					index: i, kind: op.kind(), subject: op.When.String(), status: statusSkipped, detail: reason,
					tokens: r.bridge.totals().sub(before).total(), duration: time.Since(began),
				})
				continue
			}
		}
		if op.Ask != nil {
			if err := r.settleDownloads(ctx, i-1, true); err != nil {
				return r.fail(ctx, i, kindDownload, err)
			}
			return r.waitForInput(ctx, i, *op.Ask)
		}
		err := r.runOperation(ctx, i, op)
		r.reportDialogs(i)
		r.reportBlocked(i)
		if err != nil {
			return r.fail(ctx, i, op.kind(), err)
		}
		if err := r.checkPage(ctx); err != nil {
			return r.fail(ctx, i, op.kind(), err)
		}
		if op.Act != nil || op.Goto != "" {
			r.downloadWindow = max(r.downloadWindow, op.timeout())
		}
		if err := r.settleDownloads(ctx, i, false); err != nil {
			return r.fail(ctx, i, kindDownload, err)
		}
	}
	last := len(r.cfg.Do) - 1
	if err := r.settleDownloads(ctx, last, true); err != nil {
		return r.fail(ctx, last, kindDownload, err)
	}
	return r.succeed(ctx)
}

// reportDialogs records the dialogs the browser accepted while the operation
// at index ran.
func (r *run) reportDialogs(index int) {
	if r.eng == nil {
		return
	}
	for _, d := range r.eng.TakeDialogs() {
		r.timeline.operation(operationReport{
			index: index, kind: kindDialog, subject: d.Message, status: statusCompleted, detail: "accepted " + d.Type,
		})
	}
}

// reportBlocked records the requests allowed_domains blocked while the
// operation at index ran.
func (r *run) reportBlocked(index int) {
	if r.eng == nil {
		return
	}
	blocked, err := r.eng.TakeBlockedRequests()
	if err != nil && !r.blockedUncounted {
		r.blockedUncounted = true
		_, _ = fmt.Fprintf(r.timeline.log, "warning: stopped counting requests blocked by allowed_domains: %s\n",
			r.masker.MaskString(err.Error()))
	}
	if len(blocked) == 0 {
		return
	}
	for host, count := range blocked {
		r.blocked[host] += count
	}
	r.timeline.blocked(index, describeBlocked(blocked))
}

// checkPage fails when the page has left browser.allowed_domains, which a
// redirect or an act can cause even when every goto target was allowed.
func (r *run) checkPage(ctx context.Context) error {
	if len(r.cfg.Browser.AllowedDomains) == 0 {
		return nil
	}
	current, err := r.eng.CurrentURL(ctx)
	if err != nil {
		return err
	}
	if err := checkAllowedDomain(current, r.cfg.Browser.AllowedDomains); err != nil {
		return fmt.Errorf("the page navigated away: %w", err)
	}
	return nil
}

// settleDownloads waits for downloads that are still running and records the
// finished ones. index is the operation that ran last. Downloads get the
// longest timeout of the acts and gotos that could have started them.
// Before the step ends or pauses, it also allows a download that an act or
// goto just triggered a moment to begin.
func (r *run) settleDownloads(ctx context.Context, index int, final bool) error {
	if r.downloadWindow == 0 {
		return nil
	}
	grace := time.Duration(0)
	if final {
		grace = downloadGrace
	}
	names, err := r.eng.WaitForDownloads(ctx, grace, r.downloadWindow)
	for _, name := range names {
		rel := r.artifacts.downloadPath(name)
		r.timeline.operation(operationReport{
			index: index, kind: kindDownload, subject: name, status: statusCompleted,
			detail: rel, files: []string{rel},
		})
	}
	return err
}

// startSession launches a browser, or reattaches to the one an answered ask
// operation left running, and returns the first operation to run.
func (r *run) startSession(ctx context.Context) (int, error) {
	session := r.exec.GetAgentSession()
	recordID := browserhost.RecordID(r.dagRunID, r.stepName)
	answer, answered := pendingAnswer(session)
	if answered {
		return r.resumeSession(ctx, recordID, session, answer)
	}
	if stale, err := r.store.Load(recordID); err == nil {
		// A previous attempt left a browser behind; it cannot be resumed. When
		// it does not close, its record is the only way to find it again.
		if err := browserhost.Release(ctx, r.store, stale); err != nil {
			return 0, fmt.Errorf("close the browser a previous attempt left open: %w", err)
		}
	}

	r.exec.updateSession(func(s *ir.AgentSession) {
		s.Provider = providerName
		if s.Generation < 1 {
			s.Generation = 1
		}
		// Unanswered asks from an abandoned attempt belong to a browser that
		// no longer exists.
		if len(s.Interactions) > 0 {
			s.Generation++
			s.Interactions = nil
		}
		s.State = ir.AgentSessionRunning
		s.RestartPending = false
		s.PromptSent = true
		s.OwnerWorkerID = r.workerID
		s.LastError = ""
		s.Model = r.modelLabel()
	})
	generation := r.exec.GetAgentSession().Generation

	opts, err := r.launchOptions(ctx, recordID)
	if err != nil {
		return 0, err
	}
	r.timeline.lifecycle(statusRunning, "Starting browser")
	eng, err := r.exec.launcher.Launch(ctx, opts)
	if err != nil {
		r.releaseProfile(ctx, opts)
		return 0, err
	}
	r.eng = eng
	handle := eng.Handle()
	r.record = browserhost.Record{
		ID:              recordID,
		DAGName:         r.dagName,
		DAGRunID:        r.dagRunID,
		StepName:        r.stepName,
		Generation:      generation,
		State:           browserhost.StateRunning,
		CDPURL:          handle.CDPURL,
		ExtensionID:     handle.ExtensionID,
		ExtensionDir:    handle.ExtensionDir,
		UserDataDir:     opts.UserDataDir,
		OwnsUserDataDir: r.profile == nil,
		Profile:         r.cfg.Browser.Profile,
		DownloadsDir:    opts.DownloadsDir,
		BrowserPID:      handle.BrowserPID,
	}
	r.record.BrowserStartedAt, _ = procutil.StartTime(handle.BrowserPID)
	if err := r.saveRunningRecord(); err != nil {
		return 0, err
	}
	if r.cfg.URL != "" {
		if err := r.gotoURL(ctx, -1, r.cfg.URL, defaultOperationTimeout); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func (r *run) launchOptions(ctx context.Context, recordID string) (launchOptions, error) {
	downloads, err := r.artifacts.downloadsDir()
	if err != nil {
		return launchOptions{}, err
	}
	opts := launchOptions{
		Executable:     r.cfg.Browser.Executable,
		Headless:       r.cfg.headless(),
		Viewport:       r.cfg.Browser.Viewport,
		Proxy:          r.cfg.Browser.Proxy,
		DownloadsDir:   downloads,
		AllowedDomains: r.cfg.Browser.AllowedDomains,
		NoSandbox:      cmnconfig.GetConfig(ctx).Browser.NoSandbox,
		Generate:       r.bridge.generate,
	}
	if name := r.cfg.Browser.Profile; name != "" {
		lease, err := acquireProfile(ctx, r.browser, name, r.store, recordID)
		if err != nil {
			return launchOptions{}, err
		}
		r.profile = lease
		opts.UserDataDir = lease.dir
		return opts, nil
	}
	dir, err := os.MkdirTemp("", "dagu-browser-")
	if err != nil {
		return launchOptions{}, fmt.Errorf("create browser profile directory: %w", err)
	}
	opts.UserDataDir = dir
	return opts, nil
}

// releaseProfile undoes launchOptions after a failed launch.
func (r *run) releaseProfile(_ context.Context, opts launchOptions) {
	if r.profile != nil {
		r.profile.release()
		r.profile = nil
		return
	}
	// A browser that failed to start can still hold profile files open for
	// a moment on Windows.
	_ = fileutil.RemoveAll(opts.UserDataDir)
}

func (r *run) saveRunningRecord() error {
	r.record.State = browserhost.StateRunning
	r.record.Deadline = time.Time{}
	r.record.OwnerPID = os.Getpid()
	r.record.OwnerStartedAt, _ = procutil.StartTime(r.record.OwnerPID)
	return r.store.Save(r.record)
}

func (r *run) runOperation(ctx context.Context, index int, op operation) error {
	timeout := op.timeout()
	switch {
	case op.Goto != "":
		return r.gotoURL(ctx, index, op.Goto, timeout)
	case op.Act != nil:
		return r.act(ctx, index, *op.Act, timeout)
	case op.Extract != nil:
		return r.extract(ctx, index, *op.Extract, timeout)
	case op.Expect != nil:
		return r.expect(ctx, index, *op.Expect, timeout)
	case op.Wait != nil:
		return r.wait(ctx, index, *op.Wait, timeout)
	case op.Screenshot != "":
		return r.screenshot(ctx, index, op.Screenshot)
	}
	return fmt.Errorf("unsupported operation %q", op.kind())
}

func (r *run) gotoURL(ctx context.Context, index int, target string, timeout time.Duration) error {
	began := time.Now()
	if err := checkAllowedDomain(target, r.cfg.Browser.AllowedDomains); err != nil {
		return err
	}
	if err := r.eng.Goto(ctx, target, timeout); err != nil {
		return err
	}
	r.report(ctx, operationReport{index: index, kind: opGoto, subject: target, status: statusCompleted, duration: time.Since(began)})
	return nil
}

func (r *run) act(ctx context.Context, index int, spec actSpec, timeout time.Duration) error {
	// Validation guarantees every reference names a variable or an earlier
	// ask, so a missing value means that ask was skipped.
	for _, name := range variableReferences(spec.Instruction) {
		if _, ok := r.variables[name]; !ok {
			return fmt.Errorf("the instruction uses %%%s%%, but the ask that sets it did not run", name)
		}
	}
	began, before := time.Now(), r.bridge.totals()
	useCache := r.cache != nil && (spec.Cache == nil || *spec.Cache)
	key := ""
	if useCache {
		pageURL, err := r.eng.CurrentURL(ctx)
		if err != nil {
			return err
		}
		key = replayKey(index, spec.Instruction, pageURL)
	}
	status := statusCompleted
	if actions, ok := r.lookupCache(key); ok {
		replayed, err := r.eng.Replay(ctx, actions, r.variables, timeout)
		if err != nil {
			return err
		}
		if replayed {
			r.report(ctx, operationReport{
				index: index, kind: opAct, subject: spec.Instruction, status: statusCacheHit,
				detail: describeActions(actions), duration: time.Since(began),
			})
			return nil
		}
		status = statusHealed
	}
	outcome, err := r.eng.Act(ctx, spec.Instruction, r.variables, timeout)
	if err != nil {
		return err
	}
	if !outcome.Success {
		if strings.Contains(outcome.Message, noActionFoundMessage) {
			return fmt.Errorf("the model (%s) answered that no element on the page matches the instruction; "+
				"if the element is on the page, reword the instruction or try another model, "+
				"since some models give this answer for every request", r.bridge.modelName())
		}
		return fmt.Errorf("act did not complete: %s", outcome.Message)
	}
	if useCache && len(outcome.Actions) > 0 {
		if err := r.cache.store(key, outcome.Actions); err != nil {
			return err
		}
	}
	r.report(ctx, operationReport{
		index: index, kind: opAct, subject: spec.Instruction, status: status,
		detail: describeActions(outcome.Actions), tokens: r.bridge.totals().sub(before).total(),
		duration: time.Since(began),
	})
	return nil
}

func (r *run) lookupCache(key string) ([]recordedAction, bool) {
	if key == "" {
		return nil, false
	}
	return r.cache.lookup(key)
}

func (r *run) extract(ctx context.Context, index int, spec extractSpec, timeout time.Duration) error {
	began, before := time.Now(), r.bridge.totals()
	schema, err := json.Marshal(spec.Schema)
	if err != nil {
		return fmt.Errorf("encode extract schema: %w", err)
	}
	data, err := r.eng.Extract(ctx, spec.Instruction, schema, timeout)
	if err != nil {
		return err
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("extract returned a non-object value: %w", err)
	}
	// Only the fields a schema lists are published, matching the output
	// names known when the DAG loads.
	properties, listed := spec.Schema["properties"].(map[string]any)
	for name, value := range values {
		if _, ok := properties[name]; listed && !ok {
			continue
		}
		r.outputs[name] = value
	}
	r.report(ctx, operationReport{
		index: index, kind: opExtract, subject: spec.Instruction, status: statusCompleted,
		detail: string(data), tokens: r.bridge.totals().sub(before).total(), duration: time.Since(began),
	})
	return nil
}

// expect fails unless the condition holds. A fixed check keeps reading the
// page until its within window or the operation timeout, because the page
// may still be updating.
func (r *run) expect(ctx context.Context, index int, c condition, timeout time.Duration) error {
	began, before := time.Now(), r.bridge.totals()
	holds, reason, err := r.await(ctx, c, c.window(timeout), timeout)
	if err != nil {
		return err
	}
	if !holds {
		return fmt.Errorf("expectation not met: %s", reason)
	}
	r.report(ctx, operationReport{
		index: index, kind: opExpect, subject: c.String(), status: statusCompleted, detail: reason,
		tokens: r.bridge.totals().sub(before).total(), duration: time.Since(began),
	})
	return nil
}

// await evaluates a condition. The model judges a statement once; a fixed
// check is read repeatedly until it holds or window passes. A zero window
// reads the page once.
func (r *run) await(ctx context.Context, c condition, window, timeout time.Duration) (bool, string, error) {
	deadline := time.Now().Add(window)
	for {
		holds, reason, err := r.evaluate(ctx, c, timeout)
		if err != nil || holds || c.judged() || !time.Now().Before(deadline) {
			return holds, reason, err
		}
		select {
		case <-ctx.Done():
			return false, "", ctx.Err()
		case <-time.After(conditionPollInterval):
		}
	}
}

// evaluate reports whether a condition holds now, and why.
func (r *run) evaluate(ctx context.Context, c condition, timeout time.Duration) (bool, string, error) {
	switch {
	case c.judged():
		return r.judge(ctx, c.Statement, timeout)
	case c.Text != "":
		text, err := r.eng.PageText(ctx)
		if err != nil {
			return false, "", err
		}
		if strings.Contains(text, c.Text) {
			return true, fmt.Sprintf("the page text contains %q", c.Text), nil
		}
		return false, fmt.Sprintf("the page text does not contain %q", c.Text), nil
	case c.Selector != "":
		visible, err := r.eng.SelectorVisible(ctx, c.Selector)
		if err != nil {
			return false, "", err
		}
		if visible {
			return true, fmt.Sprintf("%q is visible", c.Selector), nil
		}
		return false, fmt.Sprintf("%q is not visible", c.Selector), nil
	default:
		current, err := r.eng.CurrentURL(ctx)
		if err != nil {
			return false, "", err
		}
		if strings.Contains(current, c.URL) {
			return true, fmt.Sprintf("the URL contains %q", c.URL), nil
		}
		return false, fmt.Sprintf("the URL %s does not contain %q", current, c.URL), nil
	}
}

func (r *run) wait(ctx context.Context, index int, spec waitSpec, timeout time.Duration) error {
	began := time.Now()
	subject := spec.Selector
	if spec.Duration != "" {
		duration, err := time.ParseDuration(spec.Duration)
		if err != nil || duration <= 0 {
			return fmt.Errorf("wait duration %q must be a positive duration", spec.Duration)
		}
		subject = spec.Duration
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	} else if err := r.eng.WaitForSelector(ctx, spec.Selector, timeout); err != nil {
		return err
	}
	r.report(ctx, operationReport{index: index, kind: opWait, subject: subject, status: statusCompleted, duration: time.Since(began)})
	return nil
}

func (r *run) screenshot(ctx context.Context, index int, name string) error {
	began := time.Now()
	data, err := r.eng.Screenshot(ctx)
	if err != nil {
		return err
	}
	rel, err := r.artifacts.writeScreenshot(name, data)
	if err != nil {
		return err
	}
	r.timeline.operation(operationReport{
		index: index, kind: opScreenshot, subject: name, status: statusCompleted,
		detail: rel, duration: time.Since(began), files: []string{rel},
	})
	return nil
}

// judge asks the model whether a statement holds for the current page.
func (r *run) judge(ctx context.Context, statement string, timeout time.Duration) (bool, string, error) {
	instruction := fmt.Sprintf("Decide whether this statement is true for the current page: %q. Set answer to true or false and give a one-sentence reason.", statement)
	data, err := r.eng.Extract(ctx, instruction, statementSchema, timeout)
	if err != nil {
		return false, "", err
	}
	var verdict struct {
		Answer bool   `json:"answer"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &verdict); err != nil {
		return false, "", fmt.Errorf("decode statement verdict: %w", err)
	}
	return verdict.Answer, verdict.Reason, nil
}

// report records a finished operation, attaching a screenshot when every
// operation is captured.
func (r *run) report(ctx context.Context, report operationReport) {
	if r.cfg.screenshotPolicy() == screenshotsEach && r.artifacts.enabled() {
		if rel, err := r.capture(ctx, report.kind); err == nil {
			report.files = append(report.files, rel)
		}
	}
	r.timeline.operation(report)
}

func (r *run) capture(ctx context.Context, label string) (string, error) {
	if r.eng == nil {
		return "", errors.New("browser is not running")
	}
	data, err := r.eng.Screenshot(ctx)
	if err != nil {
		return "", err
	}
	return r.artifacts.writeScreenshot(label, data)
}

func (r *run) succeed(ctx context.Context) error {
	var files []string
	if r.cfg.capturesFinalScreenshot() && r.artifacts.enabled() {
		if rel, err := r.capture(ctx, finalShotLabel); err == nil {
			files = append(files, rel)
		}
	}
	// The operations succeeded; leftover browser files are reported, not
	// treated as a step failure.
	if err := r.shutdown(ctx); err != nil {
		_, _ = fmt.Fprintf(r.timeline.log, "warning: browser cleanup: %s\n", r.masker.MaskString(err.Error()))
	}
	usage := r.bridge.totals()
	summary := fmt.Sprintf("Completed %d operations using %d tokens", len(r.cfg.Do), usage.total())
	r.timeline.appendEvent(ir.AgentSessionEvent{Type: eventLifecycle, Status: statusCompleted, Content: summary, Files: files})
	_, _ = fmt.Fprintln(r.timeline.log, summary)
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionSucceeded
		s.Usage = ir.AgentUsage{InputTokens: int64(usage.Input), OutputTokens: int64(usage.Output), TotalTokens: int64(usage.total())}
	})
	r.exec.setOutputs(r.outputs)
	if len(r.outputs) > 0 {
		encoder := json.NewEncoder(r.exec.stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(r.outputs); err != nil {
			return err
		}
	}
	return nil
}

// fail captures the failure, closes the browser, and returns the masked
// error. index is -1 for failures outside an operation.
func (r *run) fail(ctx context.Context, index int, kind string, cause error) error {
	if ctx.Err() != nil && errors.Is(cause, context.Canceled) {
		cause = ctx.Err()
	}
	r.reportBlocked(index)
	var files []string
	if r.cfg.screenshotPolicy() != screenshotsNever && r.artifacts.enabled() && r.eng != nil {
		if rel, err := r.capture(context.WithoutCancel(ctx), failureShotLabel); err == nil {
			files = append(files, rel)
		}
	}
	_ = r.shutdown(ctx)
	message := r.masker.MaskString(cause.Error())
	if index >= 0 {
		message = fmt.Sprintf("do[%d] %s failed: %s", index, kind, message)
	}
	// A blocked request often breaks the page long before an operation
	// fails, so the failure summarizes every request blocked in the attempt.
	if len(r.blocked) > 0 {
		message += "; browser.allowed_domains blocked " + r.masker.MaskString(describeBlocked(r.blocked))
	}
	usage := r.bridge.totals()
	r.timeline.appendEvent(ir.AgentSessionEvent{Type: eventLifecycle, Status: statusFailed, Content: message, Files: files})
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionFailed
		s.LastError = message
		s.Usage = ir.AgentUsage{InputTokens: int64(usage.Input), OutputTokens: int64(usage.Output), TotalTokens: int64(usage.total())}
	})
	return errors.New("browser: " + message)
}

// shutdown closes the browser and removes everything it owned.
func (r *run) shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	var errs []error
	if r.eng != nil {
		errs = append(errs, r.eng.Close(ctx))
		r.eng = nil
	}
	if r.record.ID != "" {
		errs = append(errs, browserhost.Release(ctx, r.store, r.record))
		r.record = browserhost.Record{}
	}
	r.profile.release()
	r.profile = nil
	return errors.Join(errs...)
}

func (r *run) modelLabel() string {
	models := r.bridge.models
	if len(models) == 0 {
		return ""
	}
	return models[0].Provider + "/" + models[0].Name
}

func describeActions(actions []recordedAction) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		part := action.Method
		if part == "" {
			part = "act"
		}
		part += " " + action.Selector
		if len(action.Arguments) > 0 {
			part += " " + strings.Join(action.Arguments, " ")
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}
