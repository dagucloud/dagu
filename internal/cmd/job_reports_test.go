// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
)

const testRunKey = "drk_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// jobContext is a command's context with cloud, logging to logs.
func jobContext(t *testing.T, cloud config.CloudConfig, logs *bytes.Buffer) *Context {
	t.Helper()
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"), logger.WithWriter(logs), logger.WithQuiet()))
	return &Context{Context: ctx, Config: &config.Config{Cloud: cloud}}
}

// localRun is a top-level run this machine runs itself.
var localRun = jobRun{workerID: "local"}

// A top-level run on this machine reports with its run key at runs unless
// the config says otherwise.
func TestJobReportsForALocalRun(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	ctx := jobContext(t, config.CloudConfig{RunKey: testRunKey}, &logs)

	reports := jobReports(ctx, jobRun{workerID: "local", keepTrigger: true})

	require.NotNil(t, reports)
	assert.Equal(t, testRunKey, reports.RunKey)
	assert.Equal(t, config.ReportRuns, reports.Level)
	assert.True(t, reports.KeepTrigger)

	ctx.Config.Cloud.Report = config.ReportErrors
	assert.Equal(t, config.ReportErrors, jobReports(ctx, localRun).Level)
}

// A run reports no job run without a key, at a level that sends no runs, or
// as a sub-DAG or a worker's run.
func TestJobReportsSkipRunsThatDoNotReport(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		cloud config.CloudConfig
		run   jobRun
	}{
		"no key":       {run: localRun},
		"health only":  {cloud: config.CloudConfig{RunKey: testRunKey, Report: config.ReportHealth}, run: localRun},
		"reports off":  {cloud: config.CloudConfig{RunKey: testRunKey, Report: config.ReportOff}, run: localRun},
		"sub-DAG run":  {cloud: config.CloudConfig{RunKey: testRunKey}, run: jobRun{parent: ir.NewDAGRunRef("etl", "run-1"), workerID: "local"}},
		"worker's run": {cloud: config.CloudConfig{RunKey: testRunKey}, run: jobRun{workerID: "worker-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			ctx := jobContext(t, tc.cloud, &logs)

			assert.Nil(t, jobReports(ctx, tc.run))
			assert.NotContains(t, logs.String(), "WARN")
		})
	}
}

// A run key reports whatever license this machine holds: the key is how a
// machine without a server reports, and an activation can outlive its server.
func TestJobReportsDespiteALicense(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	ctx := jobContext(t, config.CloudConfig{RunKey: testRunKey}, &logs)
	ctx.LicenseManager = license.NewConnectedTestManager()

	assert.NotNil(t, jobReports(ctx, localRun))
}

func TestJobReportsSkipServerOwnedRun(t *testing.T) {
	t.Setenv(runenv.EnvKeyServerRunReports, "1")
	var logs bytes.Buffer
	ctx := jobContext(t, config.CloudConfig{RunKey: testRunKey}, &logs)

	assert.Nil(t, jobReports(ctx, localRun))
	baseEnv := config.LoadBaseEnvWithExtras([]string{runenv.EnvKeyServerRunReports}, nil)
	assert.NotContains(t, baseEnv.AsSlice(), runenv.EnvKeyServerRunReports+"=1")
}

// A key that is not a run key warns, naming the setting and not the value.
func TestJobReportsWarnOfAMalformedKey(t *testing.T) {
	t.Parallel()
	for name, key := range map[string]string{
		"another kind of key": "dsk_secret-value",
		"two keys pasted":     testRunKey + "\n" + testRunKey,
		"cut short":           testRunKey[:20],
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			ctx := jobContext(t, config.CloudConfig{RunKey: key}, &logs)

			assert.Nil(t, jobReports(ctx, localRun))
			assert.Contains(t, logs.String(), warnRunKeyMalformed)
			assert.NotContains(t, logs.String(), "secret-value")
			assert.NotContains(t, logs.String(), "0123456789abcdef")
		})
	}
}

// Only a trigger given on the command line counts as passed.
func TestTriggerPassed(t *testing.T) {
	t.Parallel()
	command := &cobra.Command{}
	command.Flags().String(triggerTypeFlag.name, "manual", "")
	ctx := &Context{Context: t.Context(), Command: command}
	assert.False(t, triggerPassed(ctx))

	require.NoError(t, command.Flags().Set(triggerTypeFlag.name, "scheduler"))
	assert.True(t, triggerPassed(ctx))
}
