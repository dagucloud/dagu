// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/persis/filedag"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/persis/filegithubdispatch"
	"github.com/dagucloud/dagu/internal/persis/fileproc"
	"github.com/dagucloud/dagu/internal/persis/filequeue"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGitHubDispatchRuntimeParams(t *testing.T) {
	t.Parallel()

	params := buildGitHubDispatchRuntimeParams(license.GitHubDispatchJob{
		EventName:         "release",
		EventAction:       "published",
		RepositoryName:    "dagucloud/dagu",
		SHA:               "abc123",
		Ref:               "refs/tags/v1.2.3",
		PullRequestNumber: 7,
		IssueNumber:       9,
		Command:           "run",
		ActorLogin:        "alice",
		Payload:           []byte(`{"release":{"tag_name":"v1.2.3"},"workflow":"release","event_type":"manual"}`),
		Headers:           []byte(`{"x-github-event":["release"]}`),
	})

	assert.Contains(t, params, `WEBHOOK_PAYLOAD="{\"release\":{\"tag_name\":\"v1.2.3\"},\"workflow\":\"release\",\"event_type\":\"manual\"}"`)
	assert.Contains(t, params, `GITHUB_EVENT_NAME="release"`)
	assert.Contains(t, params, `GITHUB_EVENT_ACTION="published"`)
	assert.Contains(t, params, `GITHUB_REPOSITORY="dagucloud/dagu"`)
	assert.Contains(t, params, `GITHUB_SHA="abc123"`)
	assert.Contains(t, params, `GITHUB_REF="refs/tags/v1.2.3"`)
	assert.Contains(t, params, `GITHUB_PR_NUMBER="7"`)
	assert.Contains(t, params, `GITHUB_ISSUE_NUMBER="9"`)
	assert.Contains(t, params, `GITHUB_COMMAND="run"`)
	assert.Contains(t, params, `GITHUB_ACTOR="alice"`)
	assert.Contains(t, params, `GITHUB_RELEASE_TAG="v1.2.3"`)
	assert.Contains(t, params, `GITHUB_WORKFLOW="release"`)
	assert.Contains(t, params, `GITHUB_DISPATCH_EVENT_TYPE="manual"`)
}

func TestGitHubDispatchWorker_ProcessAndReportJob(t *testing.T) {
	t.Parallel()

	env := newDispatchTestEnv(t, "github-ci")
	tracker := filegithubdispatch.New(filepath.Join(t.TempDir(), "tracker"))
	client := &stubDispatchClient{}
	licenses := newStubDispatchLicenseManager()
	worker := NewGitHubDispatchWorker(
		env.cfg,
		env.dagStore,
		env.dagRuns,
		env.queue,
		&env.runMgr,
		licenses,
		client,
		tracker,
		nil,
	)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return now }

	job := license.GitHubDispatchJob{
		ID:                "job-1",
		DAGName:           env.dag.Name,
		RepositoryName:    "dagucloud/dagu",
		EventName:         "pull_request",
		EventAction:       "opened",
		DeliveryID:        "delivery-1",
		SHA:               "abc123",
		Ref:               "refs/heads/main",
		PullRequestNumber: 42,
		ActorLogin:        "alice",
		Payload:           []byte(`{"pull_request":{"number":42}}`),
		Headers:           []byte(`{"x-github-event":["pull_request"]}`),
	}

	err := worker.processJob(env.ctx, licenses.creds(), job)
	require.NoError(t, err)

	require.Len(t, client.accepts, 1)
	assert.Equal(t, "job-1", client.accepts[0].jobID)
	assert.Equal(t, "job-1", client.accepts[0].req.DAGRunID)

	items, err := env.queue.ListByDAGName(env.ctx, env.dag.ProcGroup(), env.dag.Name)
	require.NoError(t, err)
	require.Len(t, items, 1)

	tracked, err := tracker.List()
	require.NoError(t, err)
	require.Len(t, tracked, 1)
	assert.Equal(t, githubDispatchAccepted, tracked[0].Phase)

	attempt, err := env.dagRuns.FindAttempt(env.ctx, coreexec.NewDAGRunRef(env.dag.Name, "job-1"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(env.ctx)
	require.NoError(t, err)
	assert.Equal(t, core.Queued, status.Status)
	assert.Contains(t, status.Params, `WEBHOOK_PAYLOAD`)
	assert.Contains(t, status.Params, `GITHUB_EVENT_NAME=pull_request`)
	assert.Contains(t, status.Params, `GITHUB_PR_NUMBER=42`)

	status.Status = core.Succeeded
	status.FinishedAt = coreexec.FormatTime(now.Add(time.Minute))
	require.NoError(t, attempt.Open(env.ctx))
	require.NoError(t, attempt.Write(env.ctx, *status))
	require.NoError(t, attempt.Close(env.ctx))

	err = worker.reportTrackedJobs(env.ctx, licenses.creds())
	require.NoError(t, err)

	require.Len(t, client.finishes, 1)
	assert.Equal(t, "job-1", client.finishes[0].jobID)
	assert.Equal(t, "succeeded", client.finishes[0].req.ResultStatus)

	tracked, err = tracker.List()
	require.NoError(t, err)
	assert.Empty(t, tracked)
}

func TestGitHubDispatchWorker_CancelCommandDoesNotStopDag(t *testing.T) {
	t.Parallel()

	env := newDispatchTestEnv(t, "github-deploy")
	tracker := filegithubdispatch.New(filepath.Join(t.TempDir(), "tracker"))
	client := &stubDispatchClient{}
	licenses := newStubDispatchLicenseManager()
	runMgr := &stubDispatchRuntimeManager{}
	worker := NewGitHubDispatchWorker(
		env.cfg,
		env.dagStore,
		env.dagRuns,
		env.queue,
		runMgr,
		licenses,
		client,
		tracker,
		nil,
	)

	err := worker.processJob(env.ctx, licenses.creds(), license.GitHubDispatchJob{
		ID:      "job-cancel",
		DAGName: env.dag.Name,
		Command: "cancel",
	})
	require.NoError(t, err)

	assert.Empty(t, runMgr.calls)
	require.Len(t, client.finishes, 1)
	assert.Equal(t, "aborted", client.finishes[0].req.ResultStatus)
	assert.Contains(t, client.finishes[0].req.ResultSummary, "not supported")

	items, err := env.queue.ListByDAGName(env.ctx, env.dag.ProcGroup(), env.dag.Name)
	require.NoError(t, err)
	assert.Empty(t, items)

	tracked, err := tracker.List()
	require.NoError(t, err)
	assert.Empty(t, tracked)
}

func TestGitHubDispatchWorker_ReportTrackedJobsContinuesAfterError(t *testing.T) {
	t.Parallel()

	env := newDispatchTestEnv(t, "github-report")
	tracker := filegithubdispatch.New(filepath.Join(t.TempDir(), "tracker"))
	client := &stubDispatchClient{
		finishErrByJobID: map[string]error{
			"job-1": errors.New("finish failed"),
		},
	}
	licenses := newStubDispatchLicenseManager()
	worker := NewGitHubDispatchWorker(
		env.cfg,
		env.dagStore,
		env.dagRuns,
		env.queue,
		&env.runMgr,
		licenses,
		client,
		tracker,
		nil,
	)

	for _, jobID := range []string{"job-1", "job-2"} {
		err := worker.processJob(env.ctx, licenses.creds(), license.GitHubDispatchJob{
			ID:      jobID,
			DAGName: env.dag.Name,
			Payload: []byte(`{}`),
			Headers: []byte(`{}`),
		})
		require.NoError(t, err)

		attempt, err := env.dagRuns.FindAttempt(env.ctx, coreexec.NewDAGRunRef(env.dag.Name, jobID))
		require.NoError(t, err)
		status, err := attempt.ReadStatus(env.ctx)
		require.NoError(t, err)
		status.Status = core.Succeeded
		require.NoError(t, attempt.Open(env.ctx))
		require.NoError(t, attempt.Write(env.ctx, *status))
		require.NoError(t, attempt.Close(env.ctx))
	}

	err := worker.reportTrackedJobs(env.ctx, licenses.creds())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "job-1")

	require.Len(t, client.finishes, 2)
	assert.Equal(t, "job-1", client.finishes[0].jobID)
	assert.Equal(t, "job-2", client.finishes[1].jobID)

	tracked, err := tracker.List()
	require.NoError(t, err)
	require.Len(t, tracked, 1)
	assert.Equal(t, "job-1", tracked[0].JobID)
}

func TestGitHubDispatchWorker_CredentialsEnabledWithoutGitHubFeature(t *testing.T) {
	t.Parallel()

	env := newDispatchTestEnv(t, "github-credentials")
	worker := NewGitHubDispatchWorker(
		env.cfg,
		env.dagStore,
		env.dagRuns,
		env.queue,
		&env.runMgr,
		newStubDispatchLicenseManager(),
		&stubDispatchClient{},
		filegithubdispatch.New(filepath.Join(t.TempDir(), "tracker")),
		nil,
	)

	creds, enabled, err := worker.credentials()
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.Equal(t, "lic-1", creds.licenseID)
}

func TestNewGitHubDispatchWorker_DefaultPollingIntervals(t *testing.T) {
	t.Parallel()

	env := newDispatchTestEnv(t, "github-default-intervals")
	worker := NewGitHubDispatchWorker(
		env.cfg,
		env.dagStore,
		env.dagRuns,
		env.queue,
		&env.runMgr,
		newStubDispatchLicenseManager(),
		&stubDispatchClient{},
		filegithubdispatch.New(filepath.Join(t.TempDir(), "tracker")),
		nil,
	)

	require.NotNil(t, worker)
	assert.Equal(t, 10*time.Second, worker.idleDelay)
	assert.Equal(t, 10*time.Second, worker.errDelay)
	assert.Equal(t, 10*time.Second, worker.reportGap)
}

func TestGitHubDispatchWorker_StartRunsLoopsUntilContextCanceled(t *testing.T) {
	t.Parallel()

	env := newDispatchTestEnv(t, "github-loop")
	tracker := filegithubdispatch.New(filepath.Join(t.TempDir(), "tracker"))
	client := &loopDispatchClient{pulled: make(chan struct{})}
	licenses := newStubDispatchLicenseManager()
	worker := NewGitHubDispatchWorker(
		env.cfg,
		env.dagStore,
		env.dagRuns,
		env.queue,
		&env.runMgr,
		licenses,
		client,
		tracker,
		nil,
	)
	worker.idleDelay = time.Millisecond
	worker.errDelay = time.Millisecond
	worker.reportGap = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Start(ctx)
	}()

	select {
	case <-client.pulled:
	case <-time.After(5 * time.Second):
		t.Fatal("github dispatch worker did not pull")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("github dispatch worker did not stop")
	}
	assert.Greater(t, client.pullCalls, 0)
}

func TestScheduler_StartsGitHubDispatchWorker(t *testing.T) {
	t.Parallel()

	env := newDispatchTestEnv(t, "scheduler-start")
	sc, err := New(env.cfg, &stubEntryReader{dagStore: env.dagStore}, env.runMgr, env.dagRuns, env.queue, env.proc, nil, nil, nil)
	require.NoError(t, err)
	runner := &stubDispatchRunner{started: make(chan struct{}, 1)}
	sc.SetGitHubDispatchWorker(runner)

	ctx := t.Context()
	errCh := make(chan error, 1)
	go func() {
		errCh <- sc.Start(ctx)
	}()

	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("github dispatch worker did not start")
	}

	sc.Stop(ctx)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

type dispatchTestEnv struct {
	ctx      context.Context
	cfg      *config.Config
	dag      *core.DAG
	dagStore coreexec.DAGStore
	dagRuns  coreexec.DAGRunStore
	queue    coreexec.QueueStore
	proc     coreexec.ProcStore
	runMgr   runtime.Manager
}

func newDispatchTestEnv(t *testing.T, dagName string) dispatchTestEnv {
	t.Helper()

	root := t.TempDir()
	dagsDir := filepath.Join(root, "dags")
	dataDir := filepath.Join(root, "data")
	require.NoError(t, os.MkdirAll(dagsDir, 0o750))

	cfg := &config.Config{
		Core: config.Core{
			Location:      time.UTC,
			DefaultShell:  "/bin/sh",
			SkipExamples:  true,
			TZ:            "UTC",
			TzOffsetInSec: 0,
		},
		Paths: config.PathsConfig{
			DAGsDir:            dagsDir,
			LogDir:             filepath.Join(root, "logs"),
			ArtifactDir:        filepath.Join(root, "artifacts"),
			DataDir:            dataDir,
			SuspendFlagsDir:    filepath.Join(root, "flags"),
			DAGRunsDir:         filepath.Join(dataDir, "dag-runs"),
			QueueDir:           filepath.Join(dataDir, "queue"),
			ProcDir:            filepath.Join(dataDir, "proc"),
			ServiceRegistryDir: filepath.Join(dataDir, "service-registry"),
		},
		Queues: config.Queues{Enabled: true},
		Proc: config.Proc{
			HeartbeatInterval:     5 * time.Second,
			HeartbeatSyncInterval: 10 * time.Second,
			StaleThreshold:        90 * time.Second,
		},
		Scheduler: config.Scheduler{
			Port:               0,
			LockRetryInterval:  10 * time.Millisecond,
			LockStaleThreshold: 30 * time.Second,
		},
	}

	store := filedag.New(
		cfg.Paths.DAGsDir,
		filedag.WithFlagsBaseDir(cfg.Paths.SuspendFlagsDir),
		filedag.WithSkipExamples(true),
	)
	require.NoError(t, store.(*filedag.Storage).Initialize())

	dagFile := filepath.Join(dagsDir, dagName+".yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte("name: "+dagName+"\nsteps:\n  - name: step1\n    command: echo ok\n"), 0o600))

	ctx := context.Background()
	dag, err := spec.Load(ctx, dagFile)
	require.NoError(t, err)

	dagRuns := filedagrun.New(
		cfg.Paths.DAGRunsDir,
		filedagrun.WithArtifactDir(cfg.Paths.ArtifactDir),
		filedagrun.WithLocation(time.UTC),
	)
	proc := fileproc.New(
		cfg.Paths.ProcDir,
		fileproc.WithHeartbeatInterval(cfg.Proc.HeartbeatInterval),
		fileproc.WithHeartbeatSyncInterval(cfg.Proc.HeartbeatSyncInterval),
		fileproc.WithStaleThreshold(cfg.Proc.StaleThreshold),
	)
	queue := filequeue.New(cfg.Paths.QueueDir)
	runMgr := runtime.NewManager(dagRuns, proc, cfg)

	return dispatchTestEnv{
		ctx:      ctx,
		cfg:      cfg,
		dag:      dag,
		dagStore: store,
		dagRuns:  dagRuns,
		queue:    queue,
		proc:     proc,
		runMgr:   runMgr,
	}
}

type stubDispatchClient struct {
	accepts          []dispatchAcceptCall
	finishes         []dispatchFinishCall
	acceptErr        error
	finishErr        error
	finishErrByJobID map[string]error
}

type dispatchAcceptCall struct {
	jobID string
	req   license.AcceptGitHubDispatchRequest
}

type dispatchFinishCall struct {
	jobID string
	req   license.FinishGitHubDispatchRequest
}

func (s *stubDispatchClient) PullGitHubDispatch(context.Context, license.PullGitHubDispatchRequest) (*license.GitHubDispatchJob, error) {
	return nil, nil
}

func (s *stubDispatchClient) AcceptGitHubDispatch(_ context.Context, jobID string, req license.AcceptGitHubDispatchRequest) error {
	s.accepts = append(s.accepts, dispatchAcceptCall{jobID: jobID, req: req})
	return s.acceptErr
}

func (s *stubDispatchClient) FinishGitHubDispatch(_ context.Context, jobID string, req license.FinishGitHubDispatchRequest) error {
	s.finishes = append(s.finishes, dispatchFinishCall{jobID: jobID, req: req})
	if err, ok := s.finishErrByJobID[jobID]; ok {
		return err
	}
	return s.finishErr
}

type loopDispatchClient struct {
	stubDispatchClient
	pullCalls int
	pulled    chan struct{}
	once      sync.Once
}

func (l *loopDispatchClient) PullGitHubDispatch(context.Context, license.PullGitHubDispatchRequest) (*license.GitHubDispatchJob, error) {
	l.pullCalls++
	if l.pulled != nil {
		l.once.Do(func() {
			close(l.pulled)
		})
	}
	return nil, nil
}

type stubDispatchLicenseManager struct {
	cloudCreds *license.CloudMachineCredentials
	err        error
}

func newStubDispatchLicenseManager() *stubDispatchLicenseManager {
	return &stubDispatchLicenseManager{
		cloudCreds: &license.CloudMachineCredentials{
			LicenseID:       "lic-1",
			ServerID:        "srv-1",
			HeartbeatSecret: "secret-1",
		},
	}
}

func (s *stubDispatchLicenseManager) CloudMachineCredentials() (*license.CloudMachineCredentials, error) {
	return s.cloudCreds, s.err
}

func (s *stubDispatchLicenseManager) creds() githubDispatchCredentials {
	return githubDispatchCredentials{licenseID: "lic-1", serverID: "srv-1", secret: "secret-1"}
}

type stubDispatchRuntimeManager struct {
	calls []dispatchStopCall
}

type dispatchStopCall struct {
	dagName string
	runID   string
}

func (s *stubDispatchRuntimeManager) Stop(_ context.Context, dag *core.DAG, runID string) error {
	s.calls = append(s.calls, dispatchStopCall{dagName: dag.Name, runID: runID})
	return nil
}

type stubDispatchRunner struct {
	started chan struct{}
	once    sync.Once
}

func (s *stubDispatchRunner) Start(ctx context.Context) {
	s.once.Do(func() {
		s.started <- struct{}{}
	})
	<-ctx.Done()
}

type stubEntryReader struct {
	dagStore coreexec.DAGStore
}

func (s *stubEntryReader) Init(context.Context) error  { return nil }
func (s *stubEntryReader) Start(context.Context)       {}
func (s *stubEntryReader) Stop()                       {}
func (s *stubEntryReader) DAGs() []*core.DAG           { return nil }
func (s *stubEntryReader) DAGStore() coreexec.DAGStore { return s.dagStore }
