// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	// conditionPollInterval spaces the checks of a statement with within.
	conditionPollInterval = 2 * time.Second
	// exactPollInterval spaces the looks of an exact check, which reads
	// the window's elements and costs no model turn.
	exactPollInterval = 250 * time.Millisecond
	// defaultIdlePoll spaces the checks for a person using the desktop.
	defaultIdlePoll = 500 * time.Millisecond
	// artifactLongEdge keeps saved screenshots readable at a modest size.
	artifactLongEdge = 1920
	finalShotLabel   = "final"
	failureShotLabel = "failure"
)

// visionLimit fits screenshots sent for extract and expect.
var visionLimit = computeruse.ImageLimit{LongEdge: visionLimitLongEdge, MaxPixels: visionLimitPixels}

// run executes one step attempt: from the first operation, or from the one
// after an ask a person answered.
type run struct {
	exec        *computerExecutor
	cfg         config
	dagName     string
	dagRunID    string
	stepName    string
	workerID    string
	workingDir  string
	computerDir string
	store       *computerhost.Store
	secrets     map[string]string
	masker      *masking.Masker
	models      []model
	usage       tokenUsage
	cache       *replayCache
	artifacts   *agentstep.ArtifactStore
	timeline    *agentstep.Timeline
	driver      *desktop.Driver
	// elements reads the front window for exact checks, opened on the
	// first one.
	elements  desktop.Elements
	lease     *desktopLease
	variables map[string]string
	// answers holds the values people gave to ask operations.
	answers map[string]string
	outputs map[string]any
}

func newRun(ctx context.Context, e *computerExecutor) (*run, error) {
	env := runtime.GetEnv(ctx)
	dataDir := cmnconfig.GetConfig(ctx).Paths.DataDir
	if dataDir == "" {
		return nil, errors.New("computer: the Dagu data directory is not configured")
	}
	var secrets map[string]string
	artifactsDir := ""
	if env.Scope != nil {
		secrets = env.Scope.AllSecrets()
		artifactsDir, _ = env.Scope.Get(runenv.EnvKeyDAGRunArtifactsDir)
	}
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
	// A step whose operations never call the model has none configured.
	var models []model
	if e.step.LLM != nil {
		resolved, err := newModels(ctx, e.step.LLM, e.newProvider)
		if err != nil {
			return nil, err
		}
		models = resolved
	}
	masker := agentstep.NewMasker(secrets, nil)
	computerDir := filepath.Join(dataDir, computerhost.DataDirName)
	r := &run{
		exec:        e,
		cfg:         e.cfg,
		dagName:     dagName,
		dagRunID:    env.DAGRunID,
		stepName:    e.step.Name,
		workerID:    env.WorkerID,
		workingDir:  env.WorkingDir,
		computerDir: computerDir,
		store:       computerhost.NewStore(computerDir),
		secrets:     secrets,
		masker:      masker,
		models:      models,
		artifacts:   agentstep.NewArtifactStore(artifactsDir, artifactsSubdir, stepKey),
		variables:   maps.Clone(e.cfg.Variables),
		answers:     map[string]string{},
		outputs:     map[string]any{},
	}
	if r.variables == nil {
		r.variables = map[string]string{}
	}
	r.cache = openReplayCache(computerDir, dagName, stepKey)
	r.timeline = &agentstep.Timeline{Log: e.stderr, Masker: masker, Total: len(e.cfg.Do), Update: e.updateSession, Provider: providerName}
	return r, nil
}

func (r *run) execute(ctx context.Context) error {
	start, err := r.start(ctx)
	if err != nil {
		return r.fail(ctx, -1, "", err)
	}
	for i := start; i < len(r.cfg.Do); i++ {
		op := r.cfg.Do[i]
		if op.When != nil {
			began, before := time.Now(), r.usage
			// A when reads once unless it says how long to keep looking.
			c, err := r.resolve(*op.When)
			var holds bool
			var reason string
			if err == nil {
				holds, reason, err = r.await(ctx, c, c.window(0), op.timeout())
			}
			if err != nil {
				err = fmt.Errorf("evaluate when: %w", err)
				r.reportFailure(i, op.kind(), op.When.String(), err, began, before)
				return r.fail(ctx, i, op.kind(), err)
			}
			if !holds {
				r.timeline.Operation(agentstep.Report{
					Index: i, Kind: op.kind(), Subject: op.When.String(), Status: agentstep.StatusSkipped, Via: c.via(), Detail: reason,
					Tokens: r.usage.sub(before).total(), Duration: time.Since(began),
				})
				continue
			}
		}
		if op.Ask != nil {
			return r.waitForInput(ctx, i, *op.Ask)
		}
		began, before := time.Now(), r.usage
		if err := r.runOperation(ctx, i, op); err != nil {
			r.reportFailure(i, op.kind(), op.subject(), err, began, before)
			return r.fail(ctx, i, op.kind(), err)
		}
	}
	return r.succeed(ctx)
}

// reportFailure records the operation that failed with how it ran and what
// it cost, so a run attributes the model turns a failure used to it. The
// step's failure that follows carries the screenshot.
func (r *run) reportFailure(index int, kind, subject string, cause error, began time.Time, before tokenUsage) {
	tokens := r.usage.sub(before).total()
	via := ""
	var unmet *conditionFailure
	switch {
	case tokens > 0:
		via = agentstep.ViaModel
	case errors.As(cause, new(replayMiss)):
		via = agentstep.ViaScreen
	case errors.As(cause, &unmet):
		via = unmet.via
	}
	r.timeline.Operation(agentstep.Report{
		Index: index, Kind: kind, Subject: subject, Status: agentstep.StatusFailed, Via: via,
		Detail: cause.Error(), Tokens: tokens, Duration: time.Since(began),
	})
}

// start takes the desktop and returns the first operation to run.
func (r *run) start(ctx context.Context) (int, error) {
	session := r.exec.GetAgentSession()
	cursor := 0
	if answer, answered := agentstep.PendingAnswer(session, providerName); answered {
		var err error
		if cursor, err = r.resume(session, answer); err != nil {
			return 0, err
		}
	} else {
		// A record left by an earlier attempt belongs to a pause that can no
		// longer be resumed.
		if err := r.store.Delete(r.dagRunID, r.stepName); err != nil {
			return 0, err
		}
		r.exec.updateSession(func(s *ir.AgentSession) {
			s.Provider = providerName
			if s.Generation < 1 {
				s.Generation = 1
			}
			if len(s.Interactions) > 0 {
				s.Generation++
				s.Interactions = nil
			}
			s.State = ir.AgentSessionRunning
			s.RestartPending = false
			s.PromptSent = true
			s.OwnerWorkerID = r.workerID
			s.LastError = ""
			if len(r.models) > 0 {
				s.Model = r.models[0].label()
			}
		})
	}

	lockDir := r.exec.desktopLock
	if lockDir == "" {
		lockDir = filepath.Join(r.computerDir, desktopLockName)
	}
	lease, err := acquireDesktop(ctx, lockDir, r.timeline)
	if err != nil {
		return 0, err
	}
	r.lease = lease
	driver, err := r.exec.openDesktop()
	if err != nil {
		return 0, fmt.Errorf("open the desktop: %w", err)
	}
	r.driver = driver
	r.driver.AssumeInputSent(r.lease.lastInput())
	if cursor > 0 {
		r.timeline.Lifecycle(agentstep.StatusRunning, "Resumed after input")
	} else {
		r.timeline.Lifecycle(agentstep.StatusRunning, "Started on the desktop")
	}
	return cursor, nil
}

func (r *run) runOperation(ctx context.Context, index int, op operation) error {
	timeout := op.timeout()
	switch {
	case op.Launch != nil:
		return r.launch(ctx, index, *op.Launch, timeout)
	case op.Act != nil:
		return r.act(ctx, index, *op.Act, timeout)
	case op.Extract != nil:
		return r.extract(ctx, index, *op.Extract, timeout)
	case op.Expect != nil:
		return r.expect(ctx, index, *op.Expect, timeout)
	case op.Wait != "":
		return r.wait(ctx, index, op.Wait)
	case op.Screenshot != "":
		return r.screenshot(ctx, index, op.Screenshot)
	}
	return fmt.Errorf("unsupported operation %q", op.kind())
}

func (r *run) launch(ctx context.Context, index int, spec launchSpec, timeout time.Duration) error {
	began := time.Now()
	// A new window takes the keyboard focus from a person who is typing.
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := r.awaitPerson(waitCtx); err != nil {
		return err
	}
	if err := r.exec.launch(r.workingDir, spec.Command, spec.Args); err != nil {
		return err
	}
	subject := strings.Join(append([]string{spec.Command}, spec.Args...), " ")
	r.report(ctx, agentstep.Report{Index: index, Kind: opLaunch, Subject: subject, Status: agentstep.StatusCompleted, Duration: time.Since(began)})
	return nil
}

func (r *run) extract(ctx context.Context, index int, spec extractSpec, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	began, before := time.Now(), r.usage
	full, err := r.settle(ctx)
	if err != nil {
		return err
	}
	shot, err := newScreen(full, visionLimit)
	if err != nil {
		return err
	}
	data, err := r.query(ctx, spec.Instruction, spec.Schema, shot.image())
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
	r.report(ctx, agentstep.Report{
		Index: index, Kind: opExtract, Subject: spec.Instruction, Status: agentstep.StatusCompleted, Via: agentstep.ViaModel,
		Detail: string(data), Tokens: r.usage.sub(before).total(), Duration: time.Since(began),
	})
	return nil
}

// conditionFailure is an expect that did not hold, with how it was decided.
type conditionFailure struct {
	reason string
	via    string
}

func (f *conditionFailure) Error() string { return "expectation not met: " + f.reason }

// expect fails unless the condition holds. A statement is judged once
// unless it says how long to keep rechecking; an exact check keeps looking
// until its within or the operation timeout, since the screen may still be
// changing.
func (r *run) expect(ctx context.Context, index int, spec condition, timeout time.Duration) error {
	began, before := time.Now(), r.usage
	c, err := r.resolve(spec)
	if err != nil {
		return err
	}
	fallback := time.Duration(0)
	if !c.judged() {
		fallback = timeout
	}
	holds, reason, err := r.await(ctx, c, c.window(fallback), timeout)
	if err != nil {
		return err
	}
	if !holds {
		return &conditionFailure{reason: reason, via: c.via()}
	}
	r.report(ctx, agentstep.Report{
		Index: index, Kind: opExpect, Subject: spec.String(), Status: agentstep.StatusCompleted, Via: c.via(), Detail: reason,
		Tokens: r.usage.sub(before).total(), Duration: time.Since(began),
	})
	return nil
}

// resolve replaces the placeholders of an exact check. Validation
// guarantees every reference names a variable or an earlier ask, so a
// missing value means that ask was skipped; an empty value would leave the
// check with nothing to look for.
func (r *run) resolve(spec condition) (condition, error) {
	for _, name := range spec.references() {
		value, ok := r.variables[name]
		if !ok {
			return condition{}, fmt.Errorf("the check uses %%%s%%, but the ask that sets it did not run", name)
		}
		if value == "" {
			return condition{}, fmt.Errorf("the check uses %%%s%%, but its value is empty", name)
		}
	}
	return spec.substituted(r.variables), nil
}

// await evaluates a condition until it holds or window passes, within the
// operation timeout. When the timeout cuts an exact check short, the last
// reason stands as the answer rather than the deadline.
func (r *run) await(ctx context.Context, c condition, window, timeout time.Duration) (bool, string, error) {
	deadline := time.Now().Add(window)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	interval := conditionPollInterval
	if !c.judged() {
		interval = exactPollInterval
	}
	for {
		holds, reason, err := r.evaluate(ctx, c)
		if err != nil || holds || !time.Now().Before(deadline) {
			return holds, reason, err
		}
		if err := sleep(ctx, min(interval, time.Until(deadline))); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && !c.judged() {
				return false, reason, nil
			}
			return false, "", err
		}
	}
}

// evaluate reports whether a condition holds now, and why. A statement is
// judged against a screenshot; an exact check reads the front window's
// elements and takes no screenshot.
func (r *run) evaluate(ctx context.Context, c condition) (bool, string, error) {
	if c.judged() {
		full, err := r.settle(ctx)
		if err != nil {
			return false, "", err
		}
		shot, err := newScreen(full, visionLimit)
		if err != nil {
			return false, "", err
		}
		return r.judge(ctx, c.Statement, shot.image())
	}
	els, err := r.elementsReader()
	if err != nil {
		return false, "", err
	}
	front, err := els.FrontWindow()
	if err != nil {
		return false, "", err
	}
	switch {
	case c.Text != "":
		elements, err := els.Outline(front, 0)
		if errors.Is(err, desktop.ErrNoElements) {
			return false, fmt.Sprintf("%q exposes no elements", front.Name), nil
		}
		if err != nil {
			return false, "", err
		}
		for _, e := range elements {
			if strings.Contains(e.Name, c.Text) || strings.Contains(e.Value, c.Text) {
				return true, fmt.Sprintf("the window shows %q", c.Text), nil
			}
		}
		return false, fmt.Sprintf("no element of %q shows %q", front.Name, c.Text), nil
	case c.Element != nil:
		sel := *c.Element
		matches, err := els.Find(sel)
		switch {
		case errors.Is(err, desktop.ErrNoElements):
			return false, fmt.Sprintf("%q exposes no elements", front.Name), nil
		case errors.Is(err, desktop.ErrNotFound) || errors.Is(err, desktop.ErrAmbiguous):
			// The container the selector names is the trouble.
			return false, fmt.Sprintf("%s is not there: %v", sel, err), nil
		case err != nil:
			return false, "", err
		}
		if _, err := desktop.One(matches, sel); err != nil {
			if ambiguous, ok := errors.AsType[*desktop.AmbiguousError](err); ok {
				return false, fmt.Sprintf("%s is not there; %d elements match", sel, ambiguous.Count), nil
			}
			return false, fmt.Sprintf("%s is not there", sel), nil
		}
		return true, fmt.Sprintf("%s is there", sel), nil
	default:
		if desktop.WindowMatches(front.Name, c.Window) {
			return true, fmt.Sprintf("the window %q is in front", front.Name), nil
		}
		return false, fmt.Sprintf("%q is in front, not %q", front.Name, c.Window), nil
	}
}

// elementsReader opens the elements reader on the first exact check and
// keeps it for the run.
func (r *run) elementsReader() (desktop.Elements, error) {
	if r.elements != nil {
		return r.elements, nil
	}
	els, err := r.exec.openElements()
	if err != nil {
		if errors.Is(err, desktop.ErrElementsUnsupported) {
			return nil, fmt.Errorf("exact checks need 64-bit Windows: %w", err)
		}
		return nil, fmt.Errorf("open the elements: %w", err)
	}
	r.elements = els
	return els, nil
}

func (r *run) wait(ctx context.Context, index int, value string) error {
	began := time.Now()
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fmt.Errorf("wait %q must be a positive duration", value)
	}
	if err := sleep(ctx, duration); err != nil {
		return err
	}
	r.report(ctx, agentstep.Report{Index: index, Kind: opWait, Subject: value, Status: agentstep.StatusCompleted, Duration: time.Since(began)})
	return nil
}

func (r *run) screenshot(ctx context.Context, index int, name string) error {
	began := time.Now()
	rel, err := r.capture(ctx, name)
	if err != nil {
		return err
	}
	r.timeline.Operation(agentstep.Report{
		Index: index, Kind: opScreenshot, Subject: name, Status: agentstep.StatusCompleted,
		Detail: rel, Duration: time.Since(began), Files: []string{rel},
	})
	return nil
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

// capture saves the current screen as an artifact.
func (r *run) capture(ctx context.Context, label string) (string, error) {
	if r.driver == nil {
		return "", errors.New("the desktop is not open")
	}
	if !r.artifacts.Enabled() {
		return "", agentstep.ErrNoArtifactStorage
	}
	full, err := r.settle(ctx)
	if err != nil {
		return "", err
	}
	shot, err := newScreen(full, computeruse.ImageLimit{LongEdge: artifactLongEdge})
	if err != nil {
		return "", err
	}
	return r.artifacts.WriteScreenshot(label, shot.png)
}

func (r *run) succeed(ctx context.Context) error {
	var files []string
	if r.cfg.capturesFinalScreenshot() && r.artifacts.Enabled() {
		if rel, err := r.capture(ctx, finalShotLabel); err == nil {
			files = append(files, rel)
		}
	}
	r.shutdown()
	if err := r.cache.Commit(ctx); err != nil {
		_, _ = fmt.Fprintf(r.timeline.Log, "warning: keep replay recordings: %s\n", r.masker.MaskString(err.Error()))
	}
	summary := fmt.Sprintf("Completed %d operations using %d tokens", len(r.cfg.Do), r.usage.total())
	r.timeline.AppendEvent(ir.AgentSessionEvent{Type: agentstep.EventLifecycle, Status: agentstep.StatusCompleted, Content: summary, Files: files})
	_, _ = fmt.Fprintln(r.timeline.Log, summary)
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionSucceeded
		s.Usage = r.agentUsage()
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

// fail captures the failure, releases the desktop, and returns the masked
// error. index is -1 for failures outside an operation.
func (r *run) fail(ctx context.Context, index int, kind string, cause error) error {
	if ctx.Err() != nil && errors.Is(cause, context.Canceled) {
		cause = ctx.Err()
	}
	var files []string
	if r.cfg.screenshotPolicy() != screenshotsNever && r.artifacts.Enabled() && r.driver != nil {
		if rel, err := r.capture(context.WithoutCancel(ctx), failureShotLabel); err == nil {
			files = append(files, rel)
		}
	}
	r.shutdown()
	r.forgetReplays(ctx, index, kind, cause)
	message := r.masker.MaskString(cause.Error())
	if index >= 0 {
		message = fmt.Sprintf("do[%d] %s failed: %s", index, kind, message)
	}
	r.timeline.AppendEvent(ir.AgentSessionEvent{Type: agentstep.EventLifecycle, Status: agentstep.StatusFailed, Content: message, Files: files})
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionFailed
		s.LastError = message
		s.Usage = r.agentUsage()
	})
	return errors.New("computer: " + message)
}

// errDesktopInUse reports a person who kept using the desktop until the
// operation timed out.
var errDesktopInUse = errors.New("a person kept using the desktop until the operation timed out")

// awaitPerson waits until nobody has used the desktop for the idle period,
// so the step's input does not collide with a person's.
func (r *run) awaitPerson(ctx context.Context) error {
	idle := r.cfg.idle()
	if idle <= 0 {
		return nil
	}
	err := r.driver.WaitForIdle(ctx, idle, r.exec.idlePoll, func() {
		r.timeline.Waiting(waitReasonPerson, fmt.Sprintf("Waiting until nobody has used the desktop for %s", idle))
	})
	if errors.Is(err, context.DeadlineExceeded) {
		return errDesktopInUse
	}
	return err
}

// forgetReplays settles the replay cache of a failed step. An operation that
// failed on the screen may have followed a replay that did the wrong thing,
// so the recordings the step replayed are dropped. A failure of the model,
// the screen capture, a launch, an ask, a person using the desktop, or the
// run itself says nothing about them, so they stay. So does a replay that
// stopped because it no longer fit the screen under ai: never: the
// recording is what a run under ai: on_miss repairs. What the step recorded
// is never kept.
func (r *run) forgetReplays(ctx context.Context, index int, kind string, cause error) {
	if index < 0 || ctx.Err() != nil || kind == opAsk || kind == opLaunch ||
		errors.Is(cause, errCapture) || errors.Is(cause, errDesktopInUse) || errors.Is(cause, desktop.ErrElementsUnsupported) ||
		errors.As(cause, new(modelFailure)) || errors.As(cause, new(replayMiss)) {
		r.cache.Discard()
		return
	}
	if err := r.cache.Evict(ctx); err != nil {
		_, _ = fmt.Fprintf(r.timeline.Log, "warning: drop replay recordings: %s\n", r.masker.MaskString(err.Error()))
	}
}

// shutdown closes the desktop and lets other steps use it.
func (r *run) shutdown() {
	if r.elements != nil {
		_ = r.elements.Close()
		r.elements = nil
	}
	if r.driver != nil {
		r.lease.recordInput(r.driver.InputSentAt())
		_ = r.driver.Close()
		r.driver = nil
	}
	r.lease.release()
	r.lease = nil
}

func (r *run) agentUsage() ir.AgentUsage {
	return ir.AgentUsage{InputTokens: int64(r.usage.Input), OutputTokens: int64(r.usage.Output), TotalTokens: int64(r.usage.total())}
}
