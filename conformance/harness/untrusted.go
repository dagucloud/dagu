// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// isolationOffConfig is the configuration of a process that has no restricted
// user. Isolation fails closed (Spec 092, "Without isolation"): without
// untrusted.uid and untrusted.gid, an untrusted step needs this key.
const isolationOffConfig = "untrusted:\n  isolation: off\n"

// WriteIsolationOff makes the data directory home run an untrusted step without
// isolation. It writes the file config.yaml of home, which a process with
// DAGU_HOME=home and no --config reads. A test that runs without a restricted
// user calls it. A test of the isolation does not: it sets untrusted.uid and
// untrusted.gid, and runs as root (see RequireRestrictedUserHost).
func WriteIsolationOff(t *testing.T, home string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(home, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(isolationOffConfig), 0o600))
}

// IsolationOffEnv makes a new data directory that runs an untrusted step without
// isolation, and returns the entry DAGU_HOME=<directory> for the environment of the
// command. It is for a test that has no data directory of its own.
func IsolationOffEnv(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "dagu")
	WriteIsolationOff(t, home)
	return "DAGU_HOME=" + home
}
