// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec096_skills_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// A DAG file kept as an asset of a skill is not listed, also with recursive
// discovery. A directory named skills deeper in the tree is read as usual.
func TestRecursiveDiscoverySkipsTheSkillsDirectory(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := []string{
		"DAGU_DAGS_DIR=" + dagu.ProjectPath("discovery"),
		"DAGU_DAG_DISCOVERY_RECURSIVE=true",
	}

	ls := dagu.RunWithEnv(env, "ls")
	ls.ExpectExitCode(0)
	require.Contains(t, ls.Stdout(), "daily")
	require.Contains(t, ls.Stdout(), "report")
	ls.ExpectStdoutNotContains("nightly-example")
	ls.ExpectStderrNotContains("nightly-example")
}
