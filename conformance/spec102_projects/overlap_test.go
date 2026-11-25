// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec102_projects_test holds black-box conformance tests for
// Spec 102: Projects.
package spec102_projects_test

import (
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestOverlappingPathsRefuseStart proves Spec 102 "Overlapping Paths". The
// install keeps paths.data_dir at its Dagu home, as the Helm chart does, so
// the default paths.projects_dir is <data_dir>/projects, which holds project
// state. With projects on, a CLI command and the scheduler refuse to start
// and name both paths. Moving paths.projects_dir lets the command start, and
// with projects off the same install starts as before.
func TestOverlappingPathsRefuseStart(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	home := filepath.Join(t.TempDir(), "dagu")
	env := []string{"DAGU_HOME=" + home, "DAGU_DATA_DIR=" + home}
	projectsOn := append(slices.Clone(env), "DAGU_EXPERIMENTAL_PROJECTS=true")
	overlap := "paths.projects_dir " + strconv.Quote(filepath.Join(home, "projects")) + " is paths.data_dir/projects"

	for _, args := range [][]string{{"validate", "noop.yaml"}, {"scheduler"}} {
		result := dagu.RunWithEnv(projectsOn, args...)
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(overlap)
	}

	moved := append(slices.Clone(projectsOn), "DAGU_PROJECTS_DIR="+filepath.Join(t.TempDir(), "projects"))
	dagu.RunWithEnv(moved, "validate", "noop.yaml").ExpectExitCode(0)
	dagu.RunWithEnv(env, "validate", "noop.yaml").ExpectExitCode(0)
}
