// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
)

func TestSchedulerCommand(t *testing.T) {
	t.Run("StartScheduler", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Scheduler started")

		th.RunCommand(t, cmd.Scheduler(), test.CmdTest{
			Args:        []string{"scheduler"},
			ExpectedOut: []string{"Scheduler started", "Scheduler stopped"},
		})
	})
	t.Run("StartSchedulerWithConfig", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Scheduler started", "dagu_test")

		th.RunCommand(t, cmd.Scheduler(), test.CmdTest{
			Args:        []string{"scheduler", "--config", test.TestdataPath(t, "cli/config_test.yaml")},
			ExpectedOut: []string{"dagu_test"},
		})
	})
}
