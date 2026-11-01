// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !windows

package sysproxy

import (
	"context"
	"os"
)

// getenv reads the environment. Tests replace it.
var getenv = os.Getenv

// detectSystem follows the proxy environment variables.
func detectSystem(context.Context) (string, []string) {
	env := readEnvProxyConfig(getenv)
	cfg, warnings := env.staticConfig()
	if !env.hasProxy() && getenv("XDG_CURRENT_DESKTOP") != "" {
		warnings = append(warnings, "the desktop's proxy settings are not read; "+
			"set HTTPS_PROXY, HTTP_PROXY and NO_PROXY, or the step's browser.proxy, for requests to use a proxy")
	}
	return cfg.pac(), warnings
}
