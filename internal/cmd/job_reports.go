// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"github.com/dagucloud/dagu/v2/internal/cloudreport"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
)

// warnRunKeyMalformed says a configured run key is not one Dagu Console
// makes; it names the setting, never the value.
const warnRunKeyMalformed = "cloud.run_key (DAGU_CLOUD_RUN_KEY) is not a run key from Dagu Console, which starts with drk_; this run does not report to Dagu Console"

// jobRun is what decides whether a run reports itself with a run key.
type jobRun struct {
	// parent is the run's parent, zero for a top-level run, and workerID
	// "local" unless a distributed worker runs it.
	parent   ir.DAGRunRef
	workerID string
	// keepTrigger sends the run's trigger: one passed with --trigger-type,
	// or that of a retry or restart.
	keepTrigger bool
}

// withJobReports returns store, reporting the run it records to Dagu
// Console as a job run when the run should, and store itself otherwise.
func withJobReports(ctx *Context, store runstate.Store, run jobRun) runstate.Store {
	if reports := jobReports(ctx, run); reports != nil {
		return reports.Wrap(store)
	}
	return store
}

// jobReports returns how the run reports itself to Dagu Console, or nil when
// it does not: without a run key, at a level that sends no runs, for a
// sub-DAG run or one a worker runs, and on a machine with an online
// license, whose server reports its runs.
func jobReports(ctx *Context, run jobRun) *cloudreport.JobReports {
	key := ctx.Config.Cloud.RunKey
	if key == "" {
		return nil
	}
	if !cloudreport.ValidRunKey(key) {
		logger.Warn(ctx, warnRunKeyMalformed)
		return nil
	}
	level := ctx.Config.Cloud.Report
	if level == "" {
		level = config.ReportErrors
	}
	if !level.ReportsRuns() || !run.parent.Zero() || run.workerID != "local" {
		return nil
	}
	if licensedHere(ctx) {
		logger.Debug(ctx, "This machine has an online license, so its server reports this run, not the run key")
		return nil
	}
	return &cloudreport.JobReports{
		RunKey:      key,
		CloudURL:    ctx.Config.License.CloudURL,
		Level:       level,
		KeepTrigger: run.keepTrigger,
		Version:     config.Version,
	}
}

// licensedHere reports whether the data directory holds an online license
// whose activation verifies.
func licensedHere(ctx *Context) bool {
	if ctx.LicenseManager != nil {
		_, ok := ctx.LicenseManager.CloudCredentials()
		return ok
	}
	if ctx.backend == nil {
		return false
	}
	pubKey, err := license.PublicKey()
	if err != nil {
		return false
	}
	activations := file.NewLicenseStore(ctx, ctx.backend.Collection(persis.CollectionLicense))
	_, ok := license.StoredCloudCredentials(activations, pubKey, ctx.Config.License.CloudURL)()
	return ok
}

// triggerPassed reports whether --trigger-type was given on the command
// line, rather than left at its default.
func triggerPassed(ctx *Context) bool {
	if ctx.Command == nil {
		return false
	}
	flag := ctx.Command.Flags().Lookup(triggerTypeFlag.name)
	return flag != nil && flag.Changed
}
