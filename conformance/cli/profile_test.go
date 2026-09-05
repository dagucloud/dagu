// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"regexp"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// profileStatusPattern matches the "Status" row `profile show` prints, e.g.
// "Status       active".
var profileStatusPattern = regexp.MustCompile(`(?m)^Status\s+(\S+)`)

// profileStatus extracts the exact status value from `profile show` output,
// so callers can assert on the complete value instead of a substring that
// could also match a different status sharing part of the same word.
func profileStatus(stdout string) string {
	match := profileStatusPattern.FindStringSubmatch(stdout)
	if match == nil {
		return ""
	}
	return match[1]
}

func TestProfileCreateListShowDelete(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	create := dagu.RunWithEnv(env, "profile", "create", "myprof", "--description=a test profile")
	create.ExpectExitCode(0)

	list := dagu.RunWithEnv(env, "profile", "list")
	list.ExpectExitCode(0)
	require.Contains(t, list.Stdout(), "myprof")

	show := dagu.RunWithEnv(env, "profile", "show", "myprof")
	show.ExpectExitCode(0)
	require.Contains(t, show.Stdout(), "a test profile")

	del := dagu.RunWithEnv(env, "profile", "delete", "myprof")
	del.ExpectExitCode(0)

	listAfter := dagu.RunWithEnv(env, "profile", "list")
	listAfter.ExpectExitCode(0)
	require.NotContains(t, listAfter.Stdout(), "myprof")
}

// TestProfileSetVarAppliesToRun proves a runtime profile variable actually
// flows into a run selecting it via --profile, exposed the same way step
// env values are: ${env.NAME}.
func TestProfileSetVarAppliesToRun(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	dagu.RunWithEnv(env, "profile", "create", "myprof").ExpectExitCode(0)
	dagu.RunWithEnv(env, "profile", "set-var", "myprof", "MY_VAR", "hello-from-profile").ExpectExitCode(0)

	show := dagu.RunWithEnv(env, "profile", "show", "myprof")
	show.ExpectExitCode(0)
	require.Contains(t, show.Stdout(), "MY_VAR")
	require.Contains(t, show.Stdout(), "hello-from-profile")

	result := dagu.RunWithEnv(env, "start", "--profile=myprof", "profile_consumer.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("profile_var.out", "hello-from-profile\n")
}

// TestProfileSetSecretMasksValueInShow proves a secret entry's value is
// masked when displayed, unlike a plain variable. `profile set-secret` only
// accepts its value via --value-stdin, and the harness has no way to feed a
// subprocess's stdin, so this test cannot actually supply a known value to
// assert is excluded from `show` output -- it is skipped unconditionally
// rather than asserting on a literal that was never the real secret value
// (a `show` bug that leaked the actual stored value would still have passed
// that check). Unskip this once the harness can inject stdin, and assert
// that exact injected value is excluded from `show`'s output.
func TestProfileSetSecretMasksValueInShow(t *testing.T) {
	t.Skip("profile set-secret only accepts a value via --value-stdin, which the harness cannot supply")
}

func TestProfileEnableDisable(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	dagu.RunWithEnv(env, "profile", "create", "myprof").ExpectExitCode(0)

	disable := dagu.RunWithEnv(env, "profile", "disable", "myprof")
	disable.ExpectExitCode(0)

	show := dagu.RunWithEnv(env, "profile", "show", "myprof")
	show.ExpectExitCode(0)
	require.Equal(t, "disabled", profileStatus(show.Stdout()))

	enable := dagu.RunWithEnv(env, "profile", "enable", "myprof")
	enable.ExpectExitCode(0)

	showAgain := dagu.RunWithEnv(env, "profile", "show", "myprof")
	showAgain.ExpectExitCode(0)
	require.Equal(t, "active", profileStatus(showAgain.Stdout()))
}
