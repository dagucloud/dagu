// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/go-viper/mapstructure/v2"
)

// defaults defines the default values for step configuration fields.
// These are applied to every step that does not explicitly set its own value.
type defaults struct {
	ContinueOn    types.ContinueOnValue `yaml:"continue_on,omitempty"`
	RetryPolicy   *retryPolicy          `yaml:"retry_policy,omitempty"`
	RepeatPolicy  *repeatPolicy         `yaml:"repeat_policy,omitempty"`
	TimeoutSec    int                   `yaml:"timeout_sec,omitempty"`
	MailOnError   *bool                 `yaml:"mail_on_error,omitempty"`
	SignalOnStop  *string               `yaml:"signal_on_stop,omitempty"`
	Env           types.EnvValue        `yaml:"env,omitempty"`
	Preconditions any                   `yaml:"preconditions,omitempty"`
}

// decodeDefaults decodes a raw value (from YAML) into a typed *defaults struct.
// Returns nil if raw is nil. Unknown keys cause a validation error.
func decodeDefaults(raw any) (*defaults, error) {
	if raw == nil {
		return nil, nil
	}
	d := new(defaults)
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		ErrorUnused: true,
		Result:      d,
		TagName:     "yaml",
		DecodeHook:  TypedUnionDecodeHook(),
	})
	if err != nil {
		return nil, core.NewValidationError("defaults", raw, err)
	}
	if err := decoder.Decode(raw); err != nil {
		return nil, core.NewValidationError("defaults", raw, withSnakeCaseKeyHint(err))
	}
	return d, nil
}

// applyDefaults merges default values into a step. Override fields are set only
// when the step did not explicitly set its own value. Additive fields (env,
// preconditions) prepend default entries before the step's own entries.
//
// The raw parameter is the original map[string]any from YAML decoding. When
// available, key presence in raw is used to detect explicit values (including
// zero values like timeout_sec: 0 or mail_on_error: false). When raw is nil
// (e.g. handler steps), the function falls back to checking the step field's
// Go zero value.
func applyDefaults(s *step, d *defaults, raw map[string]any) {
	if d == nil {
		return
	}

	// shouldApply reports whether the default for key should be applied.
	// When raw is available, checks that the key was NOT explicitly set in YAML.
	// When raw is nil, falls back to checking the step field's zero value.
	shouldApply := func(key string, isZero bool) bool {
		if raw != nil {
			_, ok := raw[key]
			return !ok
		}
		return isZero
	}

	// Override fields: apply only if step did not explicitly set the field
	if shouldApply("retry_policy", s.RetryPolicy == nil) && d.RetryPolicy != nil {
		s.RetryPolicy = d.RetryPolicy
	}
	if shouldApply("continue_on", s.ContinueOn.IsZero()) && !d.ContinueOn.IsZero() {
		s.ContinueOn = d.ContinueOn
	}
	if shouldApply("repeat_policy", s.RepeatPolicy == nil) && d.RepeatPolicy != nil {
		s.RepeatPolicy = d.RepeatPolicy
	}
	if shouldApply("timeout_sec", s.TimeoutSec == 0) && d.TimeoutSec != 0 {
		s.TimeoutSec = d.TimeoutSec
	}
	if shouldApply("mail_on_error", !s.MailOnError) && d.MailOnError != nil {
		s.MailOnError = *d.MailOnError
	}
	if shouldApply("signal_on_stop", s.SignalOnStop == nil) && d.SignalOnStop != nil {
		s.SignalOnStop = d.SignalOnStop
	}

	// Additive fields: prepend defaults before step values
	if !d.Env.IsZero() {
		s.Env = s.Env.Prepend(d.Env)
	}
	if d.Preconditions != nil {
		if s.Preconditions == nil {
			s.Preconditions = d.Preconditions
		} else {
			s.Preconditions = combinePreconditions(d.Preconditions, s.Preconditions)
		}
	}
}

func mergeDefaults(base, override *defaults, overrideRaw map[string]any) *defaults {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}

	merged := *base

	shouldOverride := func(key string, hasValue bool) bool {
		if overrideRaw != nil {
			_, ok := overrideRaw[key]
			return ok
		}
		return hasValue
	}

	if shouldOverride("continue_on", !override.ContinueOn.IsZero()) {
		merged.ContinueOn = override.ContinueOn
	}
	if shouldOverride("retry_policy", override.RetryPolicy != nil) {
		merged.RetryPolicy = override.RetryPolicy
	}
	if shouldOverride("repeat_policy", override.RepeatPolicy != nil) {
		merged.RepeatPolicy = override.RepeatPolicy
	}
	if shouldOverride("timeout_sec", override.TimeoutSec != 0) {
		merged.TimeoutSec = override.TimeoutSec
	}
	if shouldOverride("mail_on_error", override.MailOnError != nil) {
		merged.MailOnError = override.MailOnError
	}
	if shouldOverride("signal_on_stop", override.SignalOnStop != nil) {
		merged.SignalOnStop = override.SignalOnStop
	}
	if shouldOverride("env", !override.Env.IsZero()) {
		if len(override.Env.Entries()) == 0 {
			merged.Env = override.Env
		} else {
			merged.Env = override.Env.Prepend(merged.Env)
		}
	}
	if shouldOverride("preconditions", override.Preconditions != nil) {
		if isEmptyPreconditionsValue(override.Preconditions) {
			merged.Preconditions = override.Preconditions
		} else if merged.Preconditions == nil {
			merged.Preconditions = override.Preconditions
		} else {
			merged.Preconditions = combinePreconditions(merged.Preconditions, override.Preconditions)
		}
	}
	return &merged
}

func isEmptyPreconditionsValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case []any:
		return len(v) == 0
	default:
		return false
	}
}

// combinePreconditions merges two precondition values into a single []any slice.
// Both values are normalized to arrays and concatenated (first before second).
func combinePreconditions(first, second any) []any {
	normalize := func(v any) []any {
		if arr, ok := v.([]any); ok {
			return arr
		}
		return []any{v}
	}
	a := normalize(first)
	b := normalize(second)
	combined := make([]any, 0, len(a)+len(b))
	combined = append(combined, a...)
	combined = append(combined, b...)
	return combined
}
