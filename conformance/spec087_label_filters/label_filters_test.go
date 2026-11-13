// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec087_label_filters_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

const (
	prodRun  = "spec087-prod"
	devRun   = "spec087-dev"
	plainRun = "spec087-plain"
)

// Three runs carry env=prod, env=dev, or no labels. Each filter selects
// exactly the runs it names.
func TestLabelFilters(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	// The runs and the history reads share one Dagu home.
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
	dagu.RunWithEnv(env, "start", "--run-id="+prodRun, "--labels=env=prod,critical", "simple.yaml").ExpectExitCode(0)
	dagu.RunWithEnv(env, "start", "--run-id="+devRun, "--labels=env=dev,critical", "simple.yaml").ExpectExitCode(0)
	dagu.RunWithEnv(env, "start", "--run-id="+plainRun, "simple.yaml").ExpectExitCode(0)

	for _, tc := range []struct {
		filter string
		want   []string
	}{
		{"critical", []string{prodRun, devRun}},
		{"env=prod", []string{prodRun}},
		{"ENV=PROD", []string{prodRun}},
		{"env=*", []string{prodRun, devRun}},
		{"e?v=d*", []string{devRun}},
		{"!env", []string{plainRun}},
		{"!env=dev", []string{prodRun, plainRun}},
		{"critical,!env=dev", []string{prodRun}},
		{"!env=p*", []string{devRun, plainRun}},
	} {
		t.Run(tc.filter, func(t *testing.T) {
			result := dagu.RunWithEnv(env, "history", "--labels="+tc.filter, "--format=json")
			result.ExpectExitCode(0)
			for _, run := range []string{prodRun, devRun, plainRun} {
				if slices.Contains(tc.want, run) {
					require.Contains(t, result.Stdout(), run)
				} else {
					require.NotContains(t, result.Stdout(), run)
				}
			}
		})
	}
}

func TestInvalidLabelFilters(t *testing.T) {
	t.Parallel()

	for _, filter := range []string{"env==prod", "!", "=prod", "env prod"} {
		t.Run(filter, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("history", "--labels="+filter)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(`invalid label filter "` + filter + `"`)
		})
	}
}
