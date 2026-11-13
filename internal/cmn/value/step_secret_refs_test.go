// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/stretchr/testify/assert"
)

// The names that a text refers to, for the check of Spec 092 that a step does
// not refer to a secret that it does not get.
func TestEnvReferenceNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"echo ${TOKEN}", []string{"TOKEN"}},
		{"echo $TOKEN and $TOKEN", []string{"TOKEN"}},
		{"echo ${env.TOKEN}", []string{"TOKEN"}},
		{"echo ${TOKEN:-default}", []string{"TOKEN"}},
		{"echo '${TOKEN}'", []string{"TOKEN"}},
		{`echo \${TOKEN} \$OTHER`, nil},
		{"echo ${steps.build.outputs.x} ${params.p}", nil},
		{"echo $1 ${A} $B_2", []string{"A", "B_2"}},
		{"", nil},
	} {
		assert.Equal(t, tc.want, value.EnvReferenceNames(tc.raw), tc.raw)
	}
}

// Without drops the winning entry of a name, and the value that it shadows
// does not come back.
func TestEnvScopeWithout(t *testing.T) {
	t.Parallel()

	scope := value.NewEnvScope(nil, false).
		WithEntry("A", "base", value.EnvSourceDAGEnv).
		WithEntry("A", "secret", value.EnvSourceSecret).
		WithEntryOrigin("B", "kept", value.EnvSourceParam, "origin")
	filtered := scope.Without(func(entry value.EnvEntry) bool { return entry.Source == value.EnvSourceSecret })

	_, ok := filtered.Get("A")
	assert.False(t, ok)
	entry, ok := filtered.GetEntry("B")
	assert.True(t, ok)
	assert.Equal(t, value.EnvEntry{Key: "B", Value: "kept", Source: value.EnvSourceParam, Origin: "origin"}, entry)
	got, _ := scope.Get("A")
	assert.Equal(t, "secret", got, "the original scope does not change")
}
