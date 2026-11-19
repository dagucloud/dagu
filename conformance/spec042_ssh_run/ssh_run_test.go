// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec042_ssh_run_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestSSHConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file      string
		command   string
		wantError string
	}{
		{"basic.yaml", "validate", ""},
		{"missing_command.yaml", "validate", "with.command is required"},
		{"credential_forms.yaml", "validate", ""},
		{"credential_bad_ref.yaml", "validate", "field 'steps.deploy.with.key.secret': invalid secret ref"},
		{"credential_interpolated_ref.yaml", "validate", "field 'steps.deploy.with.key.secret': secret ref must be literal"},
		{"credential_unknown_field.yaml", "validate", `field 'steps.deploy.with.bastion.key': unknown field "secrte"`},
		{"credential_secret_and_file.yaml", "validate", "field 'ssh.key': set either secret or file, not both"},
		{"credential_password_file.yaml", "validate", `field 'ssh.password': unknown field "file"`},
		// A ref that cannot resolve fails the run before any step, so the
		// host is never contacted.
		{"credential_missing_secret.yaml", "start", `steps.deploy.with.key from secret ref "ssh/missing-key"`},
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

// A command runs on the target through a bastion, and only the target runs
// it. Both servers run in the test process, so this needs no Docker.
func TestSSHRunThroughBastion(t *testing.T) {
	t.Parallel()

	key, public := harness.NewSSHKey(t)
	opts := harness.SSHServerOptions{User: "tester", AuthorizedKeys: []ssh.PublicKey{public}}
	target := harness.StartSSHServer(t, opts)
	bastion := harness.StartSSHServer(t, opts)

	dagu := harness.NewRunner(t)
	dagu.WriteFile("id_ed25519", string(key))
	result := dagu.Run("start", "--params", "port="+target.Port+" bastion_port="+bastion.Port, "through_bastion.yaml")
	result.ExpectExitCode(0)

	dagu.ExpectFileContains("remote.out", "spec042-through-bastion")
	require.Len(t, target.Commands(), 1)
	require.Positive(t, bastion.AuthAttempts(), "the client must log in to the bastion")
	require.Empty(t, bastion.Commands(), "the bastion only forwards the connection")
}
