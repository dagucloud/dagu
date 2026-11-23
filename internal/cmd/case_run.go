// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
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
	// notifications lists the mail notifications the case's runs would have
	// sent, in the order the runs reached them. A case sends no mail.
	notifications []agent.Notification
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

	ctx := c.ctx
	if err := ctx.DAGRunMgr.CheckServiceRun(ctx, dag, dagRunID); err != nil {
		return nil, err
	}
	opts := runOptions{
		root:         ir.NewDAGRunRef(dag.Name, dagRunID),
		workerID:     "local",
		triggerType:  trigger,
		secretValues: secrets,
		testCase:     true,
		redirectRoot: runtime.RedirectRoot{
			Dir:  filepath.Join(c.dir, "redirects"),
			Keep: c.opts.real,
		},
		testSideEffects: &agent.TestSideEffects{HasRealSteps: c.hasRealSteps()},
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
			return withDAGRunAgent(ctx, dag, dagRunID, prepared, func(ag *agent.Agent, logFile *os.File) error {
				agentCtx := ctx.WithContext(ctx.Context)
				agentCtx.LogToFile(logFile)
				stop := context.AfterFunc(agentCtx, func() {
					ag.Signal(context.WithoutCancel(agentCtx), os.Interrupt)
				})
				defer stop()
				defer func() { c.notifications = append(c.notifications, ag.Notifications()...) }()
				return ag.Run(agentCtx)
			})
		},
	)

	attempt, err := ctx.Persistence.DAGRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, dagRunID))
	if err != nil {
		return nil, errors.Join(runErr, err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, errors.Join(runErr, err)
	}
	return status, nil
}

// hasRealSteps reports whether the case runs a step for real.
func (c *testCase) hasRealSteps() bool {
	return len(c.opts.real) > 0
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
	backend := file.NewBackend(cfg.Paths)
	persistence, err := newFilePersistence(c.Context, &cfg, backend, filePersistenceOptions{})
	if err != nil {
		return nil, err
	}

	clone := *c
	clone.Context = config.WithConfig(c.Context, &cfg)
	clone.Config = &cfg
	// The case's log file is its only log output.
	clone.Quiet = true
	clone.Persistence = persistence
	clone.DAGRunMgr = runtime.NewManager(persistence.DAGRunRepository, persistence.ProcRepository, &cfg)
	clone.backend = backend
	clone.Caches = nil
	clone.Proc = nil
	if c.event == nil {
		return &clone, nil
	}
	// A run emits events to the store its context carries, so the case's
	// store takes the place of the configured one.
	events := newEventService(clone.Context, &cfg)
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
