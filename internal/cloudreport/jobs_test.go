// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
)

const testRunKey = "drk_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// jobCall is a job report Dagu Console received.
type jobCall struct {
	auth, agent string
	body        map[string]any
}

// jobReply is how the fake Console answers one report: after hold closes
// when it is set, and with a body cut short when incomplete.
type jobReply struct {
	status     int
	retryAfter string
	body       string
	incomplete bool
	hold       chan struct{}
}

// fakeJobConsole takes job reports, answering each with the next reply
// queued, or 200 asking for reports each minute.
type fakeJobConsole struct {
	server *httptest.Server
	calls  chan jobCall
	mu     sync.Mutex
	queued []jobReply
}

func newFakeJobConsole(t *testing.T) *fakeJobConsole {
	t.Helper()
	c := &fakeJobConsole{calls: make(chan jobCall, 64)}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, jobReportPath, r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		assert.NoError(t, json.Unmarshal(data, &body))
		assert.LessOrEqual(t, len(data), maxJobReportBytes)
		reply := c.reply()
		c.calls <- jobCall{auth: r.Header.Get("Authorization"), agent: r.Header.Get("User-Agent"), body: body}
		if reply.hold != nil {
			<-reply.hold
		}
		if reply.retryAfter != "" {
			w.Header().Set("Retry-After", reply.retryAfter)
		}
		if reply.incomplete {
			// The server closes the connection after fewer bytes than declared.
			w.Header().Set("Content-Length", strconv.Itoa(len(reply.body)+64))
		}
		w.WriteHeader(reply.status)
		_, _ = io.WriteString(w, reply.body)
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *fakeJobConsole) answer(replies ...jobReply) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queued = append(c.queued, replies...)
}

func (c *fakeJobConsole) reply() jobReply {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queued) == 0 {
		return jobReply{status: http.StatusOK, body: `{"stored":1,"next_report_seconds":60}`}
	}
	reply := c.queued[0]
	c.queued = c.queued[1:]
	return reply
}

// next returns the next report Console receives.
func (c *fakeJobConsole) next(t *testing.T) jobCall {
	t.Helper()
	select {
	case call := <-c.calls:
		return call
	case <-time.After(5 * time.Second):
		t.Fatal("Dagu Console received no report")
		return jobCall{}
	}
}

// jobWait is a wait the reporter asked its clock for.
type jobWait struct {
	d    time.Duration
	fire chan time.Time
}

// jobClock hands each wait the reporter asks for to the test, and moves its
// time on by a wait when the test ends it.
type jobClock struct {
	mu    sync.Mutex
	at    time.Time
	waits chan jobWait
}

func newJobClock() *jobClock {
	return &jobClock{at: testRecordedAt, waits: make(chan jobWait, 16)}
}

func (c *jobClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *jobClock) after(d time.Duration) <-chan time.Time {
	w := jobWait{d: d, fire: make(chan time.Time, 1)}
	c.waits <- w
	return w.fire
}

// wait returns the next wait the reporter asks for.
func (c *jobClock) wait(t *testing.T) jobWait {
	t.Helper()
	select {
	case w := <-c.waits:
		return w
	case <-time.After(5 * time.Second):
		t.Fatal("the reporter asked for no wait")
		return jobWait{}
	}
}

// end ends w, moving the time on by it.
func (c *jobClock) end(w jobWait) {
	c.advance(w.d)
	w.fire <- c.now()
}

// advance moves the time on by d, as a report in flight takes it.
func (c *jobClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// githubEnv is a GitHub Actions job's environment.
func githubEnv(key string) string {
	return map[string]string{
		"GITHUB_ACTIONS": "true", "GITHUB_SERVER_URL": "https://github.com", "GITHUB_REPOSITORY": "acme/etl",
		"GITHUB_RUN_ID": "42", "GITHUB_RUN_ATTEMPT": "2", "KUBERNETES_SERVICE_HOST": "10.0.0.1",
	}[key]
}

// newJobAttempt begins an attempt whose statuses report to console as a job
// run, with clock, on a GitHub Actions runner named runner-1.
func newJobAttempt(ctx context.Context, t *testing.T, console *fakeJobConsole, clock *jobClock, change func(*JobReports)) runstate.Attempt {
	t.Helper()
	reports := &JobReports{
		RunKey:   testRunKey,
		CloudURL: console.server.URL,
		Level:    config.ReportErrors,
		Version:  "1.2.3",
		Getenv:   githubEnv,
		Hostname: func() (string, error) { return "runner-1", nil },
		now:      clock.now,
		after:    clock.after,
		random:   func() float64 { return 0.5 },
	}
	if change != nil {
		change(reports)
	}
	attempt, err := reports.Wrap(noopRunState{}).BeginAttempt(ctx, runstate.BeginAttemptRequest{RunID: "run-1", AttemptID: "attempt-1"})
	require.NoError(t, err)
	return attempt
}

// noopRunState begins attempts that keep nothing.
type noopRunState struct{ runstate.Store }

func (noopRunState) BeginAttempt(_ context.Context, req runstate.BeginAttemptRequest) (runstate.Attempt, error) {
	return runstate.NewNoopAttempt(req), nil
}

// jobStatus is etl's run-1 with status, extracting and then loading.
func jobStatus(status ir.Status) ir.DAGRunStatus {
	st := *etlRun(status)
	st.TriggerType = ir.TriggerTypeManual
	st.Nodes = []*ir.Node{
		{Step: ir.Step{Name: "extract"}, Status: ir.NodeSucceeded, StartedAt: "2026-10-10T09:00:00Z", FinishedAt: "2026-10-10T09:01:00Z"},
		{Step: ir.Step{Name: "load"}, Status: ir.NodeRunning, StartedAt: "2026-10-10T09:01:00Z"},
	}
	if status == ir.Failed {
		st.Nodes[1].Status, st.Nodes[1].Error, st.Nodes[1].FinishedAt = ir.NodeFailed, "connection refused", "2026-10-10T09:04:00Z"
	}
	return st
}

// entry returns the run a report carries, in progress or finished.
func (c jobCall) entry(t *testing.T, kind string) map[string]any {
	t.Helper()
	entry, ok := c.body[kind].(map[string]any)
	require.True(t, ok, "the report carries a run %s: %v", kind, c.body)
	return entry
}

// A run reports when it starts, at once when its status changes, at each
// interval Dagu Console asks for with its latest steps, and when it
// finishes, with where it ran and no trigger: on a job's machine the
// command line rarely means a person.
func TestJobRunReportsAsItGoes(t *testing.T) {
	t.Parallel()
	console := newFakeJobConsole(t)
	clock := newJobClock()
	attempt := newJobAttempt(t.Context(), t, console, clock, nil)
	console.answer(jobReply{status: http.StatusOK, body: `{"stored":0,"next_report_seconds":10}`})

	running := jobStatus(ir.Running)
	require.NoError(t, attempt.RecordStatus(t.Context(), running))
	start := console.next(t)
	assert.Equal(t, "Bearer "+testRunKey, start.auth)
	assert.Equal(t, "dagu-oss/1.2.3", start.agent)
	assert.EqualValues(t, jobProtocol, start.body["protocol"])
	active := start.entry(t, "active")
	assert.Equal(t, "running", active["status"])
	assert.Equal(t, "", active["trigger_type"])
	assert.Equal(t, "github_actions", active["platform"])
	assert.Equal(t, "https://github.com/acme/etl/actions/runs/42/attempts/2", active["platform_url"])
	assert.Equal(t, "runner-1", active["host"])
	assert.EqualValues(t, 2, active["steps_total"])

	// Console asked for 10 seconds, which is kept to 30.
	tick := clock.wait(t)
	assert.Equal(t, jobMinInterval, tick.d)
	running.Nodes[1].Status = ir.NodeSucceeded
	require.NoError(t, attempt.RecordStatus(t.Context(), running))
	clock.end(tick)
	progress := console.next(t).entry(t, "active")
	steps := progress["steps"].([]any)
	assert.Equal(t, "succeeded", steps[1].(map[string]any)["status"], "the interval's report carries the latest steps")

	tick = clock.wait(t)
	assert.Equal(t, time.Minute, tick.d)
	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Waiting)))
	assert.Equal(t, "waiting", console.next(t).entry(t, "active")["status"], "a status change reports at once")

	clock.wait(t)
	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Failed)))
	finished := console.next(t).entry(t, "finished")
	assert.Equal(t, "failed", finished["status"])
	assert.Equal(t, []any{"load"}, finished["failed_steps"])
	assert.Equal(t, "connection refused", finished["errors"].([]any)[0].(map[string]any)["message"])
	assert.Equal(t, "github_actions", finished["platform"])
	require.NoError(t, attempt.Close(t.Context()))
	assert.Empty(t, console.calls, "nothing follows the finish")
}

// At the runs level a finish carries no error messages, and a trigger passed
// on the command line is kept.
func TestJobRunReportsKeepTheLevelAndAPassedTrigger(t *testing.T) {
	t.Parallel()
	console := newFakeJobConsole(t)
	attempt := newJobAttempt(t.Context(), t, console, newJobClock(), func(r *JobReports) {
		r.Level, r.KeepTrigger = config.ReportRuns, true
	})

	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Failed)))
	finished := console.next(t).entry(t, "finished")
	assert.NotContains(t, finished, "errors")
	assert.Equal(t, "manual", finished["trigger_type"])
	require.NoError(t, attempt.Close(t.Context()))
}

// A finish is tried again after the wait Console asks for while it is busy,
// in seconds or as a date, even when its answer is cut short, and after a
// growing wait on a server error, within its 10 seconds.
func TestJobFinishIsTriedAgainWithinItsBudget(t *testing.T) {
	t.Parallel()
	console := newFakeJobConsole(t)
	clock := newJobClock()
	attempt := newJobAttempt(t.Context(), t, console, clock, nil)
	console.answer(
		jobReply{status: http.StatusServiceUnavailable, retryAfter: "2", body: `{}`, incomplete: true},
		jobReply{status: http.StatusTooManyRequests, retryAfter: testRecordedAt.Add(5 * time.Second).Format(http.TimeFormat)},
		jobReply{status: http.StatusInternalServerError, body: `{}`},
	)

	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Failed)))
	console.next(t)
	busy := clock.wait(t)
	assert.Equal(t, 2*time.Second, busy.d)
	clock.end(busy)
	console.next(t)
	limited := clock.wait(t)
	assert.Equal(t, 3*time.Second, limited.d, "the date is 3 seconds on")
	clock.end(limited)
	console.next(t)
	failed := clock.wait(t)
	assert.Equal(t, finishBackoff, failed.d)
	clock.end(failed)
	console.next(t)
	require.NoError(t, attempt.Close(t.Context()))
}

// A busy Console that names no wait gets a growing one, as on a server
// error, so a finish does not retry it every half second.
func TestJobFinishBacksOffWhenBusy(t *testing.T) {
	t.Parallel()
	console := newFakeJobConsole(t)
	clock := newJobClock()
	attempt := newJobAttempt(t.Context(), t, console, clock, nil)
	console.answer(jobReply{status: http.StatusServiceUnavailable}, jobReply{status: http.StatusTooManyRequests})

	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Failed)))
	for _, want := range []time.Duration{finishBackoff, 2 * finishBackoff} {
		console.next(t)
		wait := clock.wait(t)
		assert.Equal(t, want, wait.d)
		clock.end(wait)
	}
	console.next(t)
	require.NoError(t, attempt.Close(t.Context()))
}

// A finish Console keeps failing is given up after its budget with one
// warning, and a refused key stops at once with one warning.
func TestJobFinishGivesUpAndSaysSo(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		replies []jobReply
		warning string
	}{
		"failing": {
			replies: []jobReply{{status: 500}, {status: 500}, {status: 500}, {status: 500}, {status: 500}},
			warning: warnJobFinishLost,
		},
		"refused key": {replies: []jobReply{{status: http.StatusUnauthorized}}, warning: warnJobKeyRefused},
		"refused key, answer cut short": {
			replies: []jobReply{{status: http.StatusUnauthorized, body: `{}`, incomplete: true}},
			warning: warnJobKeyRefused,
		},
		"malformed": {replies: []jobReply{{status: http.StatusBadRequest}}, warning: warnJobRefused},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			ctx := logger.WithLogger(t.Context(), logger.NewLogger(
				logger.WithFormat("text"), logger.WithWriter(&logs), logger.WithQuiet()))
			console := newFakeJobConsole(t)
			clock := newJobClock()
			attempt := newJobAttempt(ctx, t, console, clock, nil)
			console.answer(tc.replies...)

			require.NoError(t, attempt.RecordStatus(ctx, jobStatus(ir.Failed)))
			closed := make(chan error, 1)
			go func() { closed <- attempt.Close(ctx) }()
			for range tc.replies {
				console.next(t)
				select {
				case w := <-clock.waits:
					clock.end(w)
				case err := <-closed:
					require.NoError(t, err)
					closed <- err
				}
			}
			require.NoError(t, <-closed)
			assert.Equal(t, 1, strings.Count(logs.String(), tc.warning), logs.String())
			assert.NotContains(t, logs.String(), testRunKey)
		})
	}
}

// A finish is tried for 10 seconds from when its run ended, however long
// the report then in flight took.
func TestJobFinishBudgetCountsFromTheRunsEnd(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"), logger.WithWriter(&logs), logger.WithQuiet()))
	console := newFakeJobConsole(t)
	clock := newJobClock()
	attempt := newJobAttempt(ctx, t, console, clock, nil)
	inFlight := make(chan struct{})
	console.answer(
		jobReply{status: http.StatusOK, body: `{"stored":0,"next_report_seconds":60}`, hold: inFlight},
		jobReply{status: http.StatusServiceUnavailable, retryAfter: "3"},
	)

	require.NoError(t, attempt.RecordStatus(ctx, jobStatus(ir.Running)))
	console.next(t)
	require.NoError(t, attempt.RecordStatus(ctx, jobStatus(ir.Failed)))
	clock.advance(8 * time.Second)
	close(inFlight)
	clock.wait(t) // the next progress report's interval
	console.next(t)

	closed := make(chan error, 1)
	go func() { closed <- attempt.Close(ctx) }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case w := <-clock.waits:
		t.Fatalf("the finish waits %s more, past its 10 seconds", w.d)
	}
	assert.Equal(t, 1, strings.Count(logs.String(), warnJobFinishLost))
}

// A finish Console counted but does not list, past the month's allowance,
// says so in the run's log.
func TestJobFinishPastTheAllowanceSaysSo(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"), logger.WithWriter(&logs), logger.WithQuiet()))
	console := newFakeJobConsole(t)
	attempt := newJobAttempt(ctx, t, console, newJobClock(), nil)
	console.answer(jobReply{status: http.StatusOK, body: `{"stored":0,"listed":false,"why":"allowance"}`})

	require.NoError(t, attempt.RecordStatus(ctx, jobStatus(ir.Succeeded)))
	console.next(t)
	require.NoError(t, attempt.Close(ctx))
	assert.Equal(t, 1, strings.Count(logs.String(), infoJobNotListed))
	assert.Equal(t, 1, strings.Count(logs.String(), infoJobReporting))
}

// An attempt the agent never runs, such as one of a run that already
// finished, reports nothing and says nothing of reporting.
func TestJobAttemptThatRecordsNothingReportsNothing(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"), logger.WithWriter(&logs), logger.WithQuiet()))
	console := newFakeJobConsole(t)
	attempt := newJobAttempt(ctx, t, console, newJobClock(), nil)

	require.NoError(t, attempt.RecordStatus(ctx, jobStatus(ir.NotStarted)))
	require.NoError(t, attempt.Close(ctx))
	assert.Empty(t, console.calls)
	assert.NotContains(t, logs.String(), infoJobReporting)
}

// An attempt closed before its run ends sends nothing more.
func TestJobAttemptClosedWithoutAFinish(t *testing.T) {
	t.Parallel()
	console := newFakeJobConsole(t)
	clock := newJobClock()
	attempt := newJobAttempt(t.Context(), t, console, clock, nil)

	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Running)))
	console.next(t)
	clock.wait(t)
	require.NoError(t, attempt.Close(t.Context()))
	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Failed)))
	assert.Empty(t, console.calls)
}

// A run that stops to wait for an approval records waiting as its last
// status and closes its attempt at once; the close still sends it.
func TestJobAttemptClosedWhileWaiting(t *testing.T) {
	t.Parallel()
	console := newFakeJobConsole(t)
	clock := newJobClock()
	attempt := newJobAttempt(t.Context(), t, console, clock, nil)

	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Running)))
	console.next(t)
	clock.wait(t)
	require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Waiting)))
	require.NoError(t, attempt.Close(t.Context()))

	assert.Equal(t, "waiting", console.next(t).entry(t, "active")["status"])
	assert.Empty(t, console.calls)
}

// A run sends nothing while a server reports it, and sends its finish when
// the server has stopped by then, so a run is never left unreported.
func TestJobRunLeavesItsReportsToAServer(t *testing.T) {
	t.Parallel()
	for name, serverAtFinish := range map[string]bool{
		"ServerStillReports": true,
		"ServerStopped":      false,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			console := newFakeJobConsole(t)
			clock := newJobClock()
			var server atomic.Bool
			server.Store(true)
			attempt := newJobAttempt(t.Context(), t, console, clock, func(r *JobReports) {
				r.ServerReports = server.Load
			})

			require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Running)))
			clock.wait(t)
			assert.Empty(t, console.calls, "the server reports the run in progress")

			server.Store(serverAtFinish)
			require.NoError(t, attempt.RecordStatus(t.Context(), jobStatus(ir.Failed)))
			require.NoError(t, attempt.Close(t.Context()))
			if serverAtFinish {
				assert.Empty(t, console.calls)
				return
			}
			assert.Equal(t, "failed", console.next(t).entry(t, "finished")["status"])
			assert.Empty(t, console.calls)
		})
	}
}

// dirRunState begins attempts that live in a directory, as the file store's
// do.
type dirRunState struct{ runstate.Store }

type dirAttempt struct{ runstate.Attempt }

func (dirAttempt) AttemptDir() string { return "/data/runs/attempt-1" }

func (dirRunState) BeginAttempt(_ context.Context, req runstate.BeginAttemptRequest) (runstate.Attempt, error) {
	return dirAttempt{Attempt: runstate.NewNoopAttempt(req)}, nil
}

// A reported attempt keeps its directory, where the agent stores what a
// run keeps there, such as a continuous step's samples.
func TestJobAttemptKeepsItsDirectory(t *testing.T) {
	t.Parallel()
	reports := &JobReports{RunKey: testRunKey, CloudURL: newFakeJobConsole(t).server.URL}
	attempt, err := reports.Wrap(dirRunState{}).BeginAttempt(t.Context(), runstate.BeginAttemptRequest{RunID: "run-1"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = attempt.Close(context.Background()) })

	dir, ok := attempt.(interface{ AttemptDir() string })
	require.True(t, ok, "the attempt names its directory")
	assert.Equal(t, "/data/runs/attempt-1", dir.AttemptDir())
}

// A report always fits what Console takes: what matters least goes first.
func TestJobReportFitsDaguConsole(t *testing.T) {
	t.Parallel()
	status := jobStatus(ir.Failed)
	name := strings.Repeat("\u0001", maxStepNameBytes)
	status.Nodes = nil
	for range maxRunSteps {
		status.Nodes = append(status.Nodes, &ir.Node{Step: ir.Step{Name: name}, Status: ir.NodeFailed, Error: strings.Repeat("\u0001", 4096)})
	}
	r := &jobReporter{reports: &JobReports{Level: config.ReportErrors}, now: time.Now}

	data, err := encodeJob(r.finishedRequest(&status))

	require.NoError(t, err)
	assert.LessOrEqual(t, len(data), maxJobReportBytes)
	var req jobRequest
	require.NoError(t, json.Unmarshal(data, &req))
	assert.Equal(t, "failed", req.Finished.Status)
	assert.NotEmpty(t, req.Finished.FailedSteps)
}

// Where a job runs is told from its environment: CI systems before
// Kubernetes, which many CI runners run on, and a link only when it is
// https.
func TestJobPlace(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		env  map[string]string
		want jobPlace
	}{
		"github actions": {
			env:  map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_SERVER_URL": "https://github.com", "GITHUB_REPOSITORY": "acme/etl", "GITHUB_RUN_ID": "42"},
			want: jobPlace{Platform: "github_actions", PlatformURL: "https://github.com/acme/etl/actions/runs/42", Host: "host-1"},
		},
		"gitlab ci on kubernetes": {
			env:  map[string]string{"GITLAB_CI": "true", "CI_JOB_URL": "https://gitlab.com/acme/etl/-/jobs/7", "KUBERNETES_SERVICE_HOST": "10.0.0.1"},
			want: jobPlace{Platform: "gitlab_ci", PlatformURL: "https://gitlab.com/acme/etl/-/jobs/7", Host: "host-1"},
		},
		"azure pipelines": {
			env: map[string]string{"TF_BUILD": "True", "SYSTEM_COLLECTIONURI": "https://dev.azure.com/acme/", "SYSTEM_TEAMPROJECT": "Data Team", "BUILD_BUILDID": "9"},
			want: jobPlace{Platform: "azure_pipelines",
				PlatformURL: "https://dev.azure.com/acme/Data%20Team/_build/results?buildId=9", Host: "host-1"},
		},
		"jenkins over http": {
			env:  map[string]string{"JENKINS_URL": "http://ci.internal/", "BUILD_URL": "http://ci.internal/job/etl/3/"},
			want: jobPlace{Platform: "jenkins", Host: "host-1"},
		},
		"jenkins with a user in its address": {
			env:  map[string]string{"JENKINS_URL": "https://bot:token@ci.example.com/", "BUILD_URL": "https://bot:token@ci.example.com/job/etl/3/"},
			want: jobPlace{Platform: "jenkins", PlatformURL: "https://ci.example.com/job/etl/3/", Host: "host-1"},
		},
		"kubernetes":   {env: map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}, want: jobPlace{Platform: "kubernetes", Host: "host-1"}},
		"cloud run":    {env: map[string]string{"CLOUD_RUN_JOB": "etl"}, want: jobPlace{Platform: "cloud_run", Host: "host-1"}},
		"a plain host": {env: map[string]string{}, want: jobPlace{Host: "host-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := placeOf(func(key string) string { return tc.env[key] }, func() (string, error) { return "host-1", nil })
			assert.Equal(t, tc.want, got)
		})
	}
	long := placeOf(func(string) string { return "" }, func() (string, error) { return strings.Repeat("h", 300), nil })
	assert.Len(t, long.Host, maxHostBytes)
}
