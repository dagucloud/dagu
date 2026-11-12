// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec039_wait holds black-box conformance tests for
// Spec 039: Wait Actions (wait.duration, wait.until, wait.file, wait.http).
package spec039_wait_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestWaitDurationBlocks proves wait.duration actually
// blocks for approximately the configured duration rather than returning
// immediately.
func TestWaitDurationBlocks(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	start := time.Now()
	result := dagu.Run("start", "duration_basic.yaml")
	elapsed := time.Since(start)
	result.ExpectExitCode(0)
	require.GreaterOrEqualf(t, elapsed, 300*time.Millisecond,
		"expected at least the configured 300ms to elapse, took %s", elapsed)
}

// TestWaitDurationReference proves a with.duration holding a reference passes
// build-time validation, resolves at run time, and the step runs.
func TestWaitDurationReference(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.WriteFile("duration_reference.yaml", `params:
  - name: DELAY
    default: 10ms
steps:
  - action: wait.duration
    with:
      duration: ${params.DELAY}
`)

	result := dagu.Run("start", "duration_reference.yaml")
	result.ExpectExitCode(0)
}

func TestWaitDurationMissing(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "duration_missing.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("duration is required")
}

func TestWaitDurationInvalid(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "duration_invalid.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("duration must be a duration")
}

func TestWaitDurationZero(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "duration_zero.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("duration must be greater than 0")
}

// TestWaitUntilPast proves that a with.until
// timestamp already in the past succeeds right away rather than being
// treated as an error or hanging.
func TestWaitUntilPast(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "until_past.yaml")
	result.ExpectExitCode(0)
}

// TestWaitUntilFuture proves a with.until
// timestamp far in the future genuinely blocks the step (rather than, say,
// silently succeeding) by pairing it with a step timeout_sec and confirming
// the step is cancelled instead of completing.
func TestWaitUntilFuture(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "until_future_timeout.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("step timed out")
}

// TestWaitUntilClockPast proves a with.until
// clock time that already passed on the logical day succeeds right away
// under the default if_past instead of rolling over to the next day.
// 00:00 minus 1s is always past on the day the run starts.
func TestWaitUntilClockPast(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "until_clock_past.yaml")
	result.ExpectExitCode(0)
}

// TestWaitUntilClockPastFail proves if_past: fail
// rejects a clock time that already passed on the logical day.
func TestWaitUntilClockPastFail(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "until_clock_past_fail.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("in the past")
}

func TestWaitUntilMissing(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "until_missing.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("until is required")
}

func TestWaitUntilBadFormat(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "until_bad_format.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("until must be RFC3339 timestamp or HH:MM")
}

func TestWaitUntilPastFail(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "until_past_fail.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("in the past")
}

func TestWaitUntilBadClock(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "until_bad_clock.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("until must be RFC3339 timestamp or HH:MM")
}

func TestWaitOffsetOnlyValidOnUntil(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "offset_wrong_op.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("only valid for until")
}

// TestWaitFileExists proves wait.file (default state:
// exists) genuinely polls: the step is still running well after it starts
// (the file does not exist yet), and only completes once the test creates
// the file.
func TestWaitFileExists(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
	const runID = "wait-file-exists"

	proc := dagu.StartWithEnv(env, "start", "--run-id="+runID, "file_wait_exists.yaml")
	defer proc.Stop()

	select {
	case <-proc.Done():
		t.Fatalf("wait.file returned before the file existed: %s", proc.FailureOutput())
	case <-time.After(300 * time.Millisecond):
	}

	dagu.WriteFile("ready.txt", "")

	select {
	case <-proc.Done():
	case <-time.After(harness.WaitTimeout(t)):
		t.Fatal("wait.file did not complete after the file appeared")
	}

	status := dagu.RunWithEnv(env, "status", "--run-id="+runID, "file_wait_exists.yaml")
	status.ExpectExitCode(0)
	require.Containsf(t, status.Stdout(), "Result: Succeeded",
		"expected the run to succeed, got:\n%s", status.Stdout())
}

// TestWaitFileGone proves the state: missing
// variant: the step blocks while the file still exists and only completes
// once the test removes it.
func TestWaitFileGone(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.WriteFile("present.txt", "x")
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
	const runID = "wait-file-gone"

	proc := dagu.StartWithEnv(env, "start", "--run-id="+runID, "file_wait_missing.yaml")
	defer proc.Stop()

	select {
	case <-proc.Done():
		t.Fatalf("wait.file returned before the file was removed: %s", proc.FailureOutput())
	case <-time.After(300 * time.Millisecond):
	}

	require.NoError(t, os.Remove(dagu.ProjectPath("present.txt")))

	select {
	case <-proc.Done():
	case <-time.After(harness.WaitTimeout(t)):
		t.Fatal("wait.file did not complete after the file was removed")
	}

	status := dagu.RunWithEnv(env, "status", "--run-id="+runID, "file_wait_missing.yaml")
	status.ExpectExitCode(0)
	require.Containsf(t, status.Stdout(), "Result: Succeeded",
		"expected the run to succeed, got:\n%s", status.Stdout())
}

func TestWaitFileNoPath(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "file_missing_path.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("path is required")
}

func TestWaitFileBadState(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "file_bad_state.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("state")
}

// TestWaitHTTPPolls proves wait.http genuinely polls: the
// test server returns a non-matching status for the first two requests and
// only starts returning the expected 200 afterward, so the step succeeding
// proves it retried rather than checking once.
func TestWaitHTTPPolls(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	port := serveHTTP(t, func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	dagu := harness.NewRunner(t)
	dagu.WriteFile("http_wait_ready.yaml", fmt.Sprintf(`steps:
  - action: wait.http
    with:
      url: http://127.0.0.1:%d/
      poll_interval: 100ms
`, port))

	result := dagu.Run("start", "http_wait_ready.yaml")
	result.ExpectExitCode(0)
	require.GreaterOrEqualf(t, requests.Load(), int32(3),
		"expected at least 3 requests (2 failing + 1 succeeding), got %d", requests.Load())
}

// TestWaitHTTPReferenceURL proves a with.url that embeds an env reference
// passes build-time validation and polls the URL it resolves to. The server
// only answers on /ready, so the step succeeds only if the reference resolved.
func TestWaitHTTPReferenceURL(t *testing.T) {
	t.Parallel()

	port := serveHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	dagu := harness.NewRunner(t)
	dagu.WriteFile("http_reference_url.yaml", fmt.Sprintf(`env:
  BASE_URL: http://127.0.0.1:%d
steps:
  - action: wait.http
    timeout_sec: 10
    with:
      url: ${BASE_URL}/ready
      poll_interval: 100ms
`, port))

	result := dagu.Run("start", "http_reference_url.yaml")
	result.ExpectExitCode(0)
}

// TestWaitHTTPReferenceStatus proves a with.status reference resolving to a
// numeric string at run time is parsed and compared against the response.
func TestWaitHTTPReferenceStatus(t *testing.T) {
	t.Parallel()

	port := serveHTTP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	dagu := harness.NewRunner(t)
	dagu.WriteFile("http_reference_status.yaml", fmt.Sprintf(`params:
  - name: CODE
    default: "204"
steps:
  - action: wait.http
    timeout_sec: 10
    with:
      url: http://127.0.0.1:%d/
      status: ${params.CODE}
      poll_interval: 100ms
`, port))

	result := dagu.Run("start", "http_reference_status.yaml")
	result.ExpectExitCode(0)
}

func TestWaitHTTPNoURL(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "http_missing_url.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("url is required")
}

func TestWaitHTTPBadURL(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "http_bad_url.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("must be an absolute HTTP URL")
}

func TestWaitHTTPBadStatus(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "http_bad_status.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("status")
}

// serveHTTP serves handler on an ephemeral loopback port for the life of the
// test and returns the port.
func serveHTTP(t *testing.T, handler http.HandlerFunc) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return listener.Addr().(*net.TCPAddr).Port
}
