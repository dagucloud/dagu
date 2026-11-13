// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runctx_test

import (
	"context"
	"errors"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStepResolver struct {
	calls  []string
	values map[string]string
	err    error
}

func (r *fakeStepResolver) ResolveStepReference(_ context.Context, step string, ref secretref.Ref) (string, error) {
	r.calls = append(r.calls, step+"/"+ref.Name)
	if r.err != nil {
		return "", r.err
	}
	return r.values[ref.Name], nil
}

func stepScopeDAG() *ir.DAG {
	return &ir.DAG{
		Name: "scope",
		Secrets: []secretref.Ref{
			{Name: "GIT_TOKEN", Provider: "env", Key: "X"},
			{Name: "API_KEY", Provider: "env", Key: "Y"},
			{Name: "REG", Ref: "prod/reg"},
		},
		SecretStepScope: true,
		Steps: []ir.Step{
			{Name: "push", Secrets: []string{"GIT_TOKEN", "REG"}},
			{Name: "plain"},
			{Name: "verify", Untrusted: true},
		},
	}
}

func newScopeContext(t *testing.T, dag *ir.DAG, resolver runctx.StepSecretResolver) runctx.Context {
	t.Helper()
	runSecrets := runctx.NewRunSecrets(nil, []secretref.Ref{{Name: "REG", Ref: "prod/reg"}}, resolver)
	ctx := runctx.NewContext(context.Background(), dag, "run-1", "",
		runctx.WithSecrets([]string{"GIT_TOKEN=git-value", "API_KEY=api-value"}),
		runctx.WithRunSecrets(runSecrets),
	)
	return runctx.GetContext(ctx)
}

// Spec 092, "Step scope": a step of a DAG with step scope gets only the
// secrets of its list, a step with no list gets none, and an untrusted step
// gets none. Masking still has every secret of the run.
func TestStepContextFiltersSecrets(t *testing.T) {
	t.Parallel()
	dag := stepScopeDAG()
	rCtx := newScopeContext(t, dag, &fakeStepResolver{values: map[string]string{"REG": "reg-value"}})

	push, err := rCtx.StepContext(context.Background(), dag.Steps[0], false)
	require.NoError(t, err)
	value, ok := push.EnvScope.Get("GIT_TOKEN")
	assert.True(t, ok)
	assert.Equal(t, "git-value", value)
	_, ok = push.EnvScope.Get("API_KEY")
	assert.False(t, ok, "the step does not list API_KEY")
	assert.NotContains(t, push.EnvScope.Expand("${API_KEY}"), "api-value")

	for _, step := range []ir.Step{dag.Steps[1], dag.Steps[2]} {
		scoped, err := rCtx.StepContext(context.Background(), step, true)
		require.NoError(t, err)
		assert.Empty(t, scoped.EnvScope.AllSecrets(), step.Name)
		for _, entry := range scoped.EnvScope.ToSlice() {
			assert.NotContains(t, entry, "git-value", step.Name)
			assert.NotContains(t, entry, "api-value", step.Name)
		}
		assert.Contains(t, scoped.SecretsForMasking(), "GIT_TOKEN=git-value", "masking keeps every secret")
		assert.Contains(t, scoped.SecretsForMasking(), "API_KEY=api-value")
	}

	// The run context itself keeps each secret.
	assert.Len(t, rCtx.EnvScope.AllSecrets(), 2)
}

// A dropped secret does not fall back to a value that it shadows.
func TestStepContextNoFallback(t *testing.T) {
	t.Parallel()
	dag := stepScopeDAG()
	scope := cmnvalue.NewEnvScope(nil, false).
		WithEntry("GIT_TOKEN", "from-dag-env", cmnvalue.EnvSourceDAGEnv).
		WithEntry("GIT_TOKEN", "secret", cmnvalue.EnvSourceSecret)
	rCtx := runctx.Context{DAG: dag, EnvScope: scope, RunSecrets: runctx.NewRunSecrets(nil, nil, nil)}

	plain, err := rCtx.StepContext(context.Background(), dag.Steps[1], false)
	require.NoError(t, err)
	_, ok := plain.EnvScope.Get("GIT_TOKEN")
	assert.False(t, ok)
}

// A DAG without step scope keeps Spec 069, and a step with no name stands for
// the DAG itself.
func TestStepContextWithoutStepScope(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "plain", Steps: []ir.Step{{Name: "a"}}}
	rCtx := newScopeContext(t, dag, nil)

	scoped, err := rCtx.StepContext(context.Background(), dag.Steps[0], true)
	require.NoError(t, err)
	assert.Len(t, scoped.EnvScope.AllSecrets(), 2)

	scopeDAG := stepScopeDAG()
	rCtx = newScopeContext(t, scopeDAG, nil)
	dagLevel, err := rCtx.StepContext(context.Background(), ir.Step{}, true)
	require.NoError(t, err)
	assert.Len(t, dagLevel.EnvScope.AllSecrets(), 2)
}

// A deferred registry secret comes when the step that lists it starts, once
// for the step. The value joins the secrets to mask. A refusal fails with an
// error that names the step and the secret and has no value.
func TestStepContextFetchesDeferredSecret(t *testing.T) {
	t.Parallel()
	dag := stepScopeDAG()
	resolver := &fakeStepResolver{values: map[string]string{"REG": "reg-value"}}
	rCtx := newScopeContext(t, dag, resolver)

	without, err := rCtx.StepContext(context.Background(), dag.Steps[0], false)
	require.NoError(t, err)
	_, ok := without.EnvScope.Get("REG")
	assert.False(t, ok, "no request without fetch")
	assert.Empty(t, resolver.calls)

	push, err := rCtx.StepContext(context.Background(), dag.Steps[0], true)
	require.NoError(t, err)
	value, ok := push.EnvScope.Get("REG")
	require.True(t, ok)
	assert.Equal(t, "reg-value", value)
	assert.Equal(t, []string{"push/REG"}, resolver.calls)
	assert.Contains(t, rCtx.SecretsForMasking(), "REG=reg-value")

	again, err := rCtx.StepContext(context.Background(), dag.Steps[0], true)
	require.NoError(t, err)
	value, _ = again.EnvScope.Get("REG")
	assert.Equal(t, "reg-value", value)
	assert.Len(t, resolver.calls, 1, "a retry uses the first value")

	_, err = rCtx.StepContext(context.Background(), dag.Steps[1], true)
	require.NoError(t, err)
	assert.Len(t, resolver.calls, 1, "a step without the list makes no request")

	denied := &fakeStepResolver{err: errors.New("rpc error: code = PermissionDenied")}
	rCtx = newScopeContext(t, stepScopeDAG(), denied)
	_, err = rCtx.StepContext(context.Background(), dag.Steps[0], true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "push": secret "REG"`)
	assert.ErrorContains(t, err, "PermissionDenied")
}
