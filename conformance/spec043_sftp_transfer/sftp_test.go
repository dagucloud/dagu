// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec043_sftp_transfer_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// Invalid transfer configuration fails without contacting a server.
func TestSFTPConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file      string
		command   string
		wantError string
	}{
		{"upload.yaml", "validate", ""},
		{"download.yaml", "validate", ""},
		{"direction_mismatch.yaml", "validate", "direction must be"},
		{"missing_source_no_server.yaml", "start", "source path is required"},
		{"missing_destination_no_server.yaml", "start", "destination path is required"},
		{"credential_forms.yaml", "validate", ""},
		{"credential_bad_ref.yaml", "validate", "field 'steps.copy.with.key.secret': invalid secret ref"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run(tc.command, tc.file)
			if tc.wantError == "" {
				result.ExpectExitCode(0)
			} else {
				result.ExpectNonZeroExitCode()
				result.ExpectStderrContains(tc.wantError)
			}
		})
	}
}
