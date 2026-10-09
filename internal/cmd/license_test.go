// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"testing"
	"time"

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
