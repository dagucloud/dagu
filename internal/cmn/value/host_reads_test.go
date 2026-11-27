// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Resolving a value records each process environment variable it reads, as
// a DAG env entry reads one while the DAG loads: the value of a variable, or
// the part a JSON path selects. An entry that does not come from the process
// environment is no host read. t.Setenv rules out t.Parallel.
func TestHostReads(t *testing.T) {
	t.Setenv("DAGU_TEST_HOST_READ", "host-value")
	t.Setenv("DAGU_TEST_HOST_JSON", `{"token":"json-token","region":"us"}`)

	loading := NewEnvScope(nil, true).WithEntry("LOCAL", "local-value", EnvSourceDAGEnv)
	forwarded := NewEnvScope(nil, false).WithEntry("DAGU_TEST_HOST_READ", "forwarded-value", EnvSourceOS)
	tests := []struct {
		name  string
		raw   string
		field Field
		scope *EnvScope
		want  map[string][]string
	}{
		{name: "variable", raw: "${DAGU_TEST_HOST_READ}", field: DAGEnvField("env.A"), scope: loading,
			want: map[string][]string{"DAGU_TEST_HOST_READ": {"host-value"}}},
		{name: "unbraced variable", raw: "x-$DAGU_TEST_HOST_READ", field: DAGEnvField("env.A"), scope: loading,
			want: map[string][]string{"DAGU_TEST_HOST_READ": {"host-value"}}},
		{name: "env namespace", raw: "${env.DAGU_TEST_HOST_READ}", field: DAGEnvField("env.A"), scope: loading,
			want: map[string][]string{"DAGU_TEST_HOST_READ": {"host-value"}}},
		{name: "JSON path", raw: "${DAGU_TEST_HOST_JSON.token}", field: DAGEnvField("env.A"), scope: loading,
			want: map[string][]string{"DAGU_TEST_HOST_JSON": {"json-token"}}},
		{name: "forwarded entry", raw: "${env.DAGU_TEST_HOST_READ}", field: WorkflowField("with.a"), scope: forwarded,
			want: map[string][]string{"DAGU_TEST_HOST_READ": {"forwarded-value"}}},
		{name: "DAG env entry", raw: "${LOCAL}", field: DAGEnvField("env.A"), scope: loading, want: map[string][]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := TrackHostReads(context.Background())
			resolver := NewResolver(StaticScope{}, RuntimeScope{Env: tt.scope})
			_, err := resolver.String(ctx, tt.raw, tt.field)
			require.NoError(t, err)
			assert.Equal(t, tt.want, HostReads(ctx))
		})
	}

	ctx := context.Background()
	_, err := NewResolver(StaticScope{}, RuntimeScope{Env: loading}).String(ctx, "${DAGU_TEST_HOST_READ}", DAGEnvField("env.A"))
	require.NoError(t, err)
	assert.Nil(t, HostReads(ctx))
}
