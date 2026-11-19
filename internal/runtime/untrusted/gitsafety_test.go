// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// Git applies the last entry of a key, so a later "all" turns it off.
	got = WithGitSafety([]string{
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=safe.bareRepository", "GIT_CONFIG_VALUE_0=explicit",
		"GIT_CONFIG_KEY_1=safe.bareRepository", "GIT_CONFIG_VALUE_1=all",
	})
	assert.ElementsMatch(t, []string{
		"GIT_CONFIG_KEY_0=safe.bareRepository", "GIT_CONFIG_VALUE_0=explicit",
		"GIT_CONFIG_KEY_1=safe.bareRepository", "GIT_CONFIG_VALUE_1=all",
		"GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_2=safe.bareRepository", "GIT_CONFIG_VALUE_2=explicit",
	}, got)
}

// A trusted command with a nil Env inherits the variables of Dagu. It gets the
// setting too, next to the inherited variables.
func TestGitSafetyInheritedEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	t.Setenv("DAGU_TEST_INHERITED", "kept")
	t.Setenv("GIT_CONFIG_COUNT", "")

	cmd := exec.Command("/bin/sh", "-c", `printf '%s %s %s %s' "$DAGU_TEST_INHERITED" "$GIT_CONFIG_COUNT" "$GIT_CONFIG_KEY_0" "$GIT_CONFIG_VALUE_0"`)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	process, err := StartManagedProcess(WithGitSafetyContext(context.Background()), cmd)
	require.NoError(t, err)
	defer func() { _ = process.Release() }()
	require.NoError(t, process.Wait())
	assert.Equal(t, "kept 1 safe.bareRepository explicit", stdout.String())
}
