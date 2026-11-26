// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
)

const (
	jobReportPath = "/api/v1/runs/report"
	jobProtocol   = 1
	// runKeyPrefix starts every run key Dagu Console makes, and
	// runKeySecretLen hex characters, its 32 random bytes, follow it.
	runKeyPrefix    = "drk_"
	runKeySecretLen = 64

	// jobMinInterval and jobMaxInterval bound the wait between a run's
	// progress reports that Dagu Console asks for.
	jobMinInterval = 30 * time.Second
	jobMaxInterval = 5 * time.Minute
	// finishBudget is how long a finish is tried in all, so the process
	// exits soon after its run ends whether or not Dagu Console answers.
	finishBudget = 10 * time.Second
	// jobRequestTimeout bounds one request.
	jobRequestTimeout = 5 * time.Second
	// finishBackoff is the first wait before a finish is tried again; it
	// doubles after each failure.
	finishBackoff = 500 * time.Millisecond
	// maxJobReportBytes is the largest job report Dagu Console takes.
	maxJobReportBytes = 64 << 10
	// maxJobAnswerBytes bounds the answer read.
	maxJobAnswerBytes = 64 << 10
)

// What a job run's process prints about its reports. None names the run key
// or the workspace, since a job's logs are often public.
const (
	infoJobReporting  = "Reporting this run to Dagu Console"
	infoJobNotListed  = "Dagu Console counted this run but does not list it: this month's job runs are used"
	warnJobKeyRefused = "Dagu Console refused the run key, so this run reports no more; check DAGU_CLOUD_RUN_KEY"
	warnJobRefused    = "Dagu Console refused this run's finish"
	warnJobFinishLost = "Could not report this run's finish to Dagu Console; check that this machine can reach it"
)

// ValidRunKey reports whether key has the form of a run key from Dagu
// Console.
func ValidRunKey(key string) bool {
	secret, ok := strings.CutPrefix(key, runKeyPrefix)
	if !ok || len(secret) != runKeySecretLen {
		return false
	}
	_, err := hex.DecodeString(secret)
	return err == nil
}

// JobReports reports one top-level run to Dagu Console with a run key, for a
// machine that is no server, such as a Kubernetes Job or a CI step: when the
// run starts, about each interval Dagu Console asks for while it runs, at
// once when its status changes, and when it finishes. Reporting never
// changes the run.
type JobReports struct {
	// RunKey is the workspace's run key, and CloudURL Dagu Console's
	// address, empty for the production one.
	RunKey   string
	CloudURL string
	// Level is runs or errors; at runs a finish carries no error messages.
	Level config.ReportLevel
	// KeepTrigger sends the run's trigger. Without it a report carries
	// none, since a job started from the command line is rarely started by
	// a person, and Dagu Console shows the platform the run ran in instead.
	KeepTrigger bool
	// Version is the Dagu version the reports name.
	Version string
	// Getenv and Hostname tell where the run ran; nil reads the process's.
	Getenv   func(string) string
	Hostname func() (string, error)
	// Client sends the reports; nil uses Go's default transport, which
	// honors HTTPS_PROXY, NO_PROXY, and the system's certificates.
	Client *http.Client
	// ServerReports reports whether a server sends the run to Dagu Console
	// instead; nil is never. It is asked before each report, and the report
	// is left out while it says so.
	ServerReports func() bool

	// now, after, and random stand in for the clock and jitter in tests;
	// nil uses the real ones.
	now    func() time.Time
	after  func(time.Duration) <-chan time.Time
	random func() float64
}

// Wrap returns store, whose attempts report the run whose statuses the agent
// records through them.
func (j *JobReports) Wrap(store runstate.Store) runstate.Store {
	return &jobStore{Store: store, reports: j}
}

// jobStore is a run-state store whose new attempts report their run.
type jobStore struct {
	runstate.Store
	reports *JobReports
}

func (s *jobStore) BeginAttempt(ctx context.Context, req runstate.BeginAttemptRequest) (runstate.Attempt, error) {
	attempt, err := s.Store.BeginAttempt(ctx, req)
	if err != nil {
		return nil, err
	}
	return &jobAttempt{Attempt: attempt, reporter: newJobReporter(ctx, s.reports)}, nil
}

// jobAttempt passes each status the agent records to its reporter, and waits
// for the run's finish to be sent when the agent closes it.
type jobAttempt struct {
	runstate.Attempt
	reporter *jobReporter
}

func (a *jobAttempt) RecordStatus(ctx context.Context, status ir.DAGRunStatus) error {
	err := a.Attempt.RecordStatus(ctx, status)
	a.reporter.record(&status)
	return err
}

func (a *jobAttempt) Close(ctx context.Context) error {
	err := a.Attempt.Close(ctx)
	a.reporter.close()
	return err
}

// AttemptDir is the directory of the wrapped attempt, or "" when it has
// none. The agent stores what a run keeps there, such as a continuous step's
// samples, so reporting must not hide it.
func (a *jobAttempt) AttemptDir() string {
	if d, ok := a.Attempt.(interface{ AttemptDir() string }); ok {
		return d.AttemptDir()
	}
	return ""
}

// jobPlace is where a job run ran, as its reports carry it.
type jobPlace struct {
	Platform    string `json:"platform,omitempty"`
	PlatformURL string `json:"platform_url,omitempty"`
	Host        string `json:"host,omitempty"`
}

// jobActive is a job run in progress, and jobFinished one that ended, as a
// report carries it.
type jobActive struct {
	activeEntry
	jobPlace
}

type jobFinished struct {
	finishedRun
	jobPlace
}

// jobRequest is the body of POST /api/v1/runs/report: one run.
type jobRequest struct {
	Protocol int          `json:"protocol"`
	Active   *jobActive   `json:"active,omitempty"`
	Finished *jobFinished `json:"finished,omitempty"`
}

// jobAnswer is Dagu Console's answer to a job report. Listed is false, and
// Why says why, when it counted a finish but does not list it.
type jobAnswer struct {
	Stored            int     `json:"stored"`
	NextReportSeconds float64 `json:"next_report_seconds"`
	Listed            *bool   `json:"listed"`
	Why               string  `json:"why"`
}

// jobReporter sends one run's reports, one at a time, from its own
// goroutine. The agent's calls never wait on Dagu Console, except closing
// the attempt, which waits for the finish, or for a status change not yet
// sent.
type jobReporter struct {
	reports *JobReports
	// ctx outlives the run's cancellation, so a run stopped by SIGTERM
	// still sends its finish.
	ctx    context.Context
	client *http.Client
	url    string
	place  jobPlace
	agent  string

	now    func() time.Time
	after  func(time.Duration) <-chan time.Time
	random func() float64

	mu sync.Mutex
	// latest is the run in progress as last recorded, sent at once when
	// urgent and otherwise at the next interval; status is its run status.
	latest *jobRequest
	status string
	urgent bool
	// finish is the run that ended, sent once and tried until finishBy,
	// which counts from when the run ended, not from when the report in
	// flight then returned.
	finish   *jobRequest
	finishBy time.Time
	closing  bool
	// started is set once the run records a status to report, which starts
	// the goroutine; an attempt the agent never runs starts none.
	started bool
	wake    chan struct{}
	done    chan struct{}
}

func newJobReporter(ctx context.Context, reports *JobReports) *jobReporter {
	client := reports.Client
	if client == nil {
		client = &http.Client{}
	}
	r := &jobReporter{
		reports: reports,
		ctx:     context.WithoutCancel(ctx),
		client:  client,
		url:     license.NewCloudClient(reports.CloudURL).BaseURL() + jobReportPath,
		place:   placeOf(reports.Getenv, reports.Hostname),
		agent:   "dagu-oss/" + reports.Version,
		now:     time.Now,
		after:   time.After,
		random:  rand.Float64,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	if reports.now != nil {
		r.now = reports.now
	}
	if reports.after != nil {
		r.after = reports.after
	}
	if reports.random != nil {
		r.random = reports.random
	}
	return r
}

// record takes a status the agent recorded: a run in progress, reported at
// once when its status changed, or the run that ended. A run that has not
// started, a sub-DAG's status, or one with a name or ID longer than Dagu
// Console keeps, is left out.
func (r *jobReporter) record(status *ir.DAGRunStatus) {
	if !reportable(status) || (!status.Status.IsActive() && !status.Status.IsTerminal()) {
		return
	}
	if status.Status.IsActive() {
		req := r.activeRequest(status)
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.finish != nil || r.closing {
			return
		}
		r.start()
		r.latest = req
		if req.Active.Status != r.status {
			r.status, r.urgent = req.Active.Status, true
			r.signal()
		}
		return
	}
	req := r.finishedRequest(status)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finish == nil && !r.closing {
		r.start()
		r.finish, r.finishBy = req, r.now().Add(finishBudget)
		r.signal()
	}
}

// start says the run reports to Dagu Console and starts sending, the first
// time it records a status to report; r.mu is held.
func (r *jobReporter) start() {
	if r.started {
		return
	}
	r.started = true
	logger.Info(r.ctx, infoJobReporting)
	go r.run()
}

// close stops the reporter once the run's finish was sent or given up on,
// and returns then. Without a finish, it first sends a status change not
// yet sent, such as to waiting, and otherwise returns at once, as it does
// for a run that recorded nothing to report.
func (r *jobReporter) close() {
	r.mu.Lock()
	r.closing = true
	started := r.started
	r.signal()
	r.mu.Unlock()
	if started {
		<-r.done
	}
}

// signal wakes the reporter's goroutine; r.mu is held.
func (r *jobReporter) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *jobReporter) activeRequest(status *ir.DAGRunStatus) *jobRequest {
	run := newActiveRun(status)
	if !r.reports.KeepTrigger {
		run.TriggerType = ""
	}
	entry := activeEntry{activeRun: run}
	entry.Steps, entry.StepsTotal = runSteps(status)
	return &jobRequest{Protocol: jobProtocol, Active: &jobActive{activeEntry: entry, jobPlace: r.place}}
}

func (r *jobReporter) finishedRequest(status *ir.DAGRunStatus) *jobRequest {
	run := newFinishedRun(status, r.now())
	if !r.reports.KeepTrigger {
		run.TriggerType = ""
	}
	if r.reports.Level != config.ReportErrors {
		run.Errors = nil
	}
	return &jobRequest{Protocol: jobProtocol, Finished: &jobFinished{finishedRun: run, jobPlace: r.place}}
}

// run sends the reports: the run in progress when it changes and at each
// interval, then the finish. It ends after the finish, on a refused key,
// or when the attempt closes without a finish, after sending a status
// change not yet sent.
func (r *jobReporter) run() {
	defer close(r.done)
	interval := defaultInterval
	var tick <-chan time.Time
	for {
		due := false
		select {
		case <-r.wake:
		case <-tick:
			tick, due = nil, true
		}
		r.mu.Lock()
		finish, finishBy, closing := r.finish, r.finishBy, r.closing
		var progress *jobRequest
		if r.latest != nil && (r.urgent || due) {
			progress, r.urgent = r.latest, false
		}
		r.mu.Unlock()
		switch {
		case finish != nil:
			if !r.serverReports() {
				r.sendFinish(finish, finishBy)
			}
			return
		case progress != nil:
		case closing:
			return
		default:
			continue
		}
		next, ok := time.Duration(0), true
		if !r.serverReports() {
			next, ok = r.sendProgress(progress)
		}
		if !ok || closing {
			return
		}
		if next > 0 {
			interval = next
		}
		tick = r.after(r.jitter(interval))
	}
}

// serverReports reports whether a server sends the run now.
func (r *jobReporter) serverReports() bool {
	return r.reports.ServerReports != nil && r.reports.ServerReports()
}

// sendProgress sends the run in progress once. It returns the interval
// Dagu Console asks for, and false once the run key was refused.
func (r *jobReporter) sendProgress(req *jobRequest) (time.Duration, bool) {
	ctx, cancel := context.WithTimeout(r.ctx, jobRequestTimeout)
	defer cancel()
	res, err := r.post(ctx, req)
	switch {
	case err != nil:
		logger.Debug(r.ctx, "Failed to report this run's progress to Dagu Console", tag.Error(err))
	case res.status == http.StatusUnauthorized:
		logger.Warn(r.ctx, warnJobKeyRefused)
		return 0, false
	case res.ok():
		return res.answer.interval(), true
	default:
		logger.Debug(r.ctx, "Dagu Console did not take this run's progress", slog.Int("http_status", res.status))
	}
	return 0, true
}

// sendFinish sends the run that ended, trying again until deadline on a
// failure Dagu Console or the network may recover from.
func (r *jobReporter) sendFinish(req *jobRequest, deadline time.Time) {
	backoff := finishBackoff
	for {
		remaining := deadline.Sub(r.now())
		ctx, cancel := context.WithTimeout(r.ctx, min(jobRequestTimeout, remaining))
		res, err := r.post(ctx, req)
		cancel()
		wait := backoff
		switch {
		case err != nil:
			logger.Debug(r.ctx, "Failed to report this run's finish to Dagu Console", tag.Error(err))
			backoff *= 2
		case res.ok():
			if res.answer.Listed != nil && !*res.answer.Listed && res.answer.Why == "allowance" {
				logger.Info(r.ctx, infoJobNotListed)
			}
			return
		case res.status == http.StatusUnauthorized:
			logger.Warn(r.ctx, warnJobKeyRefused)
			return
		case res.status == http.StatusTooManyRequests, res.status == http.StatusServiceUnavailable:
			if res.retryAfter > 0 {
				wait = res.retryAfter
			} else {
				backoff *= 2
			}
		case res.status >= http.StatusInternalServerError:
			backoff *= 2
		default:
			logger.Warn(r.ctx, warnJobRefused, slog.Int("http_status", res.status))
			return
		}
		if !r.now().Add(wait).Before(deadline) {
			logger.Warn(r.ctx, warnJobFinishLost)
			return
		}
		<-r.after(wait)
	}
}

// jobResponse is how Dagu Console answered one report: its status, the
// wait its Retry-After asks for, and, for a success, its answer.
type jobResponse struct {
	status     int
	retryAfter time.Duration
	answer     jobAnswer
}

func (res jobResponse) ok() bool {
	return res.status >= http.StatusOK && res.status < http.StatusMultipleChoices
}

// post sends req and returns how Dagu Console answered, or an error when
// it did not. An answer whose body cannot be read keeps its status.
func (r *jobReporter) post(ctx context.Context, req *jobRequest) (jobResponse, error) {
	body, err := encodeJob(req)
	if err != nil {
		return jobResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return jobResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+r.reports.RunKey)
	httpReq.Header.Set("User-Agent", r.agent)
	resp, err := r.client.Do(httpReq)
	if err != nil {
		return jobResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	res := jobResponse{status: resp.StatusCode, retryAfter: r.retryAfter(resp.Header.Get("Retry-After"))}
	if !res.ok() {
		return res, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJobAnswerBytes))
	if err != nil {
		logger.Debug(r.ctx, "Failed to read Dagu Console's answer", tag.Error(err))
		return res, nil
	}
	_ = json.Unmarshal(data, &res.answer)
	return res, nil
}

// jitter varies d by up to intervalJitter either way, so the machines that
// started together report apart.
func (r *jobReporter) jitter(d time.Duration) time.Duration {
	return d + time.Duration((r.random()*2-1)*intervalJitter*float64(d))
}

// interval is the wait Dagu Console asks for before the next progress
// report, kept within [jobMinInterval, jobMaxInterval], or 0 when it asks
// for none.
func (a jobAnswer) interval() time.Duration {
	if a.NextReportSeconds <= 0 {
		return 0
	}
	d := time.Duration(a.NextReportSeconds * float64(time.Second))
	return min(max(d, jobMinInterval), jobMaxInterval)
}

// retryAfter is the wait a Retry-After header asks for, in seconds or as
// an HTTP date, or 0.
func (r *jobReporter) retryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if seconds, err := strconv.Atoi(header); err == nil {
		return max(time.Duration(seconds)*time.Second, 0)
	}
	if at, err := http.ParseTime(header); err == nil {
		return max(at.Sub(r.now()), 0)
	}
	return 0
}

// encodeJob encodes req within maxJobReportBytes, leaving out what matters
// least while it does not fit: the steps, then the error messages, then
// the failed step names past the first.
func encodeJob(req *jobRequest) ([]byte, error) {
	data, err := json.Marshal(req)
	if err != nil || len(data) <= maxJobReportBytes {
		return data, err
	}
	shrunk := *req
	trims := []func(){
		func() {
			if shrunk.Active != nil {
				active := *shrunk.Active
				active.Steps, active.StepsTotal = nil, 0
				shrunk.Active = &active
			}
			if shrunk.Finished != nil {
				finished := *shrunk.Finished
				finished.Steps, finished.StepsTotal = nil, 0
				shrunk.Finished = &finished
			}
		},
		func() {
			if shrunk.Finished != nil {
				finished := *shrunk.Finished
				finished.Errors = nil
				shrunk.Finished = &finished
			}
		},
		func() {
			if shrunk.Finished != nil && len(shrunk.Finished.FailedSteps) > 1 {
				finished := *shrunk.Finished
				finished.FailedSteps = finished.FailedSteps[:1]
				shrunk.Finished = &finished
			}
		},
	}
	for _, trim := range trims {
		trim()
		if data, err = json.Marshal(shrunk); err != nil || len(data) <= maxJobReportBytes {
			return data, err
		}
	}
	return data, nil
}
