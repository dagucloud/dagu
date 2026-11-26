// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/humantask"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
)

// caseOptions configures one workflow-test case.
type caseOptions struct {
	// tempDir is where the case directory is created. Empty uses the default
	// directory for temporary files.
	tempDir string
	// keep leaves the case directory in place when the case is closed.
	keep bool
	// secrets holds the test value of each DAG secret by name, and of each
	// SSH credential secret ref by ref. No secret provider or store is
	// called. A case with a real step needs a value for every one of them,
	// and a case without one fills in placeholders (see caseSecrets).
	secrets map[string]string
	// trigger is the run's context.trigger.type. The zero value is manual.
	trigger ir.TriggerType
	// real names, by step name, the steps that run for real. Their redirect
	// files are written at their configured paths, and a case with one sets
	// up what a real step needs, as a real run does.
	real []string
	// mocks holds the scripted steps of the case by step name. A step in
	// real keeps its own action and takes only its step fields, such as its
	// review.
	mocks map[string]stepmock.Step
	// handlers holds the scripted lifecycle handlers of the case by handler
	// type. A handler marked Real runs for real.
	handlers map[ir.HandlerType]stepmock.Step
	// dagPrecondition is the outcome of the DAG-level preconditions that run
	// a command, or nil to run them.
	dagPrecondition *bool
	// slot is the schedule slot of the run, and dates the dates it resolves.
	slot  runSlot
	dates []ir.DateValue
}

// testCase is one run of a DAG that keeps its run history, logs, artifacts,
// state, materialization records, live-process records, locks, and step
// redirect files in a case directory instead of the configured data
// directory, so no live run of the DAG sees the case's run, and the case's
// run sees none.
type testCase struct {
	ctx  *Context
	dir  string
	opts caseOptions
	// dag is the DAG the case runs, set when its run starts.
	dag *ir.DAG
	// notifications lists the mail notifications the case's runs would have
	// sent, in the order the runs reached them. A case sends no mail.
	notifications []agent.Notification
	// autoApproved names, in the order the run settled them, the steps whose
	// waits the case approved automatically because their mocks set no
	// review.
	autoApproved []string
	// attempts counts the run attempts the case started: the first and each
	// retry of its DAG-level retry_policy. A resume after a wait is part of
	// the attempt it resumes.
	attempts int
	// waited holds, in order, the statuses at which the last run attempt
	// waited before it resumed. A handler that ran before a resume, such as
	// handler_on.wait, shows only there.
	waited []*ir.DAGRunStatus
	// ended holds, in order, every status at which an attempt or a resume
	// of the run stopped: waiting, failed with a retry left, or finished.
	// The executions of every attempt show only across them.
	ended []*ir.DAGRunStatus
	// streamStarts maps a step log file to where the latest execution that
	// wrote it starts, as the attempt that ran the execution recorded it.
	streamStarts map[string]int64
	// scripts scripts the steps of every attempt of the run and records
	// what each execution was called with. It is set when the run starts.
	scripts *stepmock.Set
}

// openCase creates a case directory and the stores rooted in it. The case
// runs under ctx: canceling ctx stops its run.
func openCase(ctx *Context, opts caseOptions) (*testCase, error) {
	dir, err := os.MkdirTemp(opts.tempDir, "dagu-case-")
	if err != nil {
		return nil, fmt.Errorf("failed to create case directory: %w", err)
	}
	caseCtx, err := ctx.inCaseDir(dir)
	if err != nil {
		return nil, errors.Join(err, fileutil.RemoveAll(dir))
	}
	return &testCase{ctx: caseCtx, dir: dir, opts: opts}, nil
}

// run runs dag through the local execution path of dagu start and returns
// the status the run recorded. A run that fails or is aborted returns its
// status and no error. In a case with a real step, a DAG secret or SSH
// credential ref without a test value is an error before the run starts.
//
// Each time the run reaches a waiting checkpoint, the case settles its
// waiting steps one at a time through the completion path a person uses.
// While the run has failed and its DAG-level retry_policy has a retry left,
// the case accepts the retry the scheduler would, at once instead of after
// the policy's interval. It runs each resume or retry that is queued, under
// the same run ID, and returns the last attempt's status. A settled step
// that queues no resume leaves the next waiting step to settle. Once the
// case's context is canceled, the case accepts no further retry, and an
// attempt that starts after the cancellation is interrupted at once.
//
//	attempt --> status
//	   ^          |-- waiting ------------> settle one step --+
//	   |          |-- failed, retry left -> accept retry -----+
//	   |          +-- otherwise ----------> return status     |
//	   |                                                      v
//	   +------------------ yes ------------------------ queued?
func (c *testCase) run(dag *ir.DAG) (*ir.DAGRunStatus, error) {
	secrets, err := caseSecrets(dag, c.opts.secrets, c.hasRealSteps())
	if err != nil {
		return nil, err
	}
	trigger, err := caseTrigger(c.opts.trigger)
	if err != nil {
		return nil, err
	}
	dagRunID, err := genRunID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate dag-run ID: %w", err)
	}
	dag = caseDAG(dag)
	c.dag = dag

	ctx := c.ctx
	if err := ctx.DAGRunMgr.CheckServiceRun(ctx, dag, dagRunID); err != nil {
		return nil, err
	}
	scripts := c.newScripts()
	c.scripts = scripts
	opts := runOptions{
		root:           ir.NewDAGRunRef(dag.Name, dagRunID),
		workerID:       "local",
		triggerType:    trigger,
		scheduleTime:   c.opts.slot.scheduleTime,
		scheduleWindow: c.opts.slot.window,
		defaultSlot:    c.opts.slot.isDefault,
		dates:          c.opts.dates,
		secretValues:   secrets,
		redirectRoot: runtime.RedirectRoot{
			Dir:  filepath.Join(c.dir, "redirects"),
			Keep: c.opts.real,
		},
		testSideEffects:   &agent.TestSideEffects{HasRealSteps: c.hasRealSteps()},
		executorOverride:  scripts,
		conditionOverride: scripts,
		skipWaits:         true,
		keepWaits:         c.opts.real,
	}
	runErr := withPreparedLocalExecution(
		ctx,
		dag,
		dagRunID,
		opts,
		func(execCtx context.Context) (dagrun.Attempt, error) {
			return ctx.Persistence.DAGRunRepository.CreateAttempt(execCtx, dag, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{})
		},
		func(preparedAttempt dagrun.Attempt) error {
			prepared := opts
			prepared.preparedAttempt = preparedAttempt
			return withDAGRunAgent(ctx, dag, dagRunID, prepared, c.runAgent)
		},
	)

	ref := ir.NewDAGRunRef(dag.Name, dagRunID)
	status, err := c.latestStatus(ref)
	if err != nil {
		return nil, errors.Join(runErr, err)
	}
	c.attempts = 1
	for {
		c.ended = append(c.ended, status)
		switch {
		case status.Status == ir.Waiting:
			c.waited = append(c.waited, status)
			err = c.settle(dag, status)
		// A failed root run with retries left is what the scheduler retries.
		case ctx.Err() == nil && dagrun.CanCancelFailedAutoRetryPendingRun(status):
			c.waited = nil
			err = c.acceptRetry(dag, status)
		default:
			return status, nil
		}
		if err != nil {
			return nil, err
		}
		if status, err = c.latestStatus(ref); err != nil {
			return nil, err
		}
		if status.Status != ir.Queued {
			continue
		}
		queuedErr := c.runQueued(dag, status, opts)
		if status, err = c.latestStatus(ref); err != nil {
			return nil, errors.Join(queuedErr, err)
		}
		if status.Status == ir.Queued && queuedErr != nil {
			return nil, queuedErr
		}
	}
}

// acceptRetry accepts the DAG-level retry of a failed run as the scheduler
// does, which queues the retry, and counts its attempt.
func (c *testCase) acceptRetry(dag *ir.DAG, failed *ir.DAGRunStatus) error {
	ctx := c.ctx
	admission, err := queue.PrepareRetry(ctx, ctx.Persistence.DAGRunRepository, dag, failed, queue.EnqueueRetryOptions{
		AutoRetry: true,
		Processes: ctx.Persistence.ProcRepository,
	})
	if err != nil {
		return err
	}
	// A failed run is not queued, so a nil admission means its last attempt
	// still has a live process record.
	if admission == nil {
		return fmt.Errorf("dag-run %s: the retry was not accepted because the process record of attempt %s was not released", failed.DAGRun(), failed.AttemptID)
	}
	c.attempts++
	return nil
}

// hasRealSteps reports whether the case runs a step or handler for real.
func (c *testCase) hasRealSteps() bool {
	return len(c.opts.real) > 0 || slices.ContainsFunc(slices.Collect(maps.Values(c.opts.handlers)), func(h stepmock.Step) bool {
		return h.Real
	})
}

// runAgent runs one attempt of the case's run. Canceling the case's context
// interrupts the attempt.
func (c *testCase) runAgent(ag *agent.Agent, logFile *os.File) error {
	agentCtx := c.ctx.WithContext(c.ctx.Context)
	agentCtx.LogToFile(logFile)
	stop := context.AfterFunc(agentCtx, func() {
		ag.Signal(context.WithoutCancel(agentCtx), os.Interrupt)
	})
	defer stop()
	defer func() {
		c.notifications = append(c.notifications, ag.Notifications()...)
		c.noteStreamStarts(ag.Status(agentCtx))
	}()
	return ag.Run(agentCtx)
}

// noteStreamStarts records where the latest execution of each node of an
// attempt starts in its logs. A node the attempt carried over without
// running it holds no start, so the one recorded before stays.
func (c *testCase) noteStreamStarts(status ir.DAGRunStatus) {
	if c.streamStarts == nil {
		c.streamStarts = make(map[string]int64)
	}
	nodes := append(slices.Clone(status.Nodes), status.OnInit, status.OnSuccess, status.OnFailure, status.OnAbort, status.OnExit, status.OnWait)
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.StdoutStart > 0 {
			c.streamStarts[node.Stdout] = node.StdoutStart
		}
		if node.StderrStart > 0 {
			c.streamStarts[node.Stderr] = node.StderrStart
		}
	}
}

// lastExecution returns what the log file at path holds from the start of
// the latest execution that wrote to it.
func (c *testCase) lastExecution(path string) (string, error) {
	file, err := fileutil.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(c.streamStarts[path], io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(file)
	return string(data), err
}

// latestStatus returns the status of the latest attempt of the run.
func (c *testCase) latestStatus(ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
	attempt, err := c.ctx.Persistence.DAGRunRepository.FindAttempt(c.ctx, ref)
	if err != nil {
		return nil, err
	}
	return attempt.ReadStatus(c.ctx)
}

// newScripts returns the set that scripts the case's steps and handlers. A
// step in real keeps its action and repeats as written. Every attempt of the
// run shares the set, so the results of a step keep counting after a resume
// or a retry.
func (c *testCase) newScripts() *stepmock.Set {
	steps := make(map[string]stepmock.Step, len(c.opts.mocks)+len(c.opts.real))
	maps.Copy(steps, c.opts.mocks)
	for _, name := range c.opts.real {
		step := steps[name]
		step.Real = true
		steps[name] = step
	}
	set := stepmock.New(steps, c.opts.handlers)
	if c.opts.dagPrecondition != nil {
		set.SetDAGPrecondition(*c.opts.dagPrecondition)
	}
	return set
}

// runQueued runs the attempt of a retry queued for the run, such as the
// resume a completion queues, as the queue's dispatch runs it, with the
// options of the case. It runs the case's DAG instead of rebuilding the DAG
// from its YAML, which would restore its log and artifact directories
// outside the case directory.
func (c *testCase) runQueued(dag *ir.DAG, queued *ir.DAGRunStatus, opts runOptions) error {
	ctx := c.ctx
	opts.triggerType = queue.PreservedQueueTriggerType(queued)
	opts.triggerActor = queued.TriggerActor
	return withPreparedLocalExecution(
		ctx,
		dag,
		queued.DAGRunID,
		opts,
		func(execCtx context.Context) (dagrun.Attempt, error) {
			return ctx.Persistence.DAGRunRepository.CreateAttempt(execCtx, dag, time.Now(), queued.DAGRunID, persis.DAGRunCreateAttemptOptions{Retry: true})
		},
		func(preparedAttempt dagrun.Attempt) error {
			prepared := opts
			prepared.preparedAttempt = preparedAttempt
			return withRetryAgent(ctx, dag, queued, prepared, c.runAgent)
		},
	)
}

// settle settles the first waiting step of a run at its waiting checkpoint,
// as its review decides, through the completion path a person uses. A step
// whose mock sets no review is approved with no values, which fails when its
// wait needs values.
func (c *testCase) settle(dag *ir.DAG, status *ir.DAGRunStatus) error {
	idx := slices.IndexFunc(status.Nodes, func(node *ir.Node) bool {
		return node != nil && node.Status == ir.NodeWaiting
	})
	if idx < 0 {
		return fmt.Errorf("dag-run %s is waiting with no waiting step", status.DAGRun())
	}
	node := status.Nodes[idx]
	name := node.Step.Name
	waitsForChild, err := c.childWaits(status, node)
	if err != nil {
		return fmt.Errorf("step %s: %w", name, err)
	}
	review := c.opts.mocks[name].Review
	switch {
	case waitsForChild:
		return fmt.Errorf("step %s waits for its child run, which a test cannot settle", name)
	case node.Step.HumanTask != nil:
		err = c.completeHumanTask(status, node, review)
	case node.Step.Approval != nil:
		err = c.settleApproval(dag, status, node, review)
	default:
		return fmt.Errorf("step %s waits for input that a test cannot give", name)
	}
	switch {
	case err != nil && review == nil:
		return fmt.Errorf("step %s: approving its wait with no values failed; give it a review: %w", name, err)
	case err != nil:
		return fmt.Errorf("step %s: %w", name, err)
	case review == nil && !slices.Contains(c.autoApproved, name):
		c.autoApproved = append(c.autoApproved, name)
	}
	return nil
}

// childWaits reports whether a child run of node waits. The node then waits
// for that run, even when it also has its own approval gate.
func (c *testCase) childWaits(status *ir.DAGRunStatus, node *ir.Node) (bool, error) {
	root := status.Root
	if root.Zero() {
		root = status.DAGRun()
	}
	for _, child := range node.SubRuns {
		attempt, err := c.ctx.Persistence.DAGRunRepository.FindSubAttempt(c.ctx, root, child.DAGRunID)
		if err != nil {
			return false, fmt.Errorf("failed to find child run %s: %w", child.DAGRunID, err)
		}
		childStatus, err := attempt.ReadStatus(c.ctx)
		if err != nil {
			return false, fmt.Errorf("failed to read child run %s: %w", child.DAGRunID, err)
		}
		if childStatus.Status == ir.Waiting {
			return true, nil
		}
	}
	return false, nil
}

// completeHumanTask completes a waiting human task as dagu human-task complete
// does, with the review's values typed as --inputs-json types them. The
// completion requests the resume when the run can make progress.
func (c *testCase) completeHumanTask(status *ir.DAGRunStatus, node *ir.Node, review *stepmock.Review) error {
	var input humantask.Input
	if review != nil && review.Reject {
		return errors.New("a human task cannot be rejected")
	}
	if review != nil && review.Values != nil {
		data, err := json.Marshal(review.Values)
		if err != nil {
			return fmt.Errorf("invalid review values: %w", err)
		}
		if input, err = humantask.ParseJSONInput(data); err != nil {
			return err
		}
	}
	deps := defaultHumanTaskCompleteDeps()
	service := newLocalHumanTaskService(c.ctx, deps)
	completedBy, completedByID := localOSSubject(deps.currentUser)
	_, err := service.Complete(c.ctx, humantask.CompleteRequest{
		DAGName:       status.Name,
		DAGRunID:      status.DAGRunID,
		StepID:        node.Step.ID,
		Input:         input,
		CompletedBy:   completedBy,
		CompletedByID: completedByID,
	})
	return err
}

// settleApproval approves or rejects a waiting approval gate as the approve
// and reject endpoints do. An approval that lets the run make progress
// accepts its resume, as the approve endpoint does for a DAG outside a queue.
func (c *testCase) settleApproval(dag *ir.DAG, status *ir.DAGRunStatus, node *ir.Node, review *stepmock.Review) error {
	reject := review != nil && review.Reject
	var inputs map[string]string
	if !reject {
		var err error
		if review != nil {
			inputs, err = approvalInputs(review.Values)
		}
		if err == nil {
			err = dagrun.CheckRequiredInputs(node.Step.Approval.Required, inputs)
		}
		if err != nil {
			return err
		}
	}

	ctx := c.ctx
	by, byID := localOSSubject(user.Current)
	repository := ctx.Persistence.DAGRunRepository
	updated, swapped, err := repository.CompareAndSwapLatestAttemptStatus(ctx, status.DAGRun(), status.AttemptID, ir.Waiting,
		func(latest *ir.DAGRunStatus) error {
			idx := slices.IndexFunc(latest.Nodes, func(n *ir.Node) bool { return n != nil && n.Step.Name == node.Step.Name })
			if idx < 0 || latest.Nodes[idx].Status != ir.NodeWaiting {
				return fmt.Errorf("step %s is not waiting for approval", node.Step.Name)
			}
			if reject {
				dagrun.RejectStep(latest, latest.Nodes[idx], nil, by, byID)
			} else {
				dagrun.ApproveStep(latest.Nodes[idx], inputs, by, byID)
			}
			return nil
		}, persis.DAGRunCompareAndSwapOptions{})
	if err != nil {
		return err
	}
	if !swapped {
		return errors.New("the dag-run changed before the step could be settled")
	}
	if reject || !humantask.ApprovalResumeReady(updated) {
		return nil
	}
	_, err = queue.PrepareRetry(ctx, repository, dag, updated, queue.EnqueueRetryOptions{
		Processes: ctx.Persistence.ProcRepository,
		Resume:    true,
	})
	return err
}

// approvalInputs returns the inputs of an approval gate. Each value is a
// scalar, given as its text. A null value is empty, so it counts as left out.
func approvalInputs(values map[string]any) (map[string]string, error) {
	if values == nil {
		return nil, nil
	}
	inputs := make(map[string]string, len(values))
	for name, value := range values {
		switch v := value.(type) {
		case nil:
			inputs[name] = ""
		case string:
			inputs[name] = v
		case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
			float32, float64, json.Number:
			text, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("value %s: %w", name, err)
			}
			inputs[name] = string(text)
		default:
			return nil, fmt.Errorf("value %s is not a scalar", name)
		}
	}
	return inputs, nil
}

// close removes the case directory unless the case keeps it.
func (c *testCase) close() error {
	if c.opts.keep {
		return nil
	}
	return fileutil.RemoveAll(c.dir)
}

// inCaseDir returns a copy of c whose stores keep their records in dir. The
// DAGs, the base config, and the other inputs of a run stay where they are
// configured.
func (c *Context) inCaseDir(dir string) (*Context, error) {
	cfg := *c.Config
	cfg.Paths = casePaths(c.Config.Paths, dir)
	server := file.NewServerStores(&cfg)
	stores, err := openDefaultProject(c.Context, &cfg, server, file.ProjectStoreOptions{})
	if err != nil {
		return nil, err
	}
	persistence := newPersistence(server, stores)

	clone := *c
	clone.Context = config.WithConfig(c.Context, &cfg)
	clone.Config = &cfg
	// The case's log file is its only log output.
	clone.Quiet = true
	clone.Persistence = persistence
	clone.DAGRunMgr = runtime.NewManager(persistence.DAGRunRepository, persistence.ProcRepository, &cfg)
	clone.serverStores = server
	clone.projectStores = stores
	clone.settings = stores.ProjectSettings
	clone.Caches = nil
	clone.Proc = nil
	if c.event == nil {
		return &clone, nil
	}
	// A run emits events to the store its context carries, so the case's
	// store takes the place of the configured one.
	events := newEventService(clone.Context, &cfg, stores.Paths.EventStoreDir)
	if events == nil {
		return nil, errors.New("failed to open the event store of the case")
	}
	return clone.withEvent(events), nil
}

// casePaths moves every path that holds run records, logs, artifacts, state,
// live-process records, queues, and locks into dir. The paths of the run's
// inputs and the tools directory are kept.
func casePaths(paths config.PathsConfig, dir string) config.PathsConfig {
	in := func(name string) string { return filepath.Join(dir, name) }
	paths.DataDir = dir
	paths.LogDir = in("logs")
	paths.AdminLogsDir = in("admin")
	paths.EventStoreDir = in("events")
	paths.ArtifactDir = in("artifacts")
	paths.DAGRunsDir = in("dag-runs")
	paths.DAGRunWorkDir = in("dag-run-work")
	paths.DAGStateDir = in("dag-state")
	paths.ProcDir = in("proc")
	paths.QueueDir = in("queue")
	paths.ServiceRegistryDir = in("service-registry")
	paths.SuspendFlagsDir = in("suspend")
	paths.SuspendFlagsDirLegacy = ""
	paths.UsersDir = in("users")
	paths.APIKeysDir = in("apikeys")
	paths.WebhooksDir = in("webhooks")
	paths.ContextsDir = in("contexts")
	paths.RemoteNodesDir = in("remote-nodes")
	paths.WorkspacesDir = in("workspaces")
	paths.ViewsDir = in("views")
	return paths
}

// caseDAG returns a copy of dag whose logs and artifacts go to the case
// directory instead of a DAG-level directory.
func caseDAG(dag *ir.DAG) *ir.DAG {
	clone := dag.Clone()
	clone.LogDir = ""
	if clone.Artifacts != nil {
		clone.Artifacts.Dir = ""
	}
	return clone
}

// caseSecrets returns the values the secrets of a case resolve from, in
// place of their providers. A case with a real step needs a test value for
// every DAG secret and SSH credential ref, since the real step may read any
// of them. In a case without one, a DAG secret without a test value
// resolves to the placeholder <secret:NAME>, masked as a value is. An SSH
// credential ref takes no placeholder, since a run without a real step does
// not resolve it (agent.TestSideEffects).
func caseSecrets(dag *ir.DAG, values map[string]string, hasRealSteps bool) (map[string]string, error) {
	resolved := make(map[string]string, len(values)+len(dag.Secrets))
	maps.Copy(resolved, values)
	if hasRealSteps {
		if err := checkCaseSecrets(dag, resolved); err != nil {
			return nil, err
		}
		return resolved, nil
	}
	for _, ref := range dag.Secrets {
		if _, ok := resolved[ref.Name]; !ok {
			resolved[ref.Name] = "<secret:" + ref.Name + ">"
		}
	}
	return resolved, nil
}

// checkCaseSecrets requires a test value for every DAG secret and SSH
// credential ref.
func checkCaseSecrets(dag *ir.DAG, values map[string]string) error {
	var missing []string
	for _, ref := range dag.Secrets {
		if _, ok := values[ref.Name]; !ok {
			missing = append(missing, ref.Name)
		}
	}
	for _, ref := range dag.CredentialRefs {
		if _, ok := values[ref.Ref]; !ok {
			missing = append(missing, ref.Ref)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("secrets without a test value: %s", strings.Join(missing, ", "))
	}
	return nil
}

// caseTrigger returns the trigger type of a case run. A case cannot be a
// sub-DAG run.
func caseTrigger(trigger ir.TriggerType) (ir.TriggerType, error) {
	if trigger == ir.TriggerTypeUnknown {
		return ir.TriggerTypeManual, nil
	}
	if trigger == ir.TriggerTypeSubDAG {
		return trigger, fmt.Errorf("a test case cannot have trigger type %s", trigger)
	}
	return trigger, nil
}
