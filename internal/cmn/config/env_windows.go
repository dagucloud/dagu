// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package config

import "strings"

// init populates the package's defaultWhitelist with common Windows environment
// variable names so they are treated as whitelisted on Windows builds.
// normalizeEnvKey converts to uppercase on Windows for case-insensitive matching.
// Windows environment variables are case-insensitive, but Go maps are not.
func normalizeEnvKey(key string) string {
	return strings.ToUpper(key)
}

func init() {
	// Windows-specific environment variables (all uppercase for case-insensitive matching)
	for _, key := range []string{
		"USERPROFILE",  // Native Home
		"SYSTEMROOT",   // C:\Windows
		"WINDIR",       // Same as SystemRoot
		"SYSTEMDRIVE",  // C:
		"COMSPEC",      // cmd.exe
		"PATHEXT",      // .COM;.EXE;.BAT
		"TEMP",         // Temp dir
		"TMP",          // Temp dir
		"PATH",         // System path
		"PSMODULEPATH", // PowerShell specific
		"HOME",         // Used by Go, Git, and ported tools

		// User profile and per-user data directories used by Git, PowerShell,
		// .NET tools, and credential helpers.
		"APPDATA",
		"LOCALAPPDATA",

		// Common Windows identity and install roots referenced by CLI tools.
		"USERNAME",
		"USERDOMAIN",
		"PROGRAMFILES",
		"PROGRAMFILES(X86)",
		"PROGRAMDATA",

		// Docker daemon connection (used by Docker SDK's client.FromEnv)
		"DOCKER_HOST",        // Docker daemon address
		"DOCKER_TLS_VERIFY",  // Enable TLS verification
		"DOCKER_CERT_PATH",   // Path to TLS certificates
		"DOCKER_API_VERSION", // Pin Docker API version
	} {
		defaultWhitelist[key] = true
	}
}
