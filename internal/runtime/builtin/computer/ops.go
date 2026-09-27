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
	computerDir string
	store       *computerhost.Store
	secrets     map[string]string
	masker      *masking.Masker
	models      []model
	usage       tokenUsage
	cache       *replayCache
	artifacts   *artifactStore
	timeline    *timeline
	driver      *desktop.Driver
	lease       *desktopLease
	variables   map[string]string
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
	models, err := newModels(ctx, e.step.LLM, e.newProvider)
	if err != nil {
		return nil, err
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
		computerDir: computerDir,
		store:       computerhost.NewStore(computerDir),
		secrets:     secrets,
		masker:      masker,
		models:      models,
		artifacts:   newArtifactStore(artifactsDir, stepKey),
		variables:   maps.Clone(e.cfg.Variables),
		answers:     map[string]string{},
		outputs:     map[string]any{},
	}
	if r.variables == nil {
		r.variables = map[string]string{}
	}
	if e.cfg.cacheEnabled() {
		if r.cache, err = openReplayCache(computerDir, dagName, stepKey); err != nil {
			return nil, err
		}
	}
	r.timeline = &timeline{log: e.stderr, masker: masker, total: len(e.cfg.Do), update: e.updateSession}
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
			holds, reason, err := r.await(ctx, *op.When, op.timeout())
			if err != nil {
				return r.fail(ctx, i, op.kind(), fmt.Errorf("evaluate when: %w", err))
			}
			if !holds {
				r.timeline.operation(operationReport{
					index: i, kind: op.kind(), subject: op.When.Statement, status: statusSkipped, detail: reason,
					tokens: r.usage.sub(before).total(), duration: time.Since(began),
				})
				continue
			}
		}
		if op.Ask != nil {
			return r.waitForInput(ctx, i, *op.Ask)
		}
		if err := r.runOperation(ctx, i, op); err != nil {
			return r.fail(ctx, i, op.kind(), err)
		}
	}
	return r.succeed(ctx)
}

// start takes the desktop and returns the first operation to run.
func (r *run) start(ctx context.Context) (int, error) {
	session := r.exec.GetAgentSession()
	recordID := computerhost.RecordID(r.dagRunID, r.stepName)
	cursor := 0
	if answer, answered := agentstep.PendingAnswer(session, providerName); answered {
		var err error
		if cursor, err = r.resume(recordID, session, answer); err != nil {
			return 0, err
		}
	} else {
		// A record left by an earlier attempt belongs to a pause that can no
		// longer be resumed.
		if err := r.store.Delete(recordID); err != nil {
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
			s.Model = r.models[0].label()
		})
	}

	lease, err := acquireDesktop(ctx, r.computerDir, r.timeline)
	if err != nil {
		return 0, err
	}
	r.lease = lease
	driver, err := r.exec.openDesktop()
	if err != nil {
		return 0, fmt.Errorf("open the desktop: %w", err)
	}
	r.driver = driver
	if cursor > 0 {
		r.timeline.lifecycle(statusRunning, "Resumed after input")
	} else {
		r.timeline.lifecycle(statusRunning, "Started on the desktop")
	}
	return cursor, nil
}

func (r *run) runOperation(ctx context.Context, index int, op operation) error {
	timeout := op.timeout()
	switch {
	case op.Launch != nil:
		return r.launch(ctx, index, *op.Launch)
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

func (r *run) launch(ctx context.Context, index int, spec launchSpec) error {
	began := time.Now()
	if err := r.exec.launch(spec.Command, spec.Args); err != nil {
		return err
	}
	subject := strings.Join(append([]string{spec.Command}, spec.Args...), " ")
	r.report(ctx, operationReport{index: index, kind: opLaunch, subject: subject, status: statusCompleted, duration: time.Since(began)})
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
	data, err := r.ask(ctx, spec.Instruction, spec.Schema, shot.image())
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
		detail: string(data), tokens: r.usage.sub(before).total(), duration: time.Since(began),
	})
	return nil
}

// expect fails unless the statement holds within its window.
func (r *run) expect(ctx context.Context, index int, c condition, timeout time.Duration) error {
	began, before := time.Now(), r.usage
	holds, reason, err := r.await(ctx, c, timeout)
	if err != nil {
		return err
	}
	if !holds {
		return fmt.Errorf("expectation not met: %s", reason)
	}
	r.report(ctx, operationReport{
		index: index, kind: opExpect, subject: c.Statement, status: statusCompleted, detail: reason,
		tokens: r.usage.sub(before).total(), duration: time.Since(began),
	})
	return nil
}

// await judges a statement against the screen, rechecking it until it holds
// or its within window passes.
func (r *run) await(ctx context.Context, c condition, timeout time.Duration) (bool, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline := time.Now().Add(c.window())
	for {
		full, err := r.settle(ctx)
		if err != nil {
			return false, "", err
		}
		shot, err := newScreen(full, visionLimit)
		if err != nil {
			return false, "", err
		}
		holds, reason, err := r.judge(ctx, c.Statement, shot.image())
		if err != nil || holds || !time.Now().Before(deadline) {
			return holds, reason, err
		}
		if err := sleep(ctx, conditionPollInterval); err != nil {
			return false, "", err
		}
	}
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
	r.report(ctx, operationReport{index: index, kind: opWait, subject: value, status: statusCompleted, duration: time.Since(began)})
	return nil
}

func (r *run) screenshot(ctx context.Context, index int, name string) error {
	began := time.Now()
	rel, err := r.capture(ctx, name)
	if err != nil {
		return err
	}
	r.timeline.operation(operationReport{
		index: index, kind: opScreenshot, subject: name, status: statusCompleted,
		detail: rel, duration: time.Since(began), files: []string{rel},
	})
	return nil
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

// capture saves the current screen as an artifact.
func (r *run) capture(ctx context.Context, label string) (string, error) {
	if r.driver == nil {
		return "", errors.New("the desktop is not open")
	}
	if !r.artifacts.enabled() {
		return "", errNoArtifactStorage
	}
	full, err := r.settle(ctx)
	if err != nil {
		return "", err
	}
	shot, err := newScreen(full, computeruse.ImageLimit{LongEdge: artifactLongEdge})
	if err != nil {
		return "", err
	}
	return r.artifacts.writeScreenshot(label, shot.png)
}

func (r *run) succeed(ctx context.Context) error {
	var files []string
	if r.cfg.capturesFinalScreenshot() && r.artifacts.enabled() {
		if rel, err := r.capture(ctx, finalShotLabel); err == nil {
			files = append(files, rel)
		}
	}
	r.shutdown()
	summary := fmt.Sprintf("Completed %d operations using %d tokens", len(r.cfg.Do), r.usage.total())
	r.timeline.appendEvent(ir.AgentSessionEvent{Type: eventLifecycle, Status: statusCompleted, Content: summary, Files: files})
	_, _ = fmt.Fprintln(r.timeline.log, summary)
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
	if r.cfg.screenshotPolicy() != screenshotsNever && r.artifacts.enabled() && r.driver != nil {
		if rel, err := r.capture(context.WithoutCancel(ctx), failureShotLabel); err == nil {
			files = append(files, rel)
		}
	}
	r.shutdown()
	message := r.masker.MaskString(cause.Error())
	if index >= 0 {
		message = fmt.Sprintf("do[%d] %s failed: %s", index, kind, message)
	}
	r.timeline.appendEvent(ir.AgentSessionEvent{Type: eventLifecycle, Status: statusFailed, Content: message, Files: files})
	r.exec.updateSession(func(s *ir.AgentSession) {
		s.State = ir.AgentSessionFailed
		s.LastError = message
		s.Usage = r.agentUsage()
	})
	return errors.New("computer: " + message)
}

// shutdown closes the desktop and lets other steps use it.
func (r *run) shutdown() {
	if r.driver != nil {
		_ = r.driver.Close()
		r.driver = nil
	}
	r.lease.release()
	r.lease = nil
}

func (r *run) agentUsage() ir.AgentUsage {
	return ir.AgentUsage{InputTokens: int64(r.usage.Input), OutputTokens: int64(r.usage.Output), TotalTokens: int64(r.usage.total())}
}
