// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	warmUpPowerShell()
	os.Exit(m.Run())
}

// warmUpTimeout bounds the warm-up: a shell that does not come up in this
// time is left alone, so a hung launch costs the run one wait rather than
// the whole test binary.
const warmUpTimeout = 30 * time.Second

// warmUpPowerShell runs a no-op through the shell dagu selects on Windows,
// pwsh when present and powershell otherwise, before any test does. The
// first PowerShell launch on a machine pays .NET start-up and module
// import, which on a loaded CI runner has exceeded a test's 20 second
// budget; paying it here keeps that cost out of every timed run. A launch
// that fails or times out is reported, since the tests then run without
// the warm-up and a slow first launch would be the likely cause of a
// timeout.
func warmUpPowerShell() {
	if runtime.GOOS != "windows" {
		return
	}
	for _, shell := range []string{"pwsh", "powershell"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), warmUpTimeout)
		defer cancel()
		started := time.Now()
		if err := exec.CommandContext(ctx, path, "-NoProfile", "-NonInteractive", "-Command", "exit").Run(); err != nil {
			fmt.Fprintf(os.Stderr, "engine tests: warming up %s failed after %s: %v\n", shell, time.Since(started).Round(time.Millisecond), err)
		}
		return
	}
}
