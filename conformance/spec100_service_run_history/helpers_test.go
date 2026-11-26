// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec100_service_run_history_test

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// wait is the budget of a poll. A run takes 3 to 6 seconds, and the machine
// of a test run is often loaded.
const wait = 90 * time.Second

// poll is the delay between two reads of a poll.
const poll = 500 * time.Millisecond

// project is a test project: the fixtures of one folder of testdata, a home
// that the scheduler, the server and the CLI share, and a server.
type project struct {
	t    *testing.T
	dagu *harness.Runner
	home string
	dags string
	base string
	// server and schedulers are the processes that the test started, whose
	// output logState logs.
	server     *harness.Process
	schedulers []*harness.Process
}

// newProject copies the testdata, and uses the folder as the DAGs directory.
func newProject(t *testing.T, folder string) *project {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use a POSIX shell")
	}
	dagu := harness.NewRunner(t)
	p := &project{
		t:    t,
		dagu: dagu,
		home: filepath.Join(t.TempDir(), "dagu"),
		dags: dagu.ProjectPath(folder),
	}
	// The cleanups run in reverse order: this one runs after the processes of
	// the test stop, and before the removal of the temp directories.
	t.Cleanup(p.settle)
	return p
}

// settle waits until no file of the home changes for two seconds. A run that
// the scheduler started before it stopped goes on alone, and the removal of
// the temp directory must not meet its logs.
func (p *project) settle() {
	snapshot := func() string {
		var b strings.Builder
		_ = filepath.WalkDir(p.home, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if info, err := d.Info(); err == nil {
				fmt.Fprintf(&b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
			}
			return nil
		})
		return b.String()
	}
	if _, err := os.Stat(p.home); err != nil {
		return
	}
	last, quiet := snapshot(), 0
	for deadline := time.Now().Add(30 * time.Second); quiet < 4 && time.Now().Before(deadline); {
		time.Sleep(500 * time.Millisecond)
		if now := snapshot(); now == last {
			quiet++
		} else {
			last, quiet = now, 0
		}
	}
}

func (p *project) env() []string {
	return []string{"DAGU_HOME=" + p.home}
}

// startScheduler starts `dagu scheduler` on the DAGs of the project.
func (p *project) startScheduler() *harness.Process {
	p.t.Helper()
	// Each scheduler has its own health port: the tests run in parallel.
	// Debug output tells why the scheduler starts no run; logState prints it.
	env := append(p.env(), fmt.Sprintf("DAGU_SCHEDULER_PORT=%d", harness.FreePort(p.t)), "DAGU_DEBUG=true")
	proc := p.dagu.StartWithEnv(env, "scheduler", "--dags="+p.dags)
	p.schedulers = append(p.schedulers, proc)
	select {
	case <-proc.Done():
		require.FailNowf(p.t, "the scheduler exited at start", "%s", proc.FailureOutput())
	case <-time.After(200 * time.Millisecond):
	}
	return proc
}

// startServer starts `dagu server` on the DAGs of the project and waits for
// its health route.
func (p *project) startServer() {
	p.t.Helper()
	port := harness.FreePort(p.t)
	proc := p.dagu.StartWithEnv(p.env(),
		"server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port), "--dags="+p.dags)
	p.server = proc
	p.base = fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)
	// A server that accepts the connection and does not answer must not hold
	// the poll past its deadline.
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(harness.WaitTimeout(p.t))
	for time.Now().Before(deadline) {
		select {
		case <-proc.Done():
			require.FailNowf(p.t, "the server exited at start", "%s", proc.FailureOutput())
		default:
		}
		req, err := http.NewRequestWithContext(p.t.Context(), http.MethodGet, p.base+"/health", nil)
		require.NoError(p.t, err)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.FailNow(p.t, "the server did not answer", "%s", proc.Output())
}

// get sends a GET request and returns the status code and the body.
func (p *project) get(path string, query url.Values) (int, []byte) {
	p.t.Helper()
	target := p.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(p.t.Context(), http.MethodGet, target, nil)
	require.NoError(p.t, err)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	require.NoError(p.t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(p.t, err)
	return resp.StatusCode, body
}

// getJSON sends a GET request, expects the status code and decodes the body.
func getJSON[T any](p *project, status int, path string, query url.Values) T {
	p.t.Helper()
	code, body := p.get(path, query)
	require.Equal(p.t, status, code, "GET %s %s: %s", path, query.Encode(), body)
	var v T
	require.NoError(p.t, json.Unmarshal(body, &v), "%s", body)
	return v
}

func (p *project) samples(dag string, query url.Values) api.SampleSeries {
	p.t.Helper()
	return getJSON[api.SampleSeries](p, http.StatusOK, "/dags/"+dag+"/run-history/samples", query)
}

func (p *project) events(dag string, query url.Values) api.RunHistoryEvents {
	p.t.Helper()
	return getJSON[api.RunHistoryEvents](p, http.StatusOK, "/dags/"+dag+"/run-history/events", query)
}

func (p *project) availability(dag string) api.SampleAvailability {
	p.t.Helper()
	return getJSON[api.SampleAvailability](p, http.StatusOK, "/dags/"+dag+"/run-history/availability", nil)
}

// logState logs the output of the server and of the schedulers, then the runs
// and the samples of a DAG, so that a poll that times out shows why. The
// output comes first: the reads fail the test when the server is gone.
func (p *project) logState(dag string) {
	p.t.Helper()
	if p.server != nil {
		p.t.Logf("server:\n%s", processState(p.server))
	}
	for i, scheduler := range p.schedulers {
		p.t.Logf("scheduler %d:\n%s", i+1, processState(scheduler))
	}
	_, body := p.get("/dag-runs", url.Values{"name": {dag}, "limit": {"100"}})
	p.t.Logf("runs of %s: %s", dag, body)
	_, body = p.get("/dags/"+dag+"/run-history/samples", url.Values{"points": {"2000"}})
	p.t.Logf("samples of %s: %s", dag, body)
}

// processState tells whether a process exited, and gives its output.
func processState(proc *harness.Process) string {
	select {
	case <-proc.Done():
		return proc.FailureOutput()
	default:
		return "still running\n" + proc.Output()
	}
}

// runs returns the retained runs of a DAG.
func (p *project) runs(dag string) []api.DAGRunSummary {
	p.t.Helper()
	page := getJSON[api.DAGRunsPageResponse](p, http.StatusOK, "/dag-runs", url.Values{"name": {dag}, "limit": {"100"}})
	return page.DagRuns
}

// ended returns the retained runs of a DAG that have ended.
func (p *project) ended(dag string) []api.DAGRunSummary {
	p.t.Helper()
	return endedOf(p.runs(dag))
}

// betweenRuns reports whether every retained run of a DAG has ended and the
// newest one succeeded. It reads the runs once, so a run that starts during the
// check is not missed.
func (p *project) betweenRuns(dag string) bool {
	p.t.Helper()
	runs := p.runs(dag)
	return len(runs) > 0 && len(endedOf(runs)) == len(runs) && runs[0].Status == api.StatusSuccess
}

// endedOf returns the runs that have ended.
func endedOf(runs []api.DAGRunSummary) []api.DAGRunSummary {
	var ended []api.DAGRunSummary
	for _, r := range runs {
		switch r.Status { //nolint:exhaustive // the other statuses are not final
		case api.StatusSuccess, api.StatusFailed, api.StatusAborted, api.StatusPartialSuccess, api.StatusRejected:
			ended = append(ended, r)
		}
	}
	return ended
}

// runIDs returns the IDs of the runs.
func runIDs(runs []api.DAGRunSummary) []string {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.DagRunId)
	}
	return ids
}

// countOf returns the sum of the counts of the buckets of the last 24 hours.
func (p *project) countOf(dag string) int64 {
	p.t.Helper()
	var n int64
	for _, b := range p.samples(dag, url.Values{"points": {"2000"}}).Buckets {
		n += b.Count
	}
	return n
}

// eventsOfKind returns the events of a kind, newest first.
func eventsOfKind(events []api.RunHistoryEvent, kind api.RunHistoryEventKind) []api.RunHistoryEvent {
	return slices.DeleteFunc(slices.Clone(events), func(e api.RunHistoryEvent) bool { return e.Kind != kind })
}
