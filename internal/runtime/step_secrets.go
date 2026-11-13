// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Spec 092, "Secrets of a step". The run context keeps the secrets of the run.
// Each step sees a copy of it whose scope holds only the secrets that the step
// gets, so that neither the expansion of ${NAME} nor the variables of a process
// reach another secret. Masking still uses every secret of the run.

// stepDAGContext returns the run context of the step with the scope that the
// step gets. It makes no request for a deferred registry secret.
func stepDAGContext(ctx context.Context, step ir.Step) Context {
	rCtx, _ := GetDAGContext(ctx).StepContext(ctx, step, false)
	return rCtx
}

// withStepSecretContext returns ctx with the run context of the step: the
// scope keeps only the secrets that the step gets, and the step fetches each
// deferred registry secret of its list. A failed fetch fails the step; the
// error names the step and the secret and has no value.
func withStepSecretContext(ctx context.Context, step ir.Step) (context.Context, error) {
	stepCtx, err := GetDAGContext(ctx).StepContext(ctx, step, true)
	if err != nil {
		return ctx, err
	}
	return WithDAGContext(ctx, stepCtx), nil
}

// SecretsForMasking returns the values of every secret of the run by name,
// also of the secrets that the current step does not get. Use it to mask a text
// or to refuse a text that holds a secret.
func SecretsForMasking(ctx context.Context) map[string]string {
	pairs := GetDAGContext(ctx).SecretsForMasking()
	if env, ok := LookupEnv(ctx); ok && env.Scope != nil {
		secrets := env.Scope.AllSecrets()
		for _, key := range slices.Sorted(maps.Keys(secrets)) {
			pairs = append(pairs, key+"="+secrets[key])
		}
	}
	values := make(map[string]string, len(pairs))
	for i, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || value == "" {
			continue
		}
		if previous, exists := values[name]; exists {
			if previous == value {
				continue
			}
			// A second value with the same name keeps its own key, so that
			// both are masked. The key never holds the value.
			name = name + "#" + strconv.Itoa(i)
		}
		values[name] = value
	}
	return values
}
