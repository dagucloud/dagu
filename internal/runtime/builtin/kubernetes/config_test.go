// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package kubernetes

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromMapValidation(t *testing.T) {
	t.Run("InvalidResourceQuantityReturnsError", func(t *testing.T) {
		var (
			cfg *Config
			err error
		)

		require.NotPanics(t, func() {
			cfg, err = LoadConfigFromMap(map[string]any{
				"image": "busybox",
				"resources": map[string]any{
					"requests": map[string]any{
						"cpu": "not-a-number",
					},
				},
			})
		})

		require.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resources.requests.cpu")
	})

	t.Run("NegativeResourceQuantityReturnsError", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"resources": map[string]any{
				"limits": map[string]any{
					"cpu": "-100m",
				},
			},
		})

		require.Nil(t, cfg)
		require.ErrorIs(t, err, ErrNegativeQuantity)
		assert.Contains(t, err.Error(), "resources.limits.cpu")
	})

	t.Run("InvalidEmptyDirSizeLimitReturnsError", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"volumes": []map[string]any{
				{
					"name": "scratch",
					"empty_dir": map[string]any{
						"size_limit": "bad-size",
					},
				},
			},
		})

		require.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty_dir.size_limit")
	})

	t.Run("InvalidCleanupPolicyReturnsError", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image":          "busybox",
			"cleanup_policy": "archive",
		})

		require.Nil(t, cfg)
		require.ErrorIs(t, err, ErrInvalidCleanupPolicy)
	})

	t.Run("InvalidImagePullPolicyReturnsError", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image":             "busybox",
			"image_pull_policy": "sometimes",
		})

		require.Nil(t, cfg)
		require.ErrorIs(t, err, ErrInvalidImagePullPolicy)
	})

	t.Run("UnsupportedFallbackPolicyReturnsError", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image":             "busybox",
			"image_pull_policy": "fallback",
		})

		require.Nil(t, cfg)
		require.ErrorIs(t, err, ErrUnsupportedFallbackPolicy)
	})

	t.Run("NegativeNumericFieldsReturnError", func(t *testing.T) {
		tests := []struct {
			name string
			key  string
			want error
		}{
			{name: "ActiveDeadline", key: "active_deadline", want: ErrNegativeActiveDeadline},
			{name: "BackoffLimit", key: "backoff_limit", want: ErrNegativeBackoffLimit},
			{name: "TTLAfterFinished", key: "ttl_after_finished", want: ErrNegativeTTLAfterFinished},
			{name: "TerminationGracePeriod", key: "termination_grace_period_seconds", want: ErrNegativeTerminationGrace},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cfg, err := LoadConfigFromMap(map[string]any{
					"image": "busybox",
					tt.key:  -1,
				})

				require.Nil(t, cfg)
				require.ErrorIs(t, err, tt.want)
			})
		}
	})

	t.Run("VolumeMustDefineExactlyOneSource", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"volumes": []map[string]any{
				{
					"name": "mixed",
					"empty_dir": map[string]any{
						"medium": "Memory",
					},
					"secret": map[string]any{
						"secret_name": "app-secret",
					},
				},
			},
		})

		require.Nil(t, cfg)
		require.ErrorIs(t, err, ErrInvalidVolumeSource)
	})

	t.Run("EnvRejectsValueAndValueFromTogether", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"env": []map[string]any{
				{
					"name":  "TOKEN",
					"value": "literal",
					"value_from": map[string]any{
						"secret_key_ref": map[string]any{
							"name": "app-secret",
							"key":  "token",
						},
					},
				},
			},
		})

		require.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env[0]")
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("EnvFromRejectsMissingSource", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"env_from": []map[string]any{
				{
					"prefix": "APP_",
				},
			},
		})

		require.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env_from[0]")
		assert.Contains(t, err.Error(), "exactly one source")
	})

	t.Run("SecurityContextRejectsInvalidSeccompProfile", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"security_context": map[string]any{
				"seccomp_profile": map[string]any{
					"type":              "RuntimeDefault",
					"localhost_profile": "profiles/custom.json",
				},
			},
		})

		require.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "security_context.seccomp_profile.localhost_profile")
	})

	t.Run("PodSecurityContextRejectsNegativeSupplementalGroup", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"pod_security_context": map[string]any{
				"supplemental_groups": []int{-1},
			},
		})

		require.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pod_security_context.supplemental_groups[0]")
	})

	t.Run("AffinityRejectsInvalidWeight", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"affinity": map[string]any{
				"node_affinity": map[string]any{
					"preferred_during_scheduling_ignored_during_execution": []map[string]any{
						{
							"weight": 0,
							"preference": map[string]any{
								"match_expressions": []map[string]any{
									{
										"key":      "kubernetes.io/arch",
										"operator": "In",
										"values":   []string{"amd64"},
									},
								},
							},
						},
					},
				},
			},
		})

		require.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "affinity.node_affinity.preferred_during_scheduling_ignored_during_execution[0].weight")
	})

	t.Run("PodFailurePolicyRejectsFailIndex", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"pod_failure_policy": map[string]any{
				"rules": []map[string]any{
					{
						"action": "FailIndex",
						"on_exit_codes": map[string]any{
							"operator": "In",
							"values":   []int{42},
						},
					},
				},
			},
		})

		require.Nil(t, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FailIndex is not supported")
	})

	t.Run("PodFailurePolicyAllowsEmptyRulesForClearingDefaults", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"pod_failure_policy": map[string]any{
				"rules": []any{},
			},
		})

		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.NotNil(t, cfg.PodFailurePolicy)
		assert.Empty(t, cfg.PodFailurePolicy.Rules)
	})

	t.Run("AffinityAllowsEmptyRequiredNodeSelectorForClearingDefaults", func(t *testing.T) {
		cfg, err := LoadConfigFromMap(map[string]any{
			"image": "busybox",
			"affinity": map[string]any{
				"node_affinity": map[string]any{
					"required_during_scheduling_ignored_during_execution": map[string]any{},
				},
			},
		})

		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.NotNil(t, cfg.Affinity)
		require.NotNil(t, cfg.Affinity.NodeAffinity)
		require.NotNil(t, cfg.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution)
		assert.Empty(t, cfg.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms)
	})
}

func TestValidateStep(t *testing.T) {
	t.Run("RejectsInvalidConfig", func(t *testing.T) {
		err := validateStep(core.Step{
			ExecutorConfig: core.ExecutorConfig{
				Type: "kubernetes",
				Config: map[string]any{
					"image": "busybox",
					"resources": map[string]any{
						"limits": map[string]any{
							"memory": "nope",
						},
					},
				},
			},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "resources.limits.memory")
	})
}

func TestBuildCommand(t *testing.T) {
	step := core.Step{
		Shell: "/bin/sh",
		Commands: []core.CommandEntry{{
			Command:     "echo",
			Args:        []string{"hello"},
			CmdWithArgs: "echo hello",
		}},
	}

	assert.Equal(t, []string{"echo", "hello"}, buildCommand(step))
}

func TestKubernetesDefaultsSchema(t *testing.T) {
	t.Run("DoesNotRequireImage", func(t *testing.T) {
		err := core.ValidateExecutorConfig("kubernetes_defaults", map[string]any{
			"namespace": "dag-ns",
		})
		require.NoError(t, err)
	})

	t.Run("RejectsUnknownKeys", func(t *testing.T) {
		err := core.ValidateExecutorConfig("kubernetes_defaults", map[string]any{
			"unsupported_field": true,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported_field")
	})

	t.Run("RejectsInvalidEnvShape", func(t *testing.T) {
		err := core.ValidateExecutorConfig("kubernetes_defaults", map[string]any{
			"env": []map[string]any{
				{
					"value": "missing-name",
				},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env")
	})

	t.Run("RejectsInvalidEnvFromShape", func(t *testing.T) {
		err := core.ValidateExecutorConfig("kubernetes_defaults", map[string]any{
			"env_from": []map[string]any{
				{
					"prefix": "APP_",
				},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env_from")
	})

	t.Run("RejectsInvalidSeccompSchema", func(t *testing.T) {
		err := core.ValidateExecutorConfig("kubernetes_defaults", map[string]any{
			"security_context": map[string]any{
				"seccomp_profile": map[string]any{
					"localhost_profile": "profiles/custom.json",
				},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "security_context")
	})

	t.Run("RejectsUnsupportedPodFailureAction", func(t *testing.T) {
		err := core.ValidateExecutorConfig("kubernetes_defaults", map[string]any{
			"pod_failure_policy": map[string]any{
				"rules": []map[string]any{
					{
						"action": "FailIndex",
						"on_exit_codes": map[string]any{
							"operator": "In",
							"values":   []int{42},
						},
					},
				},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pod_failure_policy")
	})

	t.Run("AllowsClearingPodFailurePolicyRules", func(t *testing.T) {
		err := core.ValidateExecutorConfig("kubernetes_defaults", map[string]any{
			"pod_failure_policy": map[string]any{
				"rules": []any{},
			},
		})
		require.NoError(t, err)
	})

	t.Run("AllowsClearingRequiredNodeSelector", func(t *testing.T) {
		err := core.ValidateExecutorConfig("kubernetes_defaults", map[string]any{
			"affinity": map[string]any{
				"node_affinity": map[string]any{
					"required_during_scheduling_ignored_during_execution": map[string]any{},
				},
			},
		})
		require.NoError(t, err)
	})
}
