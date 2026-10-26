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
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
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
	// operator performs the operations on the step's browser.
	operator
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
	cache     *replayCache
	artifacts *agentstep.ArtifactStore
	timeline  *agentstep.Timeline
	record    browserhost.Record
	profile   *profileLease
	// answers holds the values people gave to ask operations.
	answers map[string]string
	outputs map[string]any
	// blocked counts the requests allowed_domains blocked in this attempt,
	// by host.
	blocked map[string]int
	// blockedUncounted is set once blocked requests can no longer be
	// counted.
	blockedUncounted bool
	// certificateFailures holds, by host, the last trouble with a client
	// certificate the proxy reported in this attempt.
	certificateFailures map[string]string
	// refused is the refusal reported last, while the page still shows it.
	refused *refusal
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
	secrets = withCertificatePassphrases(secrets, e.cfg.Browser.ClientCertificates)
	if err := agentstep.CheckSecrets(executorType, e.cfg.operationTexts(), secrets); err != nil {
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
	masker := agentstep.NewMasker(secrets, nil)
	bridge, err := newModelBridge(ctx, e.step.LLM, masker, e.stderr, e.newProvider)
	if err != nil {
		return nil, err
	}
	browserDir := filepath.Join(dataDir, browserhost.DataDirName)
	variables := maps.Clone(e.cfg.Variables)
	if variables == nil {
		variables = map[string]string{}
	}
	artifacts := agentstep.NewArtifactStore(artifactsDir, artifactsSubdir, stepKey)
	r := &run{
		operator: operator{
			bridge:         bridge,
			variables:      variables,
			allowedDomains: e.cfg.Browser.AllowedDomains,
			choice:         e.cfg.operationChoice,
			shots:          artifacts,
			workDir:        env.WorkingDir,
		},
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
		artifacts: artifacts,
		answers:   map[string]string{},
		outputs:   map[string]any{},
		blocked:   map[string]int{},

		certificateFailures: map[string]string{},
	}
	r.cache = openReplayCache(browserDir, dagName, stepKey)
	r.recorder = stepRecorder{cache: r.cache}
	r.timeline = &agentstep.Timeline{Log: e.stderr, Masker: masker, Total: len(e.cfg.Do), Update: e.updateSession, Provider: providerName}
	r.warn = func(index int, message string) {
		_, _ = fmt.Fprintf(r.timeline.Log, "warning: %s %s\n", r.timeline.Position(index), message)
	}
	r.certificateEvent = func(index int, event certproxy.Event) {
		logCertificateEvent(r.timeline, index, event)
		if certificateTrouble(event) {
			r.certificateFailures[event.Host] = describeCertificateEvent(event)
		}
	}
	return r, nil
}

func (r *run) execute(ctx context.Context) error {
	if r.cfg.hasAsk() && !r.exec.askSupported {
		return errors.New("browser: ask operations are not supported on Windows, where the browser cannot outlive the step process")
	}
	sweepCtx, cancel := context.WithTimeout(ctx, sweepBudget)
	_ = browserhost.SweepAll(sweepCtx, r.browser, time.Now(), nil)
	cancel()

	start, err := r.startSession(ctx)
	r.reportDialogs(-1)
	r.reportBlocked(-1)
	if err == nil {
		err = r.checkPage(ctx, -1)
	}
	if err != nil {
		return r.fail(ctx, -1, "", err)
	}
	r.reportRefusal(ctx, -1)
	for i := start; i < len(r.cfg.Do); i++ {
		op := r.cfg.Do[i]
		if op.When != nil {
			began, before := time.Now(), r.bridge.totals()
			holds, reason, err := r.await(ctx, *op.When, op.When.window(0), op.timeout())
			if err != nil {
				return r.fail(ctx, i, op.kind(), fmt.Errorf("evaluate when: %w", err))
			}
			if !holds {
				r.timeline.Operation(agentstep.Report{
					Index: i, Kind: op.kind(), Subject: op.When.String(), Status: agentstep.StatusSkipped, Via: op.When.via(), Detail: reason,
					Tokens: r.bridge.totals().sub(before).total(), Duration: time.Since(began),
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
		result, err := r.runOperation(ctx, i, op)
		if err == nil {
			r.reportResult(ctx, result)
		}
		r.reportDialogs(i)
		r.reportBlocked(i)
		if err != nil {
			return r.fail(ctx, i, op.kind(), err)
		}
		if err := r.checkPage(ctx, i); err != nil {
			return r.fail(ctx, i, op.kind(), err)
		}
		r.reportRefusal(ctx, i)
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
		r.timeline.Operation(agentstep.Report{
			Index: index, Kind: kindDialog, Subject: d.Message, Status: agentstep.StatusCompleted, Detail: "accepted " + d.Type,
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
		_, _ = fmt.Fprintf(r.timeline.Log, "warning: stopped counting requests blocked by allowed_domains: %s\n",
			r.masker.MaskString(err.Error()))
	}
	if len(blocked) == 0 {
		return
	}
	for host, count := range blocked {
		r.blocked[host] += count
	}
	logBlocked(r.timeline, index, describeBlocked(blocked))
}

// reportRefusal records the refusal the page shows after the operation at
// index, once while the page keeps showing it, and returns it.
func (r *run) reportRefusal(ctx context.Context, index int) *refusal {
	if r.eng == nil {
		return nil
	}
	refused, ok := r.pageRefusal(ctx)
	if !ok {
		r.refused = nil
		return nil
	}
	if r.refused == nil || *r.refused != refused {
		r.refused = &refused
		logRefusal(r.timeline, index, refused)
	}
	return &refused
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
		rel := downloadPath(r.artifacts, name)
		r.timeline.Operation(agentstep.Report{
			Index: index, Kind: kindDownload, Subject: name, Status: agentstep.StatusCompleted,
			Detail: rel, Files: []string{rel},
		})
	}
	return err
}

// startSession launches a browser, or reattaches to the one an answered ask
// operation left running, and returns the first operation to run.
func (r *run) startSession(ctx context.Context) (int, error) {
	session := r.exec.GetAgentSession()
	recordID := browserhost.RecordID(r.dagRunID, r.stepName)
	answer, answered := agentstep.PendingAnswer(session, providerName)
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
	r.timeline.Lifecycle(agentstep.StatusRunning, "Starting browser")
	eng, err := r.exec.launcher.Launch(ctx, opts)
	if err != nil {
		r.releaseProfile(ctx, opts)
		return 0, err
	}
	r.eng = eng
	handle := eng.Handle()
	r.record = browserhost.Record{
		ID:               recordID,
		DAGName:          r.dagName,
		DAGRunID:         r.dagRunID,
		StepName:         r.stepName,
		Generation:       generation,
		State:            browserhost.StateRunning,
		CDPURL:           handle.CDPURL,
		ExtensionID:      handle.ExtensionID,
		ExtensionDir:     handle.ExtensionDir,
		UserDataDir:      opts.UserDataDir,
		OwnsUserDataDir:  r.profile == nil,
		Profile:          r.cfg.Browser.Profile,
		DownloadsDir:     opts.DownloadsDir,
		BrowserPID:       handle.BrowserPID,
		BrowserStartedAt: handle.BrowserStartedAt,
	}
	recordCertProxy(&r.record, handle.CertProxy)
	if err := r.saveRunningRecord(); err != nil {
		return 0, err
	}
	if r.cfg.URL != "" {
		result, err := r.gotoURL(ctx, -1, r.cfg.URL, defaultOperationTimeout)
		if err != nil {
			return 0, err
		}
		r.report(ctx, result.Report)
	}
	return 0, nil
}

func (r *run) launchOptions(ctx context.Context, recordID string) (launchOptions, error) {
	downloads, err := downloadsDir(r.artifacts)
	if err != nil {
		return launchOptions{}, err
	}
	certificates, warnings, err := loadClientCertificates(ctx, r.cfg.Browser.ClientCertificates, runtime.GetEnv(ctx).WorkingDir)
	if err != nil {
		return launchOptions{}, err
	}
	authorities, err := loadTrustedCAs(ctx)
	if err != nil {
		return launchOptions{}, err
	}
	for _, warning := range warnings {
		r.warn(-1, warning)
	}
	guard := guardsCertificates(ctx, certificates)
	r.certificateProxy = len(certificates) > 0 || len(authorities) > 0 || guard
	opts := launchOptions{
		Executable:     r.cfg.Browser.Executable,
		Headless:       r.cfg.headless(),
		Viewport:       r.cfg.Browser.Viewport,
		Proxy:          r.cfg.Browser.Proxy,
		DownloadsDir:   downloads,
		AllowedDomains: r.cfg.Browser.AllowedDomains,
		NoSandbox:      cmnconfig.GetConfig(ctx).Browser.NoSandbox,
		Generate:       r.bridge.generate,

		ClientCertificates: certificates,
		TrustedCAs:         authorities,
		Guard:              guard,
		Warn:               func(message string) { r.warn(-1, message) },
		BrowserDir:         r.browser,
		MaxOpen:            cmnconfig.GetConfig(ctx).Browser.MaxOpen,
		Waiting:            func() { r.timeline.Lifecycle(agentstep.StatusRunning, "Waiting for another browser to close") },
	}
	if name := r.cfg.Browser.Profile; name != "" {
		lease, err := acquireProfile(ctx, r.browser, name, recordID, true)
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

// reportResult records what an operation did and publishes the values an
// extract read. A screenshot operation is its own capture.
func (r *run) reportResult(ctx context.Context, result opResult) {
	maps.Copy(r.outputs, result.Values)
	if result.Kind == opScreenshot {
		r.timeline.Operation(result.Report)
		return
	}
	r.report(ctx, result.Report)
}

// report records a finished operation, attaching a screenshot when every
// operation is captured.
func (r *run) report(ctx context.Context, report agentstep.Report) {
	if r.cfg.screenshotPolicy() == screenshotsEach && r.artifacts.Enabled() {
		if rel, err := r.capture(ctx, report.Kind); err == nil {
			report.Files = append(report.Files, rel)
		}
	}
	r.timeline.Operation(report)
}

func (r *run) capture(ctx context.Context, label string) (string, error) {
	if r.eng == nil {
		return "", errors.New("browser is not running")
	}
	data, err := r.eng.Screenshot(ctx)
	if err != nil {
		return "", err
	}
	return r.artifacts.WriteScreenshot(label, data)
}

func (r *run) succeed(ctx context.Context) error {
	var files []string
	if r.cfg.capturesFinalScreenshot() && r.artifacts.Enabled() {
		if rel, err := r.capture(ctx, finalShotLabel); err == nil {
			files = append(files, rel)
		}
	}
	// The operations succeeded; leftover browser files are reported, not
	// treated as a step failure.
	if err := r.shutdown(ctx); err != nil {
		_, _ = fmt.Fprintf(r.timeline.Log, "warning: browser cleanup: %s\n", r.masker.MaskString(err.Error()))
	}
	if err := r.cache.Commit(ctx); err != nil {
		_, _ = fmt.Fprintf(r.timeline.Log, "warning: keep replay recordings: %s\n", r.masker.MaskString(err.Error()))
	}
	usage := r.bridge.totals()
	summary := fmt.Sprintf("Completed %d operations using %d tokens", len(r.cfg.Do), usage.total())
	r.timeline.AppendEvent(ir.AgentSessionEvent{Type: agentstep.EventLifecycle, Status: agentstep.StatusCompleted, Content: summary, Files: files})
	_, _ = fmt.Fprintln(r.timeline.Log, summary)
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
	r.takeCertificateEvents(ctx, index)
	// A page the site refused explains a failure that follows, however long
	// after the refusal.
	var refused *refusal
	if ctx.Err() == nil && !errors.Is(cause, errBrowserUnresponsive) {
		refused = r.reportRefusal(ctx, index)
	}
	var files []string
	if r.cfg.screenshotPolicy() != screenshotsNever && r.artifacts.Enabled() && r.eng != nil {
		if rel, err := r.capture(context.WithoutCancel(ctx), failureShotLabel); err == nil {
			files = append(files, rel)
		}
	}
	_ = r.shutdown(ctx)
	r.forgetReplays(ctx, index, kind, cause, refused != nil)
	message := r.masker.MaskString(cause.Error())
	if index >= 0 {
		message = fmt.Sprintf("do[%d] %s failed: %s", index, kind, message)
	}
	if refused != nil {
		if note := r.masker.MaskString(refused.String()); !strings.Contains(message, note) {
			message += "; " + note
		}
	}
	// A blocked request often breaks the page long before an operation
	// fails, so the failure summarizes every request blocked in the attempt.
	if len(r.blocked) > 0 {
		message += "; browser.allowed_domains blocked " + r.masker.MaskString(describeBlocked(r.blocked))
	}
	for _, host := range slices.Sorted(maps.Keys(r.certificateFailures)) {
		if failure := r.masker.MaskString(r.certificateFailures[host]); !strings.Contains(message, failure) {
			message += "; browser TLS: " + failure
		}
	}
	usage := r.bridge.totals()
	r.timeline.AppendEvent(ir.AgentSessionEvent{Type: agentstep.EventLifecycle, Status: agentstep.StatusFailed, Content: message, Files: files})
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionFailed
		s.LastError = message
		s.Usage = ir.AgentUsage{InputTokens: int64(usage.Input), OutputTokens: int64(usage.Output), TotalTokens: int64(usage.total())}
	})
	return errors.New("browser: " + message)
}

// forgetReplays settles the replay cache of a failed step. An operation that
// failed on the page may have followed a replay that did the wrong thing, so
// the recordings the step replayed are dropped. A failure of the model, the
// browser, a download, an ask, or the run itself says nothing about them,
// so they stay, as they do when the site refused the page. So does a replay
// that stopped because it no longer fit the page under ai: never: the
// recording is what a run under ai: on_miss repairs. What the step recorded
// is never kept.
func (r *run) forgetReplays(ctx context.Context, index int, kind string, cause error, refused bool) {
	if index < 0 || ctx.Err() != nil || refused || kind == opAsk || kind == kindDownload ||
		errors.Is(cause, errBrowserUnresponsive) || errors.Is(cause, errPageSessionLost) || r.bridge.failedRequest() ||
		errors.As(cause, new(replayMiss)) {
		r.cache.Discard()
		return
	}
	if err := r.cache.Evict(ctx); err != nil {
		_, _ = fmt.Fprintf(r.timeline.Log, "warning: drop replay recordings: %s\n", r.masker.MaskString(err.Error()))
	}
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
