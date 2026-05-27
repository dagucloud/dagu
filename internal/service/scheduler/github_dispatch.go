// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/githubdispatch"
	"github.com/dagucloud/dagu/internal/license"
)

const (
	githubDispatchPendingAccept = "pending_accept"
	githubDispatchAccepted      = "accepted"
	githubDispatchIdleDelay     = 10 * time.Second
	githubDispatchErrorDelay    = 10 * time.Second
	githubDispatchReportDelay   = 10 * time.Second
)

type githubDispatchClient interface {
	PullGitHubDispatch(context.Context, license.PullGitHubDispatchRequest) (*license.GitHubDispatchJob, error)
	AcceptGitHubDispatch(context.Context, string, license.AcceptGitHubDispatchRequest) error
	FinishGitHubDispatch(context.Context, string, license.FinishGitHubDispatchRequest) error
}

type githubDispatchTracker interface {
	Upsert(context.Context, githubdispatch.TrackedJob) error
	Delete(context.Context, string) error
	List(context.Context) ([]githubdispatch.TrackedJob, error)
}

type githubDispatchLicenseManager interface {
	CloudMachineCredentials() (*license.CloudMachineCredentials, error)
}

type githubDispatchRunner interface {
	Start(context.Context)
}

type githubDispatchWorker struct {
	cfg       *config.Config
	dagStore  coreexec.DAGStore
	dagRuns   coreexec.DAGRunStore
	queue     coreexec.QueueStore
	runMgr    githubDispatchRuntimeManager
	licenses  githubDispatchLicenseManager
	client    githubDispatchClient
	tracker   githubDispatchTracker
	now       func() time.Time
	idleDelay time.Duration
	errDelay  time.Duration
	reportGap time.Duration
}

type githubDispatchRuntimeManager interface {
	Stop(context.Context, *core.DAG, string) error
}

type githubDispatchCredentials struct {
	licenseID string
	serverID  string
	secret    string
}

func NewGitHubDispatchWorker(
	cfg *config.Config,
	dagStore coreexec.DAGStore,
	dagRuns coreexec.DAGRunStore,
	queue coreexec.QueueStore,
	runMgr githubDispatchRuntimeManager,
	licenses githubDispatchLicenseManager,
	client githubDispatchClient,
	tracker githubDispatchTracker,
	_ logger.Logger,
) *githubDispatchWorker {
	return &githubDispatchWorker{
		cfg:       cfg,
		dagStore:  dagStore,
		dagRuns:   dagRuns,
		queue:     queue,
		runMgr:    runMgr,
		licenses:  licenses,
		client:    client,
		tracker:   tracker,
		now:       time.Now,
		idleDelay: githubDispatchIdleDelay,
		errDelay:  githubDispatchErrorDelay,
		reportGap: githubDispatchReportDelay,
	}
}

func (w *githubDispatchWorker) Start(ctx context.Context) {
	creds, enabled, err := w.credentials()
	if err != nil {
		logger.Warn(ctx, "GitHub dispatch worker disabled", tag.Error(err))
		return
	}
	if !enabled {
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		w.pullLoop(ctx, creds)
	}()
	go func() {
		defer wg.Done()
		w.reportLoop(ctx, creds)
	}()
	wg.Wait()
}

func (w *githubDispatchWorker) credentials() (githubDispatchCredentials, bool, error) {
	if w == nil || w.cfg == nil || !w.cfg.Queues.Enabled || w.client == nil || w.dagStore == nil || w.dagRuns == nil || w.queue == nil || w.tracker == nil || w.licenses == nil {
		return githubDispatchCredentials{}, false, nil
	}

	cloudCreds, err := w.licenses.CloudMachineCredentials()
	if err != nil {
		return githubDispatchCredentials{}, false, fmt.Errorf("load cloud machine credentials: %w", err)
	}
	if cloudCreds == nil || cloudCreds.LicenseID == "" || cloudCreds.ServerID == "" || cloudCreds.HeartbeatSecret == "" {
		return githubDispatchCredentials{}, false, nil
	}

	return githubDispatchCredentials{
		licenseID: cloudCreds.LicenseID,
		serverID:  cloudCreds.ServerID,
		secret:    cloudCreds.HeartbeatSecret,
	}, true, nil
}

func (w *githubDispatchWorker) pullLoop(ctx context.Context, creds githubDispatchCredentials) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := w.client.PullGitHubDispatch(ctx, license.PullGitHubDispatchRequest{
			LicenseID:       creds.licenseID,
			ServerID:        creds.serverID,
			HeartbeatSecret: creds.secret,
		})
		if err != nil {
			logger.Error(ctx, "GitHub dispatch pull failed", tag.Error(err))
			if !sleepContext(ctx, w.errDelay) {
				return
			}
			continue
		}
		if job == nil {
			if !sleepContext(ctx, w.idleDelay) {
				return
			}
			continue
		}
		if err := w.processJob(ctx, creds, *job); err != nil {
			logger.Error(ctx, "GitHub dispatch processing failed",
				slog.String("job_id", job.ID),
				tag.Error(err),
			)
			if !sleepContext(ctx, w.errDelay) {
				return
			}
		}
	}
}

func (w *githubDispatchWorker) reportLoop(ctx context.Context, creds githubDispatchCredentials) {
	for {
		if err := w.reportTrackedJobs(ctx, creds); err != nil {
			logger.Error(ctx, "GitHub dispatch reporting failed", tag.Error(err))
		}
		if !sleepContext(ctx, w.reportGap) {
			return
		}
	}
}

func (w *githubDispatchWorker) processJob(ctx context.Context, creds githubDispatchCredentials, job license.GitHubDispatchJob) error {
	if strings.EqualFold(job.Command, "cancel") {
		return w.processCancelJob(ctx, creds, job)
	}

	dag, err := w.dagStore.GetMetadata(ctx, job.DAGName)
	if err != nil {
		return fmt.Errorf("load dag %q: %w", job.DAGName, err)
	}

	runID := job.ID
	params := buildGitHubDispatchRuntimeParams(job)
	if err := EnqueueWebhookRun(
		ctx,
		w.dagRuns,
		w.queue,
		w.cfg.Paths.LogDir,
		w.cfg.Paths.ArtifactDir,
		w.cfg.Paths.BaseConfig,
		dag,
		runID,
		params,
		w.now(),
	); err != nil {
		return err
	}

	tracked := githubdispatch.TrackedJob{
		JobID:     job.ID,
		DAGName:   job.DAGName,
		DAGRunID:  runID,
		Phase:     githubDispatchPendingAccept,
		UpdatedAt: w.now().UTC(),
	}
	if err := w.tracker.Upsert(ctx, tracked); err != nil {
		return fmt.Errorf("persist tracked job: %w", err)
	}
	return w.acceptTrackedJob(ctx, creds, tracked)
}

func (w *githubDispatchWorker) processCancelJob(ctx context.Context, creds githubDispatchCredentials, job license.GitHubDispatchJob) error {
	return w.client.FinishGitHubDispatch(ctx, job.ID, license.FinishGitHubDispatchRequest{
		LicenseID:     creds.licenseID,
		ServerID:      creds.serverID,
		Secret:        creds.secret,
		ResultStatus:  core.Aborted.String(),
		ResultSummary: fmt.Sprintf("GitHub dispatch command %q is not supported by this worker.", job.Command),
	})
}

func (w *githubDispatchWorker) acceptTrackedJob(ctx context.Context, creds githubDispatchCredentials, tracked githubdispatch.TrackedJob) error {
	if err := w.client.AcceptGitHubDispatch(ctx, tracked.JobID, license.AcceptGitHubDispatchRequest{
		LicenseID: creds.licenseID,
		ServerID:  creds.serverID,
		Secret:    creds.secret,
		DAGRunID:  tracked.DAGRunID,
	}); err != nil {
		return err
	}
	tracked.Phase = githubDispatchAccepted
	tracked.UpdatedAt = w.now().UTC()
	return w.tracker.Upsert(ctx, tracked)
}

func (w *githubDispatchWorker) reportTrackedJobs(ctx context.Context, creds githubDispatchCredentials) error {
	tracked, err := w.tracker.List(ctx)
	if err != nil {
		return fmt.Errorf("list tracked jobs: %w", err)
	}
	var reportErrs []error
	for _, item := range tracked {
		if err := w.handleTrackedJob(ctx, creds, item); err != nil {
			reportErrs = append(reportErrs, fmt.Errorf("job %s: %w", item.JobID, err))
		}
	}
	if len(reportErrs) > 0 {
		return errors.Join(reportErrs...)
	}
	return nil
}

func (w *githubDispatchWorker) handleTrackedJob(ctx context.Context, creds githubDispatchCredentials, tracked githubdispatch.TrackedJob) error {
	if tracked.Phase == githubDispatchPendingAccept {
		if err := w.acceptTrackedJob(ctx, creds, tracked); err != nil {
			return err
		}
		tracked.Phase = githubDispatchAccepted
	}

	attempt, err := w.dagRuns.FindAttempt(ctx, coreexec.NewDAGRunRef(tracked.DAGName, tracked.DAGRunID))
	if err != nil {
		if errors.Is(err, coreexec.ErrDAGRunIDNotFound) {
			return nil
		}
		return fmt.Errorf("find dag run %s/%s: %w", tracked.DAGName, tracked.DAGRunID, err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read dag run %s/%s status: %w", tracked.DAGName, tracked.DAGRunID, err)
	}
	if status.Status.IsActive() {
		return nil
	}

	if err := w.client.FinishGitHubDispatch(ctx, tracked.JobID, license.FinishGitHubDispatchRequest{
		LicenseID:     creds.licenseID,
		ServerID:      creds.serverID,
		Secret:        creds.secret,
		ResultStatus:  status.Status.String(),
		ResultSummary: summarizeDispatchStatus(status),
	}); err != nil {
		return err
	}
	return w.tracker.Delete(ctx, tracked.JobID)
}

func summarizeDispatchStatus(status *coreexec.DAGRunStatus) string {
	if status == nil {
		return "Dagu run finished."
	}
	summary := fmt.Sprintf("Dagu run `%s` finished with `%s`.", status.Name, status.Status.String())
	if errs := status.Errors(); len(errs) > 0 {
		summary += "\n\n" + errs[0].Error()
	}
	return summary
}

func buildGitHubDispatchRuntimeParams(job license.GitHubDispatchJob) string {
	payload := string(job.Payload)
	if payload == "" {
		payload = "{}"
	}
	headers := string(job.Headers)
	if headers == "" {
		headers = "{}"
	}

	extras := map[string]string{
		"GITHUB_ACTOR":        job.ActorLogin,
		"GITHUB_COMMAND":      job.Command,
		"GITHUB_EVENT_ACTION": job.EventAction,
		"GITHUB_EVENT_NAME":   job.EventName,
		"GITHUB_REF":          job.Ref,
		"GITHUB_REPOSITORY":   job.RepositoryName,
		"GITHUB_SHA":          job.SHA,
	}
	if job.PullRequestNumber > 0 {
		extras["GITHUB_PR_NUMBER"] = strconv.FormatInt(job.PullRequestNumber, 10)
	}
	if job.IssueNumber > 0 {
		extras["GITHUB_ISSUE_NUMBER"] = strconv.FormatInt(job.IssueNumber, 10)
	}

	var body map[string]any
	if err := json.Unmarshal(job.Payload, &body); err == nil {
		if tagName := nestedDispatchString(body, "release", "tag_name"); tagName != "" {
			extras["GITHUB_RELEASE_TAG"] = tagName
		} else if after, ok := strings.CutPrefix(job.Ref, "refs/tags/"); ok {
			extras["GITHUB_RELEASE_TAG"] = after
		}

		switch workflow := body["workflow"].(type) {
		case string:
			extras["GITHUB_WORKFLOW"] = workflow
		case map[string]any:
			if name := nestedDispatchString(workflow, "name"); name != "" {
				extras["GITHUB_WORKFLOW"] = name
			} else if path := nestedDispatchString(workflow, "path"); path != "" {
				extras["GITHUB_WORKFLOW"] = path
			}
		}

		if eventType, ok := body["event_type"].(string); ok {
			extras["GITHUB_DISPATCH_EVENT_TYPE"] = eventType
		}
	}

	return core.BuildWebhookRuntimeParams(payload, headers, extras)
}

func nestedDispatchString(m map[string]any, path ...string) string {
	cur := any(m)
	for _, part := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = next[part]
	}
	if str, ok := cur.(string); ok {
		return str
	}
	return ""
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *Scheduler) SetGitHubDispatchWorker(worker githubDispatchRunner) {
	if s == nil {
		return
	}
	s.githubDispatch = worker
}

func (s *Scheduler) startGitHubDispatch(ctx context.Context) {
	if s == nil || s.githubDispatch == nil {
		return
	}
	s.githubDispatch.Start(logger.WithValues(ctx, slog.String("component", "github_dispatch")))
}
