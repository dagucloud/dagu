// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The banner names the web UI URL, the setup step while no admin exists, and
// free monitoring only when this server could still connect to Dagu Console.
func TestStartupBanner(t *testing.T) {
	const url = "http://localhost:8080"
	community := &license.Status{Community: true}
	tests := []struct {
		name        string
		banner      startupBanner
		contains    []string
		notContains []string
	}{
		{
			name:   "FirstRun",
			banner: startupBanner{version: "v2.1.0", url: url, dagsDir: "/srv/dags", setupRequired: true, license: community},
			contains: []string{
				"Dagu v2.1.0 is ready",
				"Web UI:  " + url + "\n",
				"create the admin account at " + url + "/setup",
				"DAGs:    /srv/dags",
				url + "/license#monitoring",
			},
		},
		{
			name:        "Licensed",
			banner:      startupBanner{url: url, license: &license.Status{Plan: "pro", Valid: true}},
			notContains: []string{"Alerts:"},
		},
		{
			name:        "LicenseFromEnv",
			banner:      startupBanner{url: url, license: &license.Status{Community: true, Source: license.SourceEnvKey}},
			notContains: []string{"Alerts:"},
		},
		{
			name:        "NoLicenseManager",
			banner:      startupBanner{url: url},
			notContains: []string{"Alerts:"},
		},
		{
			name:        "SetupDone",
			banner:      startupBanner{url: url, license: community},
			contains:    []string{"Alerts:"},
			notContains: []string{"Setup:"},
		},
		{
			name:   "BasePath",
			banner: startupBanner{url: url + "/dagu", setupRequired: true, license: community},
			contains: []string{
				url + "/dagu/setup",
				url + "/dagu/license#monitoring",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			tt.banner.write(&out)
			for _, want := range tt.contains {
				require.Contains(t, out.String(), want)
			}
			for _, unwanted := range tt.notContains {
				require.NotContains(t, out.String(), unwanted)
			}
		})
	}
}

// The banner on a terminal and the log line elsewhere offer the same
// free monitoring page, and only to a server that could still connect.
func TestMonitoringOffer(t *testing.T) {
	const url = "http://localhost:8080/dagu"
	link, ok := monitoringOffer(url, &license.Status{Community: true})
	require.True(t, ok)
	require.Equal(t, url+"/license#monitoring", link)

	for _, status := range []*license.Status{nil, {Plan: "pro", Valid: true}, {Community: true, Source: license.SourceEnvKey}} {
		_, ok := monitoringOffer(url, status)
		require.False(t, ok)
	}
}

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

// The reporter runs whenever a license manager does: so that an
// administrator can choose a level later, and, when configuration fixes off,
// so that Dagu Console can be told once that the server no longer reports.
func TestStartCloudReport(t *testing.T) {
	newContext := func(report config.ReportLevel) *Context {
		cfg := &config.Config{Cloud: config.CloudConfig{Report: report}}
		cfg.Paths.DataDir = t.TempDir()
		return &Context{Context: t.Context(), Config: cfg, LicenseManager: license.NewTestManager()}
	}

	for _, level := range []config.ReportLevel{"", config.ReportOff, config.ReportHealth, config.ReportRuns} {
		t.Run("level "+string(level), func(t *testing.T) {
			ctx := newContext(level)
			reporter := startCloudReport(ctx, newCloudReportMonitoring(ctx), newCloudAccess(ctx), resource.NewService(ctx.Config))
			require.NotNil(t, reporter)
			reporter.Stop()
		})
	}

	t.Run("no license manager", func(t *testing.T) {
		ctx := newContext("")
		ctx.LicenseManager = nil
		monitoring := newCloudReportMonitoring(ctx)
		require.Nil(t, monitoring)
		require.Nil(t, newCloudAccess(ctx))
		require.Nil(t, startCloudReport(ctx, monitoring, nil, nil))
	})
}

// The audit shipper starts only at a level that sends, with local audit on
// and a license manager; otherwise nothing reads the audit files.
func TestStartCloudAudit(t *testing.T) {
	newContext := func(level config.AuditLevel) *Context {
		cfg := &config.Config{Cloud: config.CloudConfig{Audit: level}}
		cfg.Server.Audit.Enabled = true
		cfg.Paths.DataDir = t.TempDir()
		cfg.Paths.AdminLogsDir = t.TempDir()
		return &Context{Context: t.Context(), Config: cfg, LicenseManager: license.NewConnectedTestManager(license.FeatureAudit)}
	}
	stateFile := func(ctx *Context) string {
		return filepath.Join(ctx.Config.Paths.DataDir, "cloud", "audit-state.json")
	}

	t.Run("sends", func(t *testing.T) {
		ctx := newContext(config.AuditChanges)
		shipper := startCloudAudit(ctx, nil)
		require.NotNil(t, shipper)
		defer shipper.Stop()
		require.Eventually(t, func() bool {
			_, err := os.Stat(stateFile(ctx))
			return err == nil
		}, 5*time.Second, 10*time.Millisecond, "the shipper saves where it starts")
	})

	for name, ctx := range map[string]*Context{
		"unset": newContext(""),
		"off":   newContext(config.AuditOff),
		"audit off": func() *Context {
			ctx := newContext(config.AuditAll)
			ctx.Config.Server.Audit.Enabled = false
			return ctx
		}(),
		"no license": func() *Context { ctx := newContext(config.AuditAll); ctx.LicenseManager = nil; return ctx }(),
	} {
		t.Run(name, func(t *testing.T) {
			require.Nil(t, startCloudAudit(ctx, nil))
			_, err := os.Stat(filepath.Dir(stateFile(ctx)))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

// recordingShipper records how the command ended the audit shipper.
type recordingShipper struct {
	ended    chan string
	signaled time.Time
}

func (s *recordingShipper) Drain(signaled time.Time) {
	s.signaled = signaled
	s.ended <- "drain"
}

func (s *recordingShipper) Stop() { s.ended <- "stop" }

// A stop signal drains the audit shipper once the web server has stopped, or
// after the start delay; anything else stops it at once.
func TestWatchCloudAudit(t *testing.T) {
	signal := func(cancel context.CancelCauseFunc) { cancel(shutdownSignalError{signal: syscall.SIGTERM}) }

	t.Run("signal, then the web server stops", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		webStopped, onWebStopped := closedOnce()
		shipper := &recordingShipper{ended: make(chan string, 1)}
		wait := watchCloudAudit(ctx, webStopped, shipper, time.Hour)

		before := time.Now()
		signal(cancel)
		onWebStopped()
		wait()

		assert.Equal(t, "drain", <-shipper.ended)
		assert.WithinRange(t, shipper.signaled, before, time.Now())
	})

	t.Run("signal, and the web server takes too long", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		webStopped, _ := closedOnce()
		shipper := &recordingShipper{ended: make(chan string, 1)}
		wait := watchCloudAudit(ctx, webStopped, shipper, 10*time.Millisecond)

		signal(cancel)
		wait()

		assert.Equal(t, "drain", <-shipper.ended)
	})

	t.Run("no signal", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		shipper := &recordingShipper{ended: make(chan string, 1)}
		wait := watchCloudAudit(ctx, nil, shipper, time.Hour)

		cancel(nil)
		wait()

		assert.Equal(t, "stop", <-shipper.ended)
	})
}

// The event store and the run history are prepared at every level: an
// administrator can choose runs at any time, and a reporter below runs
// discards where runs stopped being reported.
func TestCloudReportEvents(t *testing.T) {
	runs := persis.NewDAGRunRepository(nil, nil, persis.DAGRunRepositoryOptions{})
	newContext := func(report config.ReportLevel) *Context {
		cfg := &config.Config{Cloud: config.CloudConfig{Report: report}}
		cfg.Paths.DataDir = t.TempDir()
		return &Context{
			Context:     t.Context(),
			Config:      cfg,
			event:       eventstore.New(nil),
			Persistence: Persistence{DAGRunRepository: runs},
		}
	}

	for _, level := range []config.ReportLevel{config.ReportRuns, "", config.ReportHealth, config.ReportOff} {
		events := cloudReportEvents(newContext(level))
		require.NotNil(t, events.Reader)
		require.Equal(t, runs, events.Statuses)
	}
}
