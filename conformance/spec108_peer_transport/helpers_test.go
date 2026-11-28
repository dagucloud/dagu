// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// waitForOutput waits until process has written every part of want and
// returns its output. It fails the test when the process exits first.
func waitForOutput(t *testing.T, process *harness.Process, want ...string) string {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		if output := process.Output(); containsAll(output, want) {
			return output
		}
		select {
		case <-process.Done():
			// The process may have written the output just before it
			// exited.
			if output := process.Output(); containsAll(output, want) {
				return output
			}
			t.Fatalf("process exited before writing %q: %s", want, process.FailureOutput())
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("process did not write %q: %s", want, process.Output())
	return ""
}

func containsAll(output string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(output, part) {
			return false
		}
	}
	return true
}
