// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

func TestRestartPolicy(t *testing.T) {
	t.Parallel()

	steps := "steps:\n  - run: echo tick\n"

	valid := []struct {
		name     string
		authored string
		policy   ir.RestartPolicy
		wait     time.Duration
	}{
		{name: "Always", authored: "restart_policy: always\nrestart_wait_sec: 5\n", policy: ir.RestartPolicyAlways, wait: 5 * time.Second},
		{name: "UnlessStopped", authored: "restart_policy: unless-stopped\nrestart_wait_sec: 1\n", policy: ir.RestartPolicyUnlessStopped, wait: time.Second},
		{name: "CaseInsensitive", authored: "restart_policy: ALWAYS\nrestart_wait_sec: 5\n", policy: ir.RestartPolicyAlways, wait: 5 * time.Second},
		{name: "ExplicitNo", authored: "restart_policy: \"no\"\n", policy: ir.RestartPolicyNo},
		{name: "EmptyIsNo", authored: "restart_policy: \"\"\n", policy: ir.RestartPolicyNo},
		{name: "AbsentIsNo", authored: "", policy: ir.RestartPolicyNo},
		{name: "NoNeedsNoWait", authored: "restart_policy: \"no\"\nschedule: \"* * * * *\"\n", policy: ir.RestartPolicyNo},
		{name: "WaitAloneIsUnchanged", authored: "restart_wait_sec: 3\nschedule: \"* * * * *\"\n", policy: ir.RestartPolicyNo, wait: 3 * time.Second},
		{name: "EmptySchedule", authored: "restart_policy: always\nrestart_wait_sec: 5\nschedule: \"\"\n", policy: ir.RestartPolicyAlways, wait: 5 * time.Second},
		{name: "EmptyScheduleList", authored: "restart_policy: always\nrestart_wait_sec: 5\nschedule: []\n", policy: ir.RestartPolicyAlways, wait: 5 * time.Second},
		{
			name:     "KeepsQueueRetentionAndTimeout",
			authored: "restart_policy: always\nrestart_wait_sec: 5\nqueue: services\ntimeout_sec: 60\nhist_retention_runs: 20\n",
			policy:   ir.RestartPolicyAlways, wait: 5 * time.Second,
		},
	}
	for _, tc := range valid {
		t.Run("Valid"+tc.name, func(t *testing.T) {
			t.Parallel()
			path := createTempYAMLFile(t, tc.authored+steps)
			for _, metadata := range []bool{false, true} {
				opts := []spec.LoadOption{spec.WithoutEval()}
				if metadata {
					opts = append(opts, spec.OnlyMetadata(), spec.SkipSchemaValidation())
				}
				dag, err := spec.Load(t.Context(), path, opts...)
				require.NoError(t, err)
				assert.Equal(t, tc.policy, dag.RestartPolicy)
				assert.Equal(t, tc.wait, dag.RestartWait)
				if tc.policy.Enabled() {
					assert.Empty(t, dag.Schedule)
					assert.Nil(t, dag.RetryPolicy)
				}
			}
		})
	}

	rejected := []struct {
		name     string
		authored string
		contains string
	}{
		{name: "UnknownValue", authored: "restart_policy: sometimes\nrestart_wait_sec: 5\n", contains: "restart_policy"},
		{name: "OnFailureIsNotSupported", authored: "restart_policy: on-failure\nrestart_wait_sec: 5\n", contains: "restart_policy"},
		{name: "MissingWait", authored: "restart_policy: always\n", contains: "restart_wait_sec"},
		{name: "ZeroWait", authored: "restart_policy: always\nrestart_wait_sec: 0\n", contains: "restart_wait_sec"},
		{name: "NegativeWait", authored: "restart_policy: unless-stopped\nrestart_wait_sec: -1\n", contains: "restart_wait_sec"},
		{name: "WithSchedule", authored: "restart_policy: always\nrestart_wait_sec: 5\nschedule: \"* * * * *\"\n", contains: "cannot be combined with schedule"},
		{name: "WithScheduleList", authored: "restart_policy: always\nrestart_wait_sec: 5\nschedule:\n  - \"0 * * * *\"\n  - \"30 * * * *\"\n", contains: "cannot be combined with schedule"},
		{name: "WithStartStopSchedule", authored: "restart_policy: always\nrestart_wait_sec: 5\nschedule:\n  start: \"0 8 * * *\"\n  stop: \"0 18 * * *\"\n", contains: "cannot be combined with schedule"},
		{name: "WithRetryPolicy", authored: "restart_policy: always\nrestart_wait_sec: 5\nretry_policy:\n  limit: 3\n  interval_sec: 5\n", contains: "cannot be combined with retry_policy"},
	}
	for _, tc := range rejected {
		t.Run("Rejected"+tc.name, func(t *testing.T) {
			t.Parallel()
			path := createTempYAMLFile(t, tc.authored+steps)
			_, err := spec.Load(t.Context(), path, spec.WithoutEval())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}

func TestRestartPolicyInTheBaseConfiguration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeBase := func(name, content string) []spec.LoadOption {
		path := filepath.Join(root, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
		return []spec.LoadOption{spec.WithoutEval(), spec.WithBaseConfig(path)}
	}

	for name, policy := range map[string]string{"Always": "always", "UnlessStopped": "unless-stopped"} {
		t.Run("Rejected"+name, func(t *testing.T) {
			t.Parallel()
			opts := writeBase("policy-"+policy+".yaml", "restart_policy: "+policy+"\nrestart_wait_sec: 7\n")
			path := createTempYAMLFile(t, "steps:\n  - run: echo tick\n")
			_, err := spec.Load(t.Context(), path, opts...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "cannot be set in the base configuration")
		})
	}

	t.Run("NoIsAccepted", func(t *testing.T) {
		t.Parallel()
		opts := writeBase("policy-no.yaml", "restart_policy: \"no\"\n")
		path := createTempYAMLFile(t, "steps:\n  - run: echo tick\n")
		dag, err := spec.Load(t.Context(), path, opts...)
		require.NoError(t, err)
		assert.Equal(t, ir.RestartPolicyNo, dag.RestartPolicy)
	})

	t.Run("ServiceDAGDropsWhatItCannotInherit", func(t *testing.T) {
		t.Parallel()
		opts := writeBase("schedule.yaml",
			"restart_wait_sec: 7\nretry_policy:\n  limit: 2\n  interval_sec: 1\nschedule: \"0 * * * *\"\n")
		path := createTempYAMLFile(t, "restart_policy: always\nsteps:\n  - run: echo tick\n")
		dag, err := spec.Load(t.Context(), path, opts...)
		require.NoError(t, err)
		assert.Equal(t, ir.RestartPolicyAlways, dag.RestartPolicy)
		assert.Equal(t, 7*time.Second, dag.RestartWait, "restart_wait_sec is inherited")
		assert.Empty(t, dag.Schedule, "an inherited schedule is ignored")
		assert.Nil(t, dag.RetryPolicy, "an inherited retry_policy is ignored")
	})

	t.Run("ScheduledDAGKeepsItsInheritedSchedule", func(t *testing.T) {
		t.Parallel()
		opts := writeBase("schedule-kept.yaml", "schedule: \"0 * * * *\"\n")
		path := createTempYAMLFile(t, "steps:\n  - run: echo tick\n")
		dag, err := spec.Load(t.Context(), path, opts...)
		require.NoError(t, err)
		assert.Len(t, dag.Schedule, 1)
	})

	t.Run("ServiceDAGWithoutAnyWaitIsRejected", func(t *testing.T) {
		t.Parallel()
		opts := writeBase("no-wait.yaml", "queue: pool\n")
		path := createTempYAMLFile(t, "restart_policy: always\nsteps:\n  - run: echo tick\n")
		_, err := spec.Load(t.Context(), path, opts...)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "restart_wait_sec")
	})
}

// An always-on DAG adds a run at each restart, so without a retention of its
// own it keeps a number of runs rather than the days of other DAGs.
func TestRestartPolicyHistoryRetention(t *testing.T) {
	t.Parallel()

	steps := "steps:\n  - run: echo tick\n"

	for name, tc := range map[string]struct {
		authored string
		base     string
		days     int
		runs     int
	}{
		"Default":          {authored: "restart_policy: always\nrestart_wait_sec: 5\n", runs: 100},
		"RetentionRuns":    {authored: "restart_policy: always\nrestart_wait_sec: 5\nhist_retention_runs: 50\n", runs: 50},
		"RetentionDays":    {authored: "restart_policy: always\nrestart_wait_sec: 5\nhist_retention_days: 2\n", days: 2},
		"BaseRetention":    {authored: "restart_policy: always\nrestart_wait_sec: 5\n", base: "hist_retention_runs: 20\n", runs: 20},
		"NoPolicy":         {authored: "schedule: \"* * * * *\"\n", days: 30},
		"PolicyNoDisabled": {authored: "restart_policy: \"no\"\n", days: 30},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts := []spec.LoadOption{spec.WithoutEval()}
			if tc.base != "" {
				base := filepath.Join(t.TempDir(), "base.yaml")
				require.NoError(t, os.WriteFile(base, []byte(tc.base), 0600))
				opts = append(opts, spec.WithBaseConfig(base))
			}
			dag, err := spec.Load(t.Context(), createTempYAMLFile(t, tc.authored+steps), opts...)
			require.NoError(t, err)
			assert.Equal(t, tc.days, dag.HistRetentionDays)
			assert.Equal(t, tc.runs, dag.HistRetentionRuns)
			assert.Empty(t, dag.BuildWarnings)
		})
	}
}
