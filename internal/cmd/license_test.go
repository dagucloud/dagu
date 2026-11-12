// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintLicenseStatus(t *testing.T) {
	t.Parallel()

	t.Run("connected server", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		printLicenseStatus(&out, license.Status{
			Valid:        true,
			Plan:         "pro",
			Features:     []string{"audit", "rbac"},
			Expiry:       time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
			Workspace:    "acme",
			ServerName:   "build-01",
			ServerID:     "srv-1",
			LicenseID:    "lic-1",
			ConnectedVia: license.ConnectedViaConsole,
			LastCheckIn:  time.Date(2026, 10, 9, 12, 30, 0, 0, time.UTC),
			ConsoleURL:   "https://console.dagu.sh/servers?server=srv-1",
		})

		assert.Equal(t, `License Status
  Plan:         pro
  Features:     audit, rbac
  Expires:      2026-12-31
  Status:       Active
  Workspace:    acme
  Server:       build-01
  Server ID:    srv-1
  License ID:   lic-1
  Connected:    via Dagu Console
  Checked in:   2026-10-09 12:30 UTC
  Manage:       https://console.dagu.sh/servers?server=srv-1
`, out.String())
	})

	t.Run("community", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		printLicenseStatus(&out, license.Status{Community: true, ServerName: "build-01"})

		assert.Equal(t, "License: Community mode (no license)\n", out.String())
	})
}

// Without a key, activate waits for a browser approval, which only makes sense
// in a terminal; a script that lost its key fails instead of waiting.
func TestLicenseActivateWithoutKeyNeedsTerminal(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	err := th.RunCommandWithError(t, License(), test.CmdTest{Args: []string{"license", "activate"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a license key is required when not running in a terminal")
}

// An unknown --remote-access level fails before anything is activated.
func TestLicenseActivateUnknownRemoteAccess(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	err := th.RunCommandWithError(t, License(), test.CmdTest{
		Args: []string{"license", "activate", "DAGU-KEY", "--remote-access", "admin"},
	})
	require.ErrorContains(t, err, "--remote-access must be one of off, viewer, operator, or developer")
}

// --remote-access saves the level a server reads once licensed, unless
// cloud.remote_access fixes it; without the flag, remote access stays off.
func TestSaveRemoteAccessLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured config.RemoteAccessLevel
		level      config.RemoteAccessLevel
		want       config.RemoteAccessLevel
	}{
		{name: "Chosen", level: config.RemoteAccessOperator, want: config.RemoteAccessOperator},
		{name: "Omitted", want: config.RemoteAccessOff},
		{name: "Configured", configured: config.RemoteAccessViewer, level: config.RemoteAccessDeveloper, want: config.RemoteAccessViewer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{
				Paths: config.PathsConfig{DataDir: t.TempDir()},
				Cloud: config.CloudConfig{RemoteAccess: tt.configured},
			}
			ctx := &Context{Context: context.Background(), Config: cfg}

			require.NoError(t, saveRemoteAccessLevel(ctx, tt.level, "https://console.example.com/servers?server=s1"))

			status, err := newCloudAccessSettings(cfg).Status(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.want, status.Level)
		})
	}
}

func TestMCPURL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://console.example.com/mcp", mcpURL("https://console.example.com/servers?server=s1"))
	assert.Equal(t, "https://example.com/console/mcp", mcpURL("https://example.com/console/servers?server=s1"))
	assert.Empty(t, mcpURL(""))
}

// What a server connected from the CLI reports comes from --report, else
// from the answer to a question whose default sends error messages, else
// from cloud.report, which no choice overrides.
func TestChooseReportLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		flag       string
		answer     string
		configured config.ReportLevel
		want       config.ReportLevel
		asked      bool
		wantErr    string
	}{
		{name: "Flag", flag: "runs", want: config.ReportRuns},
		{name: "Default", answer: "\n", want: config.ReportErrors, asked: true},
		{name: "Answer", answer: "3\n", want: config.ReportHealth, asked: true},
		{name: "Nothing", answer: "4\n", want: config.ReportOff, asked: true},
		{name: "Configured", flag: "health", configured: config.ReportRuns, want: config.ReportRuns},
		{name: "UnknownFlag", flag: "everything", wantErr: "--report must be one of errors, runs, health, or off"},
		{name: "UnknownAnswer", answer: "9\n", wantErr: "choose 1, 2, 3, or 4", asked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			got, err := chooseReportLevel(strings.NewReader(tt.answer), &out, tt.flag, tt.configured)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
			assert.Equal(t, tt.asked, strings.Contains(out.String(), "What should this server report"), out.String())
		})
	}
}
