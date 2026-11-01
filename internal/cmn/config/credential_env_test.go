// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A setting added later whose name says it carries a credential is hidden
// from workflows too, rather than left to whoever adds it to remember.
func TestEverySettingThatNamesACredentialIsHiddenFromWorkflows(t *testing.T) {
	t.Parallel()

	// Matched against a setting's last segment: "secrets.vault.address" is
	// about secrets but carries none, and "client_key" names a file.
	credential := regexp.MustCompile(`^(password|passphrase|ssh_passphrase|secret|client_secret|token|auth_key)$`)
	prefix := strings.ToUpper(AppSlug) + "_"
	for _, binding := range envBindings {
		name := binding.key[strings.LastIndex(binding.key, ".")+1:]
		if credential.MatchString(name) || binding.key == "license.key" {
			require.True(t, IsCredentialEnv(prefix+binding.env), "%s (%s) carries a credential", binding.key, binding.env)
		}
	}
}
