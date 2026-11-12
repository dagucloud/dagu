// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sysproxy describes how this device routes web requests through
// proxies, as a proxy auto-config (PAC) script.
package sysproxy

import (
	"context"
	"fmt"
	"strings"
)

// Detect returns a PAC script that routes requests the way this device does:
// through explicit when it is set, otherwise by the operating system's proxy
// settings. Warnings say what could not be followed; the script then falls
// back to DIRECT for that part. The error reports an invalid explicit proxy.
func Detect(ctx context.Context, explicit string) (pac string, warnings []string, err error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		pac, err := explicitPAC(explicit)
		return pac, nil, err
	}
	pac, warnings = detectSystem(ctx)
	return pac, warnings, nil
}

// systemConfig is an operating system's proxy configuration. A configured
// script takes precedence over auto-discovery, which takes precedence over
// the static rules.
type systemConfig struct {
	autoConfigURL string
	autoDetect    bool
	static        staticConfig
}

// discoverFunc finds the URL of the proxy auto-config the network offers.
type discoverFunc func(ctx context.Context) (string, error)

// pac returns the script for the configuration, finding an auto-discovered
// script with discover.
func (c systemConfig) pac(ctx context.Context, discover discoverFunc) (string, []string) {
	if c.autoConfigURL != "" {
		script, err := fetchPAC(ctx, c.autoConfigURL)
		if err != nil {
			return directPAC, []string{fmt.Sprintf("could not read the proxy auto-config %s: %v; requests go DIRECT", c.autoConfigURL, err)}
		}
		return script, nil
	}
	var warnings []string
	if c.autoDetect {
		script, err := discoverPAC(ctx, discover)
		if err == nil {
			return script, nil
		}
		warnings = append(warnings, fmt.Sprintf("proxy auto-discovery failed: %v; the manual proxy settings apply", err))
	}
	return c.static.pac(), warnings
}

func discoverPAC(ctx context.Context, discover discoverFunc) (string, error) {
	scriptURL, err := discover(ctx)
	if err != nil {
		return "", err
	}
	script, err := fetchPAC(ctx, scriptURL)
	if err != nil {
		return "", fmt.Errorf("could not read %s: %w", scriptURL, err)
	}
	return script, nil
}
