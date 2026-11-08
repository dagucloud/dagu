// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec085_scheduler_ha_test holds black-box conformance tests for
// Spec 085: Scheduler High Availability.
package spec085_scheduler_ha_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
