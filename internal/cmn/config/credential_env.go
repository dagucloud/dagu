// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"sync"
)

// directCredentialEnv names, without the app prefix, the credentials Dagu
// reads from the environment outside envBindings.
var directCredentialEnv = []string{
	"ENCRYPTION_KEY", // the key of the built-in secret store
	"LICENSE",        // an inline license token
	"XLSX_PASSWORD",  // a protected workbook's password, for dagu xlsx
}

// credentialEnv is the set of normalized environment variable names that
// carry Dagu's own credentials.
var credentialEnv = sync.OnceValue(func() map[string]bool {
	prefix := strings.ToUpper(AppSlug) + "_"
	names := make(map[string]bool, len(directCredentialEnv))
	for _, name := range directCredentialEnv {
		names[normalizeEnvKey(prefix+name)] = true
	}
	for _, binding := range envBindings {
		if binding.secret {
			names[normalizeEnvKey(prefix+binding.env)] = true
		}
	}
	return names
})

// IsCredentialEnv reports whether key names an environment variable that
// carries one of Dagu's own credentials, such as the server's password or the
// secret store's key. Dagu's child processes inherit these to load their
// settings; the processes a workflow runs never see them.
func IsCredentialEnv(key string) bool {
	return credentialEnv()[normalizeEnvKey(key)]
}

// WithoutCredentials returns the filtered environment without Dagu's own
// credentials: the environment a workflow's processes start from.
func (b BaseEnv) WithoutCredentials() []string {
	env := make([]string, 0, len(b.variables))
	for _, entry := range b.variables {
		if key, _, found := strings.Cut(entry, "="); found && IsCredentialEnv(key) {
			continue
		}
		env = append(env, entry)
	}
	return env
}
