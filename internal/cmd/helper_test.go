// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatchBaseConfig(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	dag := &ir.DAG{Name: "smtp", BaseConfigData: []byte("smtp:\n  host: smtp.example\n")}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, dag, "run", runOptions{}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, string(dag.BaseConfigData), client.task.BaseConfig)
}

func TestDispatchTriggerType(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "gpu"}, "run", runOptions{triggerType: ir.TriggerTypeManual}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, ir.TriggerTypeManual, client.task.TriggerType)
}

// A narrowed start reaches the coordinator with its node list.
func TestDispatchTargetNodes(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "deploy"}, "run", runOptions{targetNodes: []string{"web01"}}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, []string{"web01"}, client.task.TargetNodes)
}

// A selected-steps run is dispatched as a retry of its seeded queued attempt,
// so the worker starts from the seeded node states.
func TestDispatchSeededRun(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	dag := &ir.DAG{Name: "seeded"}
	seed := &ir.DAGRunStatus{Name: "seeded", DAGRunID: "run", Status: ir.Queued}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, dag, "run", runOptions{seed: seed}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, dispatch.DispatchOperationRetry, client.task.Operation)
	require.NotNil(t, client.task.PreviousStatus)
	assert.Equal(t, ir.Queued, client.task.PreviousStatus.Status)
}

// A run bound to a worker node is dispatched to that worker, bound to it, so
// the coordinator refuses it at once while the worker is offline.
func TestDispatchBoundWorker(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "deploy-one", NodeRequired: true}, "run",
		runOptions{targetNodes: []string{"gw-1"}, boundWorker: "gw-1"}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, dispatch.DispatchOperationStart, client.task.Operation)
	assert.Equal(t, "gw-1", client.task.TargetWorkerID)
	assert.Equal(t, "gw-1", client.task.BoundNode)
	assert.Empty(t, client.task.NodeStep)
}

// A started run reaches its worker with the params it was started with, since
// the worker loads the definition again.
func TestDispatchParams(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	dag := &ir.DAG{Name: "deploy-one", Params: []string{"DIR=/tmp/a b"}}
	err := dispatchToCoordinatorAndWait(ctx, dag, "run", runOptions{}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, `DIR="/tmp/a b"`, client.task.Params)
}

// A retry of a run bound to a worker node is dispatched to that worker as a
// retry of the run, with the step it retries.
func TestDispatchBoundWorkerRetry(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	retried := &ir.DAGRunStatus{Name: "deploy-one", DAGRunID: "run", Status: ir.Failed, TargetNodes: []string{"gw-1"}}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "deploy-one", NodeRequired: true}, "run",
		runOptions{retried: retried, boundWorker: "gw-1", step: "restart"}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, dispatch.DispatchOperationRetry, client.task.Operation)
	require.NotNil(t, client.task.PreviousStatus)
	assert.Equal(t, ir.Failed, client.task.PreviousStatus.Status)
	assert.Equal(t, "restart", client.task.Step)
	assert.Equal(t, "gw-1", client.task.TargetWorkerID)
	assert.Equal(t, []string{"gw-1"}, client.task.TargetNodes, "the retry keeps the run's node list")
}

// A retry that the rules send to the coordinator goes to the worker that holds
// the agent session of a failed step, as the API sends it.
func TestDispatchRetryToSessionOwner(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	retried := &ir.DAGRunStatus{Name: "agent", DAGRunID: "run", Status: ir.Failed, Nodes: []*ir.Node{{
		Status:       ir.NodeFailed,
		AgentSession: &ir.AgentSession{OwnerWorkerID: "worker-2"},
	}}}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "agent", WorkerSelector: map[string]string{"gpu": "true"}}, "run",
		runOptions{retried: retried}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, dispatch.DispatchOperationRetry, client.task.Operation)
	assert.Equal(t, "worker-2", client.task.TargetWorkerID)
	assert.Equal(t, map[string]string{"gpu": "true"}, client.task.WorkerSelector)
}

// A step retry goes to the worker that holds that step's session, even when
// another step's session is on another worker.
func TestDispatchStepRetryToSessionOwner(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	retried := &ir.DAGRunStatus{Name: "agent", DAGRunID: "run", Status: ir.Succeeded, Nodes: []*ir.Node{
		{Step: ir.Step{Name: "a"}, Status: ir.NodeSucceeded, AgentSession: &ir.AgentSession{OwnerWorkerID: "worker-1"}},
		{Step: ir.Step{Name: "b"}, Status: ir.NodeSucceeded, AgentSession: &ir.AgentSession{OwnerWorkerID: "worker-2"}},
	}}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "agent", WorkerSelector: map[string]string{"gpu": "true"}}, "run",
		runOptions{retried: retried, step: "b"}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, "b", client.task.Step)
	assert.Equal(t, "worker-2", client.task.TargetWorkerID)
}

// A step retry in a sub-run that ran in the root's process, so under the root
// attempt's claim, goes to the worker that holds the sub-run step's session.
func TestDispatchSubRunStepRetryToSessionOwner(t *testing.T) {
	t.Parallel()

	ctx := &Context{Context: context.Background(), Config: &config.Config{}, Quiet: true}
	retried := &ir.DAGRunStatus{Name: "parent", DAGRunID: "run", Status: ir.Failed, WorkerID: "worker-2", ClaimKey: "claim", Nodes: []*ir.Node{
		{Step: ir.Step{Name: "call"}, Status: ir.NodeFailed},
	}}
	sub := &ir.DAGRunStatus{Name: "child", DAGRunID: "child-run", Status: ir.Failed, WorkerID: "worker-2", ClaimKey: "claim", Nodes: []*ir.Node{
		{Step: ir.Step{Name: "agent"}, Status: ir.NodeFailed, AgentSession: &ir.AgentSession{OwnerWorkerID: "worker-2"}},
	}}
	path := dagrun.RetryPath{Hops: []dagrun.RetryHop{{Step: "call", RunID: "child-run"}}, Step: "agent"}
	client := &smtpDispatchClient{err: errors.New("stop after dispatch")}
	err := dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "parent", WorkerSelector: map[string]string{"gpu": "true"}}, "run",
		runOptions{retried: retried, retriedSubRun: sub, step: "call", retryPath: path}, client)
	require.ErrorIs(t, err, client.err)
	require.NotNil(t, client.task)
	assert.Equal(t, "worker-2", client.task.TargetWorkerID)
}

type smtpDispatchClient struct {
	coordinator.Client
	task *dispatch.DispatchTask
	err  error
}

func (c *smtpDispatchClient) Dispatch(_ context.Context, req dispatch.DispatchRequest) error {
	c.task = req.Task
	return c.err
}

// interruptedDispatchClient is a coordinator that takes a task while the
// command is interrupted, and records the calls it receives in order.
type interruptedDispatchClient struct {
	coordinator.Client
	interrupt context.CancelFunc
	calls     []string
}

func (c *interruptedDispatchClient) Dispatch(ctx context.Context, req dispatch.DispatchRequest) error {
	c.interrupt()
	if ctx.Err() != nil {
		c.calls = append(c.calls, "dispatch cut short")
		return ctx.Err()
	}
	c.calls = append(c.calls, "dispatched "+req.Task.DAGRunID)
	return nil
}

func (c *interruptedDispatchClient) RequestCancel(_ context.Context, dagName, dagRunID string, _ *ir.DAGRunRef) error {
	c.calls = append(c.calls, "cancel "+dagName+":"+dagRunID)
	return nil
}

// An interrupt during the dispatch does not cut the call short, since the
// coordinator may take the task anyway. The run is cancelled once the
// coordinator holds it, so the cancellation cannot arrive before the run
// exists, or reach an earlier attempt of a retried run.
func TestDispatchInterruptedCancelsRun(t *testing.T) {
	t.Parallel()

	parent, interrupt := context.WithCancel(context.Background())
	defer interrupt()
	ctx := &Context{Context: parent, Config: &config.Config{}, Quiet: true}
	client := &interruptedDispatchClient{interrupt: interrupt}

	err := dispatchToCoordinatorAndWait(ctx, &ir.DAG{Name: "deploy"}, "run", runOptions{}, client)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"dispatched run", "cancel deploy:run"}, client.calls)
}

func TestRestoreDAGFromStatus_SMTP(t *testing.T) {
	t.Parallel()

	for _, current := range []string{"smtp:\n  host: current.example\n  password: current-password\n", "{}\n"} {
		t.Run(current, func(t *testing.T) {
			t.Parallel()
			basePath := filepath.Join(t.TempDir(), "base.yaml")
			require.NoError(t, os.WriteFile(basePath, []byte(current), 0600))
			cfg := &config.Config{}
			cfg.Paths.BaseConfig = basePath
			ctx := config.WithConfig(context.Background(), cfg)
			dag := &ir.DAG{
				Name:           "smtp-retry",
				YamlData:       []byte("smtp:\n  username: ${SMTP_USER}\nenv:\n  SMTP_USER: original-user\nsteps:\n  - run: echo original\n"),
				BaseConfigData: []byte("smtp:\n  host: old.example\n  password: old-password\nenv:\n  ORIGINAL_BASE: original\n"),
			}
			restored, err := restoreDAGFromStatus(ctx, dag, &ir.DAGRunStatus{}, nil, baseConfigOptions(config.GetConfig(ctx)))
			require.NoError(t, err)
			require.NotNil(t, restored.SMTP)
			assert.Equal(t, "${SMTP_USER}", restored.SMTP.Username)
			if current == "{}\n" {
				assert.Empty(t, restored.SMTP.Host)
				assert.Empty(t, restored.SMTP.Password)
			} else {
				assert.Equal(t, "current.example", restored.SMTP.Host)
				assert.Equal(t, "current-password", restored.SMTP.Password)
			}
			assert.Contains(t, restored.Env, "ORIGINAL_BASE=original")
			assert.Contains(t, restored.Env, "SMTP_USER=original-user")
		})
	}
}

// TestExtractDAGNameYAMLCase verifies mixed-case YAML paths return authored DAG metadata names.
func TestExtractDAGNameYAMLCase(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		file string
		dag  string
	}{
		{name: "YAML", file: "FLOW.YAML", dag: "authored-yaml"},
		{name: "Yml", file: "flow.Yml", dag: "authored-yml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagsDir := t.TempDir()
			path := filepath.Join(dagsDir, tc.file)
			require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf("name: %s\nsteps: []\n", tc.dag)), 0600))

			cfg := &config.Config{}
			cfg.Paths.DAGsDir = dagsDir
			stores, err := openProject(context.Background(), cfg, file.NewServerStores(cfg), defaultProject(), file.ProjectStoreOptions{})
			require.NoError(t, err)
			ctx := &Context{Context: context.Background(), Config: cfg, projectStores: stores}

			name, err := extractDAGName(ctx, path)
			require.NoError(t, err)
			assert.Equal(t, tc.dag, name)
		})
	}
}

// A path to a test file names no DAG, even when the file holds one.
func TestExtractDAGNameTestFile(t *testing.T) {
	t.Parallel()

	dagsDir := t.TempDir()
	path := filepath.Join(dagsDir, "release.test.yaml")
	require.NoError(t, os.WriteFile(path, []byte("steps: []\n"), 0600))
	cfg := &config.Config{}
	cfg.Paths.DAGsDir = dagsDir

	_, err := extractDAGName(&Context{Context: context.Background(), Config: cfg}, path)
	require.ErrorIs(t, err, spec.ErrTestFile)
}

func TestRestoreLegacyChildSMTP(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Paths.DAGsDir = t.TempDir()
	basePath := workspace.BaseConfigPath(cfg.Paths.DAGsDir, "ops")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0750))
	require.NoError(t, os.WriteFile(basePath, []byte("smtp:\n  host: workspace.example\n"), 0600))
	ctx := config.WithConfig(context.Background(), cfg)
	repository := testutil.NewFileDAGRunRepository(t.TempDir(), persis.DAGRunRepositoryOptions{})
	parent := &ir.DAG{Name: "parent", YamlData: []byte("labels: [workspace=ops]\nsteps:\n  - run: echo parent\n")}
	parentStatus := ir.DAGRunStatus{Name: "parent", DAGRunID: "parent-run", Status: ir.Failed}
	attempt, err := repository.CreateAttempt(ctx, parent, time.Now(), parentStatus.DAGRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, parentStatus))
	require.NoError(t, attempt.Close(ctx))
	child := &ir.DAG{Name: "child", YamlData: []byte("steps:\n  - run: echo child\n"), BaseConfigData: []byte("{}")}
	status := &ir.DAGRunStatus{Root: parentStatus.DAGRun(), Parent: parentStatus.DAGRun()}
	got, err := restoreDAGFromStatus(ctx, child, status, repository, baseConfigOptions(cfg))
	require.NoError(t, err)
	require.NotNil(t, got.SMTP)
	assert.Equal(t, "workspace.example", got.SMTP.Host)
	require.NotNil(t, got.BaseConfigWorkspace)
	assert.Equal(t, "ops", *got.BaseConfigWorkspace)
}

func TestQuoteParamValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []string
		paramDefs []ir.ParamDef
		expect    []string
	}{
		{
			name:   "named param with spaces",
			input:  []string{"topic=hello world"},
			expect: []string{`topic="hello world"`},
		},
		{
			name:   "named param without spaces",
			input:  []string{"topic=hello"},
			expect: []string{`topic="hello"`},
		},
		{
			name:   "positional param with spaces",
			input:  []string{"hello world"},
			expect: []string{`"hello world"`},
		},
		{
			name:   "positional param without spaces",
			input:  []string{"hello"},
			expect: []string{`"hello"`},
		},
		{
			name:   "multiple params",
			input:  []string{"topic=hello world", "count=42", "greeting"},
			expect: []string{`topic="hello world"`, `count="42"`, `"greeting"`},
		},
		{
			name:   "empty slice",
			input:  []string{},
			expect: []string{},
		},
		{
			name:   "param with quotes in value",
			input:  []string{`msg=say "hi"`},
			expect: []string{`msg="say \"hi\""`},
		},
		{
			name:      "positional params stored with numeric placeholders",
			input:     []string{"1=hello world", "2=42"},
			paramDefs: []ir.ParamDef{{Name: ""}, {Name: ""}},
			expect:    []string{`"hello world"`, `"42"`},
		},
		{
			name:      "numeric named params stay named",
			input:     []string{"1=hello"},
			paramDefs: []ir.ParamDef{{Name: "1"}},
			expect:    []string{`1="hello"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := spec.QuoteRuntimeParams(tt.input, tt.paramDefs)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestRestoreDAGFromStatus_ParamsWithSpaces(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:     "test-dag",
		YamlData: []byte("params:\n  - topic: \"\"\nsteps:\n  - name: test\n    command: echo $topic"),
	}

	status := &ir.DAGRunStatus{
		ParamsList: []string{"topic=hello world"},
	}

	result, err := restoreDAGFromStatus(context.Background(), dag, status, nil, nil)
	require.NoError(t, err)

	// The restored params should preserve "hello world" as a single value
	found := slices.Contains(result.Params, "topic=hello world")
	assert.True(t, found, "expected 'topic=hello world' in params, got: %v", result.Params)
}

func TestRestoreDAGFromStatus_PositionalParamsRemainOverrides(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:     "test-dag",
		YamlData: []byte("params: \"default\"\nsteps:\n  - name: test\n    command: echo $1"),
		ParamDefs: []ir.ParamDef{
			{Name: ""},
		},
	}

	status := &ir.DAGRunStatus{
		ParamsList: []string{"1=override"},
	}

	result, err := restoreDAGFromStatus(context.Background(), dag, status, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"1=override"}, result.Params)
}

func TestRestoreDAGFromStatus_PreservesExplicitWorkingDirFromYAML(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "test-dag",
		WorkingDir: workDir,
		YamlData: fmt.Appendf(nil, `
working_dir: %q
steps:
  - name: test
    run: pwd
`, workDir),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, workDir, result.WorkingDir)
	assert.True(t, result.WorkingDirExplicit)
}

func TestRestoreDAGFromStatus_PreservesBaseConfigWorkingDirAsExplicit(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "test-dag",
		WorkingDir: workDir,
		YamlData: []byte(`
steps:
  - name: test
    run: pwd
`),
		BaseConfigData: fmt.Appendf(nil, "working_dir: %q\n", workDir),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, workDir, result.WorkingDir)
	assert.True(t, result.WorkingDirExplicit)
}

func TestRestoreDAGFromStatus_PrefersPersistedRunWorkingDir(t *testing.T) {
	t.Parallel()

	persistedWorkDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "test-dag",
		WorkingDir: "/changed-work-dir",
		YamlData: []byte(`
working_dir: /changed-work-dir
steps:
  - name: test
    run: pwd
`),
	}
	status := &ir.DAGRunStatus{WorkingDir: persistedWorkDir}

	result, err := restoreDAGFromStatus(context.Background(), dag, status, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, persistedWorkDir, result.WorkingDir)
	assert.True(t, result.WorkingDirExplicit)
}

func TestRestoreDAGFromStatus_RestoresRegistryAuthsFromYAML(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		YamlData: []byte(`
registry_auths:
  registry.example.com:
    username: ${REGISTRY_USER}
    password: ${REGISTRY_PASSWORD}
steps:
  - name: test
    run: echo hello
`),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status, nil, nil)
	require.NoError(t, err)
	require.Contains(t, result.RegistryAuths, "registry.example.com")
	require.Equal(t, "${REGISTRY_USER}", result.RegistryAuths["registry.example.com"].Username)
	require.Equal(t, "${REGISTRY_PASSWORD}", result.RegistryAuths["registry.example.com"].Password)
}

func TestRestoreDAGFromStatus_RestoresRegistryAuthsFromBaseConfig(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		YamlData: []byte(`
steps:
  - name: test
    run: echo hello
`),
		BaseConfigData: []byte(`
registry_auths:
  registry.example.com:
    username: ${REGISTRY_USER}
    password: ${REGISTRY_PASSWORD}
`),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status, nil, nil)
	require.NoError(t, err)
	require.Contains(t, result.RegistryAuths, "registry.example.com")
	require.Equal(t, "${REGISTRY_USER}", result.RegistryAuths["registry.example.com"].Username)
	require.Equal(t, "${REGISTRY_PASSWORD}", result.RegistryAuths["registry.example.com"].Password)
}

func TestRestoreDAGFromStatus_RestoresHarnessConfigFromBaseConfig(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		YamlData: []byte(`
steps:
  - run: Review the repository
`),
		BaseConfigData: []byte(`
harnesses:
  passthrough:
    binary: cat
    prompt_mode: stdin
harness:
  provider: passthrough
`),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result.Harness)
	assert.Equal(t, "passthrough", result.Harness.Config["provider"])
	require.NotNil(t, result.Harnesses)
	require.Contains(t, result.Harnesses, "passthrough")
	assert.Equal(t, "cat", result.Harnesses["passthrough"].Binary)
}
