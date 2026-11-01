// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"context"
	"fmt"
	"os/exec"
)

// wpadURL is where auto-discovery looks for the network's proxy auto-config.
const wpadURL = "http://wpad/wpad.dat"

// runScutil prints the macOS proxy settings. Tests replace it.
var runScutil = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
}

// detectSystem follows the macOS proxy settings, which Chrome on macOS
// follows as well.
func detectSystem(ctx context.Context) (string, []string) {
	out, err := runScutil(ctx)
	if err != nil {
		return directPAC, []string{fmt.Sprintf("could not read the macOS proxy settings: %v; requests go DIRECT", err)}
	}
	settings, err := parseScutil(out)
	if err != nil {
		return directPAC, []string{fmt.Sprintf("could not read the macOS proxy settings: %v; requests go DIRECT", err)}
	}
	cfg, warnings := macSystemConfig(settings)
	pac, more := cfg.pac(ctx, discoverWPAD)
	return pac, append(warnings, more...)
}

func discoverWPAD(context.Context) (string, error) {
	return wpadURL, nil
}
