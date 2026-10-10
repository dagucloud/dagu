// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/stretchr/testify/require"
)

func TestStopLocalAgentSessionCleanupHonorsShutdownContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- stopLocalAgentSessionCleanup(ctx, func() {}, done, nil)
	}()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(100 * time.Millisecond):
		close(done)
		<-result
		t.Fatal("agent session cleanup did not honor the shutdown context")
	}
}

func TestStartCloudReport(t *testing.T) {
	newContext := func(report config.ReportLevel) *Context {
		return &Context{
			Context:        t.Context(),
			Config:         &config.Config{Cloud: config.CloudConfig{Report: report}},
			LicenseManager: license.NewTestManager(),
		}
	}

	t.Run(string(config.ReportHealth), func(t *testing.T) {
		reporter := startCloudReport(newContext(config.ReportHealth))
		require.NotNil(t, reporter)
		reporter.Stop()
	})

	for _, level := range []config.ReportLevel{"", config.ReportOff} {
		t.Run("not "+string(level), func(t *testing.T) {
			require.Nil(t, startCloudReport(newContext(level)))
		})
	}
}

// Reports carry DAG-run events only when the admin chose runs.
func TestCloudReportEventsOnlyAtRuns(t *testing.T) {
	newContext := func(report config.ReportLevel) *Context {
		cfg := &config.Config{Cloud: config.CloudConfig{Report: report}}
		cfg.Paths.DataDir = t.TempDir()
		return &Context{Context: t.Context(), Config: cfg, event: eventstore.New(nil)}
	}

	require.NotNil(t, cloudReportEvents(newContext(config.ReportRuns)).Reader)
	require.Nil(t, cloudReportEvents(newContext(config.ReportHealth)).Reader)
}
