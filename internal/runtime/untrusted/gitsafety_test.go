// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The setting safe.bareRepository=explicit goes after the entries that
// GIT_CONFIG_COUNT already counts, once.
func TestWithGitSafety(t *testing.T) {
	t.Parallel()

	got := WithGitSafety([]string{"PATH=/bin"})
	assert.ElementsMatch(t, []string{
		"PATH=/bin", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=safe.bareRepository", "GIT_CONFIG_VALUE_0=explicit",
	}, got)

	got = WithGitSafety([]string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=dagu"})
	assert.ElementsMatch(t, []string{
		"GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=dagu",
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_1=safe.bareRepository", "GIT_CONFIG_VALUE_1=explicit",
	}, got)

	again := WithGitSafety(got)
	assert.ElementsMatch(t, got, again, "the setting is added once")
}
