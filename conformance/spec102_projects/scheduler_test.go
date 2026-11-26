// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestSchedulerServesEveryProject proves Spec 102 "Scheduling" and
// "Queues": one scheduler process runs the queued runs of every project,
// each in its own project, and a project created while it runs is served
// without a restart.
func TestSchedulerServesEveryProject(t *testing.T) {
	t.Parallel()
	in := newProjectsInstall(t)
	env := append(slices.Clone(in.env), fmt.Sprintf("DAGU_SCHEDULER_PORT=%d", harness.FreePort(t)))
	scheduler := in.dagu.StartWithEnv(env, "scheduler")
	t.Cleanup(func() {
		if t.Failed() {
			scheduler.Stop()
			t.Logf("scheduler output:\n%s", scheduler.FailureOutput())
		}
	})

	in.run(t, "enqueue", "--project", "intraday", "forecast").ExpectExitCode(0)
	require.Equal(t, "intraday", in.waitProjectOf(t, "forecast"))

	in.run(t, "project", "create", "weather").ExpectExitCode(0)
	in.writeDAG(t, "projects/weather/nowcast.yaml")
	in.run(t, "enqueue", "--project", "weather", "nowcast").ExpectExitCode(0)
	require.Equal(t, "weather", in.waitProjectOf(t, "nowcast"))
}

// waitProjectOf waits until the DAG name has run and returns the
// DAGU_PROJECT its run got.
func (in projectsInstall) waitProjectOf(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(in.out, name)
	require.Eventually(t, func() bool {
		info, err := os.Stat(path)
		return err == nil && info.Size() > 0
	}, harness.WaitTimeout(t), 100*time.Millisecond, "%s did not run", name)
	return in.projectOf(t, name)
}
