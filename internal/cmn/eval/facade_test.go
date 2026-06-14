// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eval_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type facadeSample struct {
	Path   string
	Nested map[string]string
}

func TestFacadeStringObjectAndStringFields(t *testing.T) {
	scope := eval.NewEnvScope(nil, false).WithEntry("ROOT", "/tmp/root", eval.EnvSourceDAGEnv)
	ctx := eval.WithEnvScope(context.Background(), scope)

	got, err := eval.String(
		ctx,
		"$ROOT/$NAME",
		eval.WithVariables(map[string]string{"NAME": "job"}),
		eval.WithoutSubstitute(),
	)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/root/job", got)

	obj, err := eval.Object(ctx, facadeSample{
		Path:   "$ROOT/$NAME",
		Nested: map[string]string{"key": "$NAME"},
	}, map[string]string{"NAME": "run"}, eval.WithoutSubstitute())
	require.NoError(t, err)
	assert.Equal(t, "/tmp/root/run", obj.Path)
	assert.Equal(t, "run", obj.Nested["key"])

	fields, err := eval.StringFields(ctx, facadeSample{Path: "$ROOT"}, eval.WithoutSubstitute())
	require.NoError(t, err)
	assert.Equal(t, "/tmp/root", fields.Path)
}

func TestFacadeExpandReferences(t *testing.T) {
	ctx := context.Background()

	got := eval.ExpandReferences(ctx, "tag=${DATA.image.tag} short=$DATA.name", map[string]string{
		"DATA": `{"image":{"tag":"v1"},"name":"api"}`,
	})
	assert.Equal(t, "tag=v1 short=api", got)

	output := "artifact.tar"
	outputs := `{"id":"abc123"}`
	got = eval.ExpandReferencesWithSteps(ctx, "${build.output} ${build.outputs.id} ${build.stdout}", nil, map[string]eval.StepInfo{
		"build": {
			Output:  &output,
			Outputs: &outputs,
			Stdout:  "/tmp/build.out",
		},
	})
	assert.Equal(t, "artifact.tar abc123 /tmp/build.out", got)
}

func TestFacadeEnvScopeAndOptions(t *testing.T) {
	parent := eval.NewEnvScope(nil, false).WithEntry("BASE", "root", eval.EnvSourceDAGEnv)
	child := eval.NewEnvScope(parent, false).
		WithEntry("BASE", "child", eval.EnvSourceStepEnv).
		WithEntry("SECRET", "value", eval.EnvSourceSecret)

	assert.Equal(t, "child/$UNKNOWN", child.Expand("$BASE/$UNKNOWN"))
	assert.Equal(t, map[string]string{"BASE": "child"}, child.AllBySource(eval.EnvSourceStepEnv))
	assert.Equal(t, map[string]string{"SECRET": "value"}, child.AllSecrets())

	opts := eval.NewOptions()
	eval.WithNoExpansion()(opts)
	eval.WithoutDollarEscape()(opts)

	assert.True(t, opts.NoExpansion)
	assert.False(t, opts.EscapeDollar)
}
