// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/stretchr/testify/require"
)

func TestApplyDefaults(t *testing.T) {
	t.Parallel()

	boolPtr := func(b bool) *bool { return &b }
	strPtr := func(s string) *string { return &s }

	t.Run("NilDefaults", func(t *testing.T) {
		t.Parallel()
		s := &step{TimeoutSec: 10}
		applyDefaults(s, nil, nil)
		require.Equal(t, 10, s.TimeoutSec)
	})

	t.Run("EmptyDefaults", func(t *testing.T) {
		t.Parallel()
		s := &step{TimeoutSec: 10}
		applyDefaults(s, &defaults{}, nil)
		require.Equal(t, 10, s.TimeoutSec)
	})

	t.Run("RetryPolicy_Inherits", func(t *testing.T) {
		t.Parallel()
		rp := &retryPolicy{Limit: 3, IntervalSec: 5}
		s := &step{}
		applyDefaults(s, &defaults{RetryPolicy: rp}, nil)
		require.Equal(t, rp, s.RetryPolicy)
	})

	t.Run("RetryPolicy_StepOverrides", func(t *testing.T) {
		t.Parallel()
		stepRP := &retryPolicy{Limit: 10}
		defRP := &retryPolicy{Limit: 3, IntervalSec: 5}
		s := &step{RetryPolicy: stepRP}
		applyDefaults(s, &defaults{RetryPolicy: defRP}, nil)
		require.Equal(t, stepRP, s.RetryPolicy)
		require.Equal(t, 10, s.RetryPolicy.Limit.(int))
	})

	t.Run("RetryPolicy_StepOverrides_WithRaw", func(t *testing.T) {
		t.Parallel()
		defRP := &retryPolicy{Limit: 3, IntervalSec: 5}
		s := &step{}
		raw := map[string]any{"retry_policy": map[string]any{"limit": 0}}
		applyDefaults(s, &defaults{RetryPolicy: defRP}, raw)
		// Step explicitly set retry_policy in raw, so default should NOT apply
		require.Nil(t, s.RetryPolicy)
	})

	t.Run("ContinueOn_Inherits", func(t *testing.T) {
		t.Parallel()
		co := continueOnValue("failed")
		s := &step{}
		applyDefaults(s, &defaults{ContinueOn: co}, nil)
		require.False(t, s.ContinueOn.IsZero())
		require.True(t, s.ContinueOn.Failed())
	})

	t.Run("ContinueOn_StepOverrides", func(t *testing.T) {
		t.Parallel()
		stepCO := continueOnValue("skipped")
		defCO := continueOnValue("failed")
		s := &step{ContinueOn: stepCO}
		applyDefaults(s, &defaults{ContinueOn: defCO}, nil)
		require.True(t, s.ContinueOn.Skipped())
		require.False(t, s.ContinueOn.Failed())
	})

	t.Run("RepeatPolicy_Inherits", func(t *testing.T) {
		t.Parallel()
		rp := &repeatPolicy{Repeat: types.RepeatModeFromString("while"), IntervalSec: types.IntOrDynamicFromInt(30)}
		s := &step{}
		applyDefaults(s, &defaults{RepeatPolicy: rp}, nil)
		require.Equal(t, rp, s.RepeatPolicy)
	})

	t.Run("RepeatPolicy_StepOverrides", func(t *testing.T) {
		t.Parallel()
		stepRP := &repeatPolicy{Repeat: types.RepeatModeFromString("until"), IntervalSec: types.IntOrDynamicFromInt(10)}
		defRP := &repeatPolicy{Repeat: types.RepeatModeFromString("while"), IntervalSec: types.IntOrDynamicFromInt(30)}
		s := &step{RepeatPolicy: stepRP}
		applyDefaults(s, &defaults{RepeatPolicy: defRP}, nil)
		require.Equal(t, stepRP, s.RepeatPolicy)
	})

	t.Run("TimeoutSec_Inherits", func(t *testing.T) {
		t.Parallel()
		s := &step{}
		applyDefaults(s, &defaults{TimeoutSec: 600}, nil)
		require.Equal(t, 600, s.TimeoutSec)
	})

	t.Run("TimeoutSec_StepOverrides", func(t *testing.T) {
		t.Parallel()
		s := &step{TimeoutSec: 300}
		applyDefaults(s, &defaults{TimeoutSec: 600}, nil)
		require.Equal(t, 300, s.TimeoutSec)
	})

	t.Run("TimeoutSec_StepExplicitZero_WithRaw", func(t *testing.T) {
		t.Parallel()
		s := &step{}
		raw := map[string]any{"timeout_sec": 0}
		applyDefaults(s, &defaults{TimeoutSec: 600}, raw)
		// Step explicitly set timeout_sec: 0 in raw, default should NOT apply
		require.Equal(t, 0, s.TimeoutSec)
	})

	t.Run("MailOnError_Inherits_True", func(t *testing.T) {
		t.Parallel()
		s := &step{}
		applyDefaults(s, &defaults{MailOnError: boolPtr(true)}, nil)
		require.True(t, s.MailOnError)
	})

	t.Run("MailOnError_NilDefault_NoChange", func(t *testing.T) {
		t.Parallel()
		s := &step{}
		applyDefaults(s, &defaults{MailOnError: nil}, nil)
		require.False(t, s.MailOnError)
	})

	t.Run("MailOnError_StepTrue_DefaultFalse", func(t *testing.T) {
		t.Parallel()
		s := &step{MailOnError: true}
		applyDefaults(s, &defaults{MailOnError: boolPtr(false)}, nil)
		// Step already has true, default false doesn't override
		require.True(t, s.MailOnError)
	})

	t.Run("MailOnError_StepExplicitFalse_WithRaw", func(t *testing.T) {
		t.Parallel()
		s := &step{}
		raw := map[string]any{"mail_on_error": false}
		applyDefaults(s, &defaults{MailOnError: boolPtr(true)}, raw)
		// Step explicitly set mail_on_error: false in raw, default should NOT apply
		require.False(t, s.MailOnError)
	})

	t.Run("SignalOnStop_Inherits", func(t *testing.T) {
		t.Parallel()
		s := &step{}
		applyDefaults(s, &defaults{SignalOnStop: strPtr("SIGTERM")}, nil)
		require.NotNil(t, s.SignalOnStop)
		require.Equal(t, "SIGTERM", *s.SignalOnStop)
	})

	t.Run("SignalOnStop_StepOverrides", func(t *testing.T) {
		t.Parallel()
		s := &step{SignalOnStop: strPtr("SIGINT")}
		applyDefaults(s, &defaults{SignalOnStop: strPtr("SIGTERM")}, nil)
		require.Equal(t, "SIGINT", *s.SignalOnStop)
	})

	t.Run("Env_Inherits_WhenStepEmpty", func(t *testing.T) {
		t.Parallel()
		defEnv := envValueMap(map[string]string{"LOG_LEVEL": "info"})
		s := &step{}
		applyDefaults(s, &defaults{Env: defEnv}, nil)
		require.False(t, s.Env.IsZero())
		entries := s.Env.Entries()
		require.Len(t, entries, 1)
		require.Equal(t, "LOG_LEVEL", entries[0].Key)
		require.Equal(t, "info", entries[0].Value)
	})

	t.Run("Env_Additive", func(t *testing.T) {
		t.Parallel()
		defEnv := envValueMap(map[string]string{"LOG_LEVEL": "info"})
		stepEnv := envValueMap(map[string]string{"EXTRA": "true"})
		s := &step{Env: stepEnv}
		applyDefaults(s, &defaults{Env: defEnv}, nil)

		entries := s.Env.Entries()
		require.Len(t, entries, 2)
		// Default entry comes first
		require.Equal(t, "LOG_LEVEL", entries[0].Key)
		require.Equal(t, "EXTRA", entries[1].Key)
	})

	t.Run("Preconditions_Inherits_WhenStepNil", func(t *testing.T) {
		t.Parallel()
		defPrecond := "test -f /tmp/ready"
		s := &step{}
		applyDefaults(s, &defaults{Preconditions: defPrecond}, nil)
		require.Equal(t, defPrecond, s.Preconditions)
	})

	t.Run("Preconditions_Additive_StringAndString", func(t *testing.T) {
		t.Parallel()
		s := &step{Preconditions: "test -d /data"}
		applyDefaults(s, &defaults{Preconditions: "test -f /tmp/ready"}, nil)
		combined, ok := s.Preconditions.([]any)
		require.True(t, ok)
		require.Len(t, combined, 2)
		require.Equal(t, "test -f /tmp/ready", combined[0])
		require.Equal(t, "test -d /data", combined[1])
	})

	t.Run("Preconditions_Additive_ArrayAndArray", func(t *testing.T) {
		t.Parallel()
		defPrecond := []any{"check1", "check2"}
		stepPrecond := []any{"check3"}
		s := &step{Preconditions: stepPrecond}
		applyDefaults(s, &defaults{Preconditions: defPrecond}, nil)
		combined, ok := s.Preconditions.([]any)
		require.True(t, ok)
		require.Len(t, combined, 3)
		require.Equal(t, "check1", combined[0])
		require.Equal(t, "check2", combined[1])
		require.Equal(t, "check3", combined[2])
	})

	t.Run("AllFieldsSimultaneously", func(t *testing.T) {
		t.Parallel()
		d := &defaults{
			RetryPolicy:   &retryPolicy{Limit: 3, IntervalSec: 5},
			ContinueOn:    continueOnValue("failed"),
			RepeatPolicy:  &repeatPolicy{Repeat: types.RepeatModeFromString("while"), IntervalSec: types.IntOrDynamicFromInt(30)},
			TimeoutSec:    600,
			MailOnError:   boolPtr(true),
			SignalOnStop:  strPtr("SIGTERM"),
			Env:           envValueMap(map[string]string{"DEFAULT_VAR": "value"}),
			Preconditions: "test -f /ready",
		}

		s := &step{}
		applyDefaults(s, d, nil)

		require.NotNil(t, s.RetryPolicy)
		require.False(t, s.ContinueOn.IsZero())
		require.NotNil(t, s.RepeatPolicy)
		require.Equal(t, 600, s.TimeoutSec)
		require.True(t, s.MailOnError)
		require.NotNil(t, s.SignalOnStop)
		require.Equal(t, "SIGTERM", *s.SignalOnStop)
		require.False(t, s.Env.IsZero())
		require.Equal(t, "test -f /ready", s.Preconditions)
	})
}

func TestMergeDefaults(t *testing.T) {
	t.Parallel()

	boolPtr := func(b bool) *bool { return &b }
	strPtr := func(s string) *string { return &s }

	t.Run("NilHandling", func(t *testing.T) {
		t.Parallel()

		base := &defaults{TimeoutSec: 10}
		override := &defaults{TimeoutSec: 20}

		require.Nil(t, mergeDefaults(nil, nil, nil))
		require.Same(t, base, mergeDefaults(base, nil, nil))
		require.Same(t, override, mergeDefaults(nil, override, nil))
	})

	t.Run("OverrideReplacementAndComposeAdditiveFields", func(t *testing.T) {
		t.Parallel()

		base := &defaults{
			ContinueOn:    continueOnValue("failed"),
			RetryPolicy:   &retryPolicy{Limit: 1, IntervalSec: 60},
			RepeatPolicy:  &repeatPolicy{Repeat: types.RepeatModeFromString("while"), Condition: "true", IntervalSec: types.IntOrDynamicFromInt(30)},
			TimeoutSec:    600,
			MailOnError:   boolPtr(true),
			SignalOnStop:  strPtr("SIGTERM"),
			Env:           envValueMap(map[string]string{"BASE_ONLY": "base-only"}),
			Preconditions: []any{"base-check"},
		}
		override := &defaults{
			ContinueOn:    continueOnValue("skipped"),
			RetryPolicy:   &retryPolicy{Limit: 7, IntervalSec: 3},
			RepeatPolicy:  &repeatPolicy{Repeat: types.RepeatModeFromString("until"), Condition: "cat /tmp/status", Expected: "done", IntervalSec: types.IntOrDynamicFromInt(11)},
			TimeoutSec:    300,
			MailOnError:   boolPtr(false),
			SignalOnStop:  strPtr("SIGINT"),
			Env:           envValueMap(map[string]string{"OVERRIDE_ONLY": "override-only"}),
			Preconditions: []any{"override-check"},
		}

		merged := mergeDefaults(base, override, nil)
		require.NotNil(t, merged)
		require.NotSame(t, base, merged)
		require.True(t, merged.ContinueOn.Skipped())
		require.False(t, merged.ContinueOn.Failed())
		require.Equal(t, override.RetryPolicy, merged.RetryPolicy)
		require.Equal(t, override.RepeatPolicy, merged.RepeatPolicy)
		require.Equal(t, 300, merged.TimeoutSec)
		require.Equal(t, false, *merged.MailOnError)
		require.Equal(t, "SIGINT", *merged.SignalOnStop)

		envEntries := merged.Env.Entries()
		require.Len(t, envEntries, 2)
		require.Equal(t, "BASE_ONLY", envEntries[0].Key)
		require.Equal(t, "base-only", envEntries[0].Value)
		require.Equal(t, "OVERRIDE_ONLY", envEntries[1].Key)
		require.Equal(t, "override-only", envEntries[1].Value)

		preconditions, ok := merged.Preconditions.([]any)
		require.True(t, ok)
		require.Equal(t, []any{"base-check", "override-check"}, preconditions)

		baseEnvEntries := base.Env.Entries()
		require.Len(t, baseEnvEntries, 1)
		require.Equal(t, "BASE_ONLY", baseEnvEntries[0].Key)
		require.Equal(t, "base-only", baseEnvEntries[0].Value)
	})

	t.Run("ExplicitZeroAndEmptyOverridesWithRaw", func(t *testing.T) {
		t.Parallel()

		base := &defaults{
			TimeoutSec:    600,
			Env:           envValueMap(map[string]string{"BASE_ONLY": "base-only"}),
			Preconditions: []any{"base-check"},
		}
		overrideRaw := map[string]any{
			"timeout_sec":   0,
			"env":           []any{},
			"preconditions": []any{},
		}
		override, err := decodeDefaults(overrideRaw)
		require.NoError(t, err)

		merged := mergeDefaults(base, override, overrideRaw)
		require.NotNil(t, merged)
		require.Zero(t, merged.TimeoutSec)
		require.Empty(t, merged.Env.Entries())
		preconditions, ok := merged.Preconditions.([]any)
		require.True(t, ok)
		require.Empty(t, preconditions)
	})
}

func TestDecodeDefaults(t *testing.T) {
	t.Parallel()

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		d, err := decodeDefaults(nil)
		require.NoError(t, err)
		require.Nil(t, d)
	})

	t.Run("ValidAllFields", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"retry_policy": map[string]any{
				"limit":        3,
				"interval_sec": 5,
			},
			"continue_on":    "failed",
			"repeat_policy":  map[string]any{"repeat": "while", "interval_sec": 30},
			"timeout_sec":    600,
			"mail_on_error":  true,
			"signal_on_stop": "SIGTERM",
			"env":            []any{"LOG_LEVEL=info"},
			"preconditions":  "test -f /ready",
		}

		d, err := decodeDefaults(raw)
		require.NoError(t, err)
		require.NotNil(t, d)

		require.NotNil(t, d.RetryPolicy)
		require.Equal(t, 3, d.RetryPolicy.Limit)
		require.False(t, d.ContinueOn.IsZero())
		require.NotNil(t, d.RepeatPolicy)
		require.Equal(t, 600, d.TimeoutSec)
		require.NotNil(t, d.MailOnError)
		require.True(t, *d.MailOnError)
		require.NotNil(t, d.SignalOnStop)
		require.Equal(t, "SIGTERM", *d.SignalOnStop)
		require.False(t, d.Env.IsZero())
		require.Equal(t, "test -f /ready", d.Preconditions)
	})

	t.Run("UnknownKey_Error", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"timeout_sec":   600,
			"unknown_field": "value",
		}

		d, err := decodeDefaults(raw)
		require.Error(t, err)
		require.Nil(t, d)
		require.Contains(t, err.Error(), "defaults")
	})

	t.Run("EmptyMap", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{}

		d, err := decodeDefaults(raw)
		require.NoError(t, err)
		require.NotNil(t, d)
		// All fields at zero values
		require.Nil(t, d.RetryPolicy)
		require.True(t, d.ContinueOn.IsZero())
		require.Equal(t, 0, d.TimeoutSec)
	})
}

func TestCombinePreconditions(t *testing.T) {
	t.Parallel()

	t.Run("StringAndString", func(t *testing.T) {
		t.Parallel()
		result := combinePreconditions("a", "b")
		require.Equal(t, []any{"a", "b"}, result)
	})

	t.Run("ArrayAndArray", func(t *testing.T) {
		t.Parallel()
		result := combinePreconditions([]any{"a", "b"}, []any{"c"})
		require.Equal(t, []any{"a", "b", "c"}, result)
	})

	t.Run("StringAndArray", func(t *testing.T) {
		t.Parallel()
		result := combinePreconditions("a", []any{"b", "c"})
		require.Equal(t, []any{"a", "b", "c"}, result)
	})

	t.Run("ArrayAndString", func(t *testing.T) {
		t.Parallel()
		result := combinePreconditions([]any{"a", "b"}, "c")
		require.Equal(t, []any{"a", "b", "c"}, result)
	})

	t.Run("MapAndString", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"condition": "test", "expected": "ok"}
		result := combinePreconditions(m, "check")
		require.Len(t, result, 2)
		require.Equal(t, m, result[0])
		require.Equal(t, "check", result[1])
	})
}
