// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// untrustedRequireRoot skips a test that needs the change to the restricted
// user.
func untrustedRequireRoot(t *testing.T) {
	t.Helper()
	harness.RequireRestrictedUserHost(t)
}

// Spec 092, "Isolation": with untrusted.uid and untrusted.gid, an untrusted
// step runs as the restricted user. It cannot read a file of the worker,
// cannot write .git or rename it, can write the other files, and has a private
// HOME. At the end, Dagu stops its leftover process and gives the files back
// to the worker before a trusted step runs.
func TestUntrustedIsolation(t *testing.T) {
	untrustedRequireRoot(t)

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--config", dagu.ProjectPath("untrusted_isolation_config.yaml"), "untrusted_isolation.yaml")
	out := result.Stdout() + result.Stderr()
	result.ExpectExitCode(0)
	for _, want := range []string{
		"uid=64021",
		"read:denied",
		"git:denied",
		"move:denied",
		"write:allowed",
		"home=700:64021",
		"owner=0",
		"hook:absent",
		"leftover=0",
		"result=ok",
	} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "isolation is off")
}
