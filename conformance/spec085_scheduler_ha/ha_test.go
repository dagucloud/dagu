// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec085_scheduler_ha_test holds black-box conformance tests for
// Spec 085: Scheduler High Availability.
package spec085_scheduler_ha_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

const (
	staleThreshold = 10 * time.Second
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

func (c *cluster) start() *scheduler {
	c.t.Helper()
	port := harness.FreePort(c.t)
	process := c.dagu.StartWithEnv([]string{
		"DAGU_HOME=" + c.home,
		fmt.Sprintf("DAGU_SCHEDULER_PORT=%d", port),
		"DAGU_SCHEDULER_LOCK_STALE_THRESHOLD=" + staleThreshold.String(),
		"DAGU_SCHEDULER_LOCK_RETRY_INTERVAL=" + retryInterval.String(),
	}, "scheduler", "--dags="+c.dagu.ProjectPath("."))
	return &scheduler{process: process, port: port}
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
	require.Eventuallyf(t, func() bool {
		select {
		case <-s.process.Done():
			require.FailNowf(t, "scheduler exited", "%s", s.process.FailureOutput())
		default:
		}
		return s.role() == want
	}, within, 50*time.Millisecond, "scheduler did not report role %q", want)
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

	active, standby := newCluster(t).startPair()

	active.process.Stop()

	// A clean stop releases the lock, so the standby does not wait for staleness.
	standby.waitRole(t, roleActive, staleThreshold/2)
}
