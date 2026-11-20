// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// RequireRestrictedUserHost skips a test that changes to the restricted user of
// Spec 092: an untrusted step with isolation, or an agent worker. Such a test
// needs Linux, the user ID 0 and the flag no-new-privileges, as an agent worker
// checks at start (Spec 092, "Start"). Run it in a root container with the flag
// no-new-privileges.
func RequireRestrictedUserHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the restricted user needs Linux")
	}
	if os.Geteuid() != 0 {
		t.Skip("the restricted user needs the user ID 0; run the test in a root container with no-new-privileges")
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatalf("read /proc/self/status: %v", err)
	}
	if !strings.Contains(string(status), "NoNewPrivs:\t1") {
		t.Skip("the restricted user needs the flag no-new-privileges; run the test in a root container with no-new-privileges")
	}
}
