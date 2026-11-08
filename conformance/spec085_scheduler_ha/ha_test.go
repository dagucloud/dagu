// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec085_scheduler_ha_test holds black-box conformance tests for
// Spec 085: Scheduler High Availability.
package spec085_scheduler_ha_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

const (
	staleThreshold = 4 * time.Second
	retryInterval  = 250 * time.Millisecond
	// startupAllowance covers scheduler startup on a loaded CI machine.
	startupAllowance = 10 * time.Second

	// slotLeadTime is the least time a test gets to act before a slot.
	slotLeadTime = 3 * time.Second
	// slotRunsFile is where a DAG records the run ID of each of its runs.
	slotRunsFile = "slot-runs.txt"

	roleActive  = "active"
	roleStandby = "standby"
)

type scheduler struct {
	process *harness.Process
	port    int
}

type cluster struct {
	t    *testing.T
	dagu *harness.Runner
	home string
}

func newCluster(t *testing.T) *cluster {
	t.Helper()
	dagu := harness.NewRunner(t)
	return &cluster{t: t, dagu: dagu, home: dagu.ProjectPath("home")}
}

func (c *cluster) env(port int) []string {
	return []string{
		"DAGU_HOME=" + c.home,
		fmt.Sprintf("DAGU_SCHEDULER_PORT=%d", port),
		"DAGU_SCHEDULER_LOCK_STALE_THRESHOLD=" + staleThreshold.String(),
		"DAGU_SCHEDULER_LOCK_RETRY_INTERVAL=" + retryInterval.String(),
	}
}

func (c *cluster) start() *scheduler {
	c.t.Helper()
	port := harness.FreePort(c.t)
	process := c.dagu.StartWithEnv(c.env(port), "scheduler", "--dags="+c.dagu.ProjectPath("."))
	c.t.Cleanup(func() {
		if c.t.Failed() {
			process.Stop()
			c.t.Logf("scheduler output:\n%s", process.FailureOutput())
		}
	})
	return &scheduler{process: process, port: port}
}

func (c *cluster) lockOwners() []string {
	c.t.Helper()
	matches, err := filepath.Glob(filepath.Join(c.home, "data", "scheduler", "locks", ".dagu_lock", "owner.*"))
	require.NoError(c.t, err)
	return matches
}

// removeLockOwner makes the running scheduler find its lock is not its own.
func (c *cluster) removeLockOwner() {
	c.t.Helper()
	owners := c.lockOwners()
	require.NotEmpty(c.t, owners, "no lock owner file found")
	for _, owner := range owners {
		require.NoError(c.t, os.Remove(owner))
	}
}

// role returns the role a scheduler reports, or "" while it cannot be read.
func (s *scheduler) role() string {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", s.port))
	if err != nil {
		return ""
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	return body.Role
}

func (s *scheduler) waitRole(t *testing.T, want string, within time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		select {
		case <-s.process.Done():
			require.FailNowf(t, "scheduler exited", "%s", s.process.FailureOutput())
		default:
		}
		if s.role() == want {
			return
		}
	}
	require.FailNowf(t, "timed out", "scheduler did not report role %q within %s", want, within)
}

// startPair starts an active scheduler and a standby behind it.
func (c *cluster) startPair() (active, standby *scheduler) {
	c.t.Helper()
	active = c.start()
	active.waitRole(c.t, roleActive, harness.WaitTimeout(c.t))
	standby = c.start()
	standby.waitRole(c.t, roleStandby, harness.WaitTimeout(c.t))
	return active, standby
}

func TestOneActive(t *testing.T) {
	t.Parallel()

	active, standby := newCluster(t).startPair()

	// The standby must keep waiting while the active scheduler is healthy,
	// through several lock heartbeats and past the stale threshold.
	deadline := time.Now().Add(staleThreshold + 2*time.Second)
	for time.Now().Before(deadline) {
		require.Equal(t, roleActive, active.role())
		require.Equal(t, roleStandby, standby.role())
		time.Sleep(200 * time.Millisecond)
	}
}

func TestKillActive(t *testing.T) {
	t.Parallel()

	active, standby := newCluster(t).startPair()

	active.process.Kill()

	standby.waitRole(t, roleActive, staleThreshold+2*retryInterval+startupAllowance)
}

func TestStopActive(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no graceful stop for a background process; TestKillActive covers the forced stop")
	}

	active, standby := newCluster(t).startPair()

	active.process.Stop()

	// Exit code 0 shows the process shut down by itself rather than through
	// the harness's force fallback, so it released the lock on the way out.
	require.Zerof(t, active.process.ExitCode(), "%s", active.process.FailureOutput())
	standby.waitRole(t, roleActive, retryInterval+startupAllowance)
}

// Losing the lock must end the process visibly, so a supervisor restarts it.
func TestLockLostExit(t *testing.T) {
	t.Parallel()

	t.Run("scheduler", func(t *testing.T) {
		t.Parallel()
		c := newCluster(t)
		s := c.start()
		s.waitRole(t, roleActive, harness.WaitTimeout(t))

		c.removeLockOwner()

		requireFailedExit(t, s.process)
	})

	t.Run("start-all", func(t *testing.T) {
		t.Parallel()
		c := newCluster(t)
		apiPort := harness.FreePort(t)
		env := append(c.env(harness.FreePort(t)),
			fmt.Sprintf("DAGU_PORT=%d", apiPort),
			"DAGU_HOST=127.0.0.1",
			"DAGU_COORDINATOR_ENABLED=false",
		)
		process := c.dagu.StartWithEnv(env, "start-all", "--dags="+c.dagu.ProjectPath("."))

		// start-all serves no scheduler health endpoint, so ask its API. Removing
		// the owner file while the lock is still being taken would only make the
		// scheduler take it again.
		require.Eventually(t, func() bool {
			return schedulerActive(apiPort)
		}, harness.WaitTimeout(t), 50*time.Millisecond, "start-all did not become active")

		c.removeLockOwner()

		requireFailedExit(t, process)
	})
}

// schedulerActive reports whether the server's API lists an active scheduler.
// A slot that passes entirely while no scheduler is active runs once, after a
// scheduler takes over in a later minute. Within the slot's own minute the
// first tick would run it, so the replacement starts after that.
func TestMissedSlot(t *testing.T) {
	t.Parallel()
	c := newSlotCluster(t)
	active := c.start()
	active.waitRole(t, roleActive, harness.WaitTimeout(t))
	slot := nextSlot()

	sleepUntil(slot.Add(-2 * time.Second))
	active.process.Kill()
	sleepUntil(slot.Add(time.Minute + time.Second))
	replacement := c.start()
	replacement.waitRole(t, roleActive, staleThreshold+2*retryInterval+startupAllowance)

	c.requireOneSlotRun(slot)
}

// A scheduler killed as a slot comes due may or may not have started it. The
// slot still runs once.
func TestKillAtSlot(t *testing.T) {
	t.Parallel()
	c := newSlotCluster(t)
	active, standby := c.startPair()
	slot := nextSlot()

	sleepUntil(slot.Add(50 * time.Millisecond))
	active.process.Kill()
	standby.waitRole(t, roleActive, staleThreshold+2*retryInterval+startupAllowance)

	c.requireOneSlotRun(slot)
}

// newSlotCluster prepares schedulers with a DAG that records each run it makes.
func newSlotCluster(t *testing.T) *cluster {
	t.Helper()
	if testing.Short() {
		t.Skip("waits for a minute boundary")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the recording step is a POSIX command")
	}

	c := newCluster(t)
	c.dagu.WriteFile("slots.yaml", fmt.Sprintf(`schedule: "* * * * *"
steps:
  - id: record
    run: echo "$DAG_RUN_ID" >> %q
`, c.dagu.ProjectPath(slotRunsFile)))
	return c
}

// nextSlot returns the first minute boundary far enough ahead to act before it.
func nextSlot() time.Time {
	return time.Now().Add(slotLeadTime).Truncate(time.Minute).Add(time.Minute)
}

func sleepUntil(t time.Time) {
	time.Sleep(time.Until(t))
}

// waitUntil polls on the test goroutine, so that done may fail the test.
func waitUntil(t *testing.T, within time.Duration, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if done() {
			return
		}
	}
	require.FailNowf(t, "timed out", "waiting for %s", what)
}

// requireOneSlotRun waits for the slot's run, then allows time for a duplicate
// to appear before requiring that there is only one.
func (c *cluster) requireOneSlotRun(slot time.Time) {
	c.t.Helper()
	waitUntil(c.t, startupAllowance+harness.WaitTimeout(c.t), "the slot's run", func() bool {
		return c.slotRuns(slot) > 0
	})
	time.Sleep(2 * time.Second)
	require.Equal(c.t, 1, c.slotRuns(slot))
}

// slotRuns counts the recorded runs of a slot, whose time ends its run ID.
func (c *cluster) slotRuns(slot time.Time) int {
	c.t.Helper()
	data, err := os.ReadFile(c.dagu.ProjectPath(slotRunsFile)) // #nosec G304 -- path inside the test project.
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	require.NoError(c.t, err)
	suffix := "-" + slot.UTC().Format("20060102T150405")
	count := 0
	for line := range strings.Lines(string(data)) {
		if strings.HasSuffix(strings.TrimSpace(line), suffix) {
			count++
		}
	}
	return count
}

// A queued run keeps going after the scheduler that started it stops.
func TestStopKeepsQueuedRun(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the step is a POSIX command")
	}

	c := newCluster(t)
	started, release := c.dagu.ProjectPath("started"), c.dagu.ProjectPath("release")
	c.dagu.WriteFile("queued.yaml", fmt.Sprintf(`steps:
  - id: work
    run: touch %q && until [ -e %q ]; do sleep 0.05; done
`, started, release))

	active := c.start()
	active.waitRole(t, roleActive, harness.WaitTimeout(t))
	const runID = "queued-run"
	env := c.env(0)
	c.dagu.RunWithEnv(env, "enqueue", "--run-id="+runID, "queued.yaml").ExpectExitCode(0)
	waitUntil(t, harness.WaitTimeout(t), "the queued step to start", func() bool {
		_, err := os.Stat(started)
		return err == nil
	})

	active.process.Stop()
	require.NoError(t, os.WriteFile(release, nil, 0600))

	waitUntil(t, harness.WaitTimeout(t), "the run to succeed", func() bool {
		result := c.dagu.RunWithEnv(env, "status", "--run-id="+runID, "queued.yaml")
		return result.ExitCode() == 0 && strings.Contains(result.Stdout(), "Succeeded")
	})
}

func schedulerActive(apiPort int) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/services/scheduler", apiPort))
	if err != nil {
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	var body struct {
		Schedulers []struct {
			Status string `json:"status"`
		} `json:"schedulers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	for _, s := range body.Schedulers {
		if s.Status == roleActive {
			return true
		}
	}
	return false
}

func requireFailedExit(t *testing.T, process *harness.Process) {
	t.Helper()
	select {
	case <-process.Done():
		require.NotZerof(t, process.ExitCode(), "%s", process.FailureOutput())
	case <-time.After(harness.WaitTimeout(t)):
		process.Stop()
		t.Fatalf("process kept running after losing the scheduler lock\n%s", process.FailureOutput())
	}
}
