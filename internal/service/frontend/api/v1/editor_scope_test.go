// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildReferenceScopeNil(t *testing.T) {
	assert.Nil(t, buildReferenceScope(nil))
}

func TestBuildReferenceScopeEmpty(t *testing.T) {
	scope := buildReferenceScope(&ir.DAG{})
	require.NotNil(t, scope)
	assert.NotNil(t, scope.Params)
	assert.NotNil(t, scope.Consts)
	assert.NotNil(t, scope.Env)
	assert.NotNil(t, scope.Secrets)
	assert.NotNil(t, scope.Steps)
}

func TestBuildReferenceScope(t *testing.T) {
	dag := &ir.DAG{
		ParamDefs: []ir.ParamDef{
			{Name: "region", Default: "eu", Description: "target"},
			{Name: "count", Default: 3},
			{Name: "free"},
			{Name: "blank", Default: ""},
			{},
		},
		Steps: []ir.Step{
			{
				Name: "fetch",
				ID:   "fetch",
				Outputs: []ir.StepOutputDeclaration{
					{Name: "body", Type: "json"},
				},
			},
			{Name: "plain", Depends: []string{"fetch"}, Outputs: []ir.StepOutputDeclaration{{Name: "x"}}},
		},
	}
	scope := buildReferenceScope(dag)
	require.NotNil(t, scope)

	require.Len(t, scope.Params, 4)
	assert.Equal(t, "${params.region}", scope.Params[0].Reference)
	require.NotNil(t, scope.Params[0].DefaultValue)
	assert.Equal(t, "eu", *scope.Params[0].DefaultValue)
	require.NotNil(t, scope.Params[0].Description)
	assert.Equal(t, "3", *scope.Params[1].DefaultValue)
	assert.Nil(t, scope.Params[2].DefaultValue)
	// An empty default is a default, unlike no default.
	require.NotNil(t, scope.Params[3].DefaultValue)
	assert.Empty(t, *scope.Params[3].DefaultValue)

	require.Len(t, scope.Steps, 2)
	fetch := scope.Steps[0]
	assert.Equal(t, "steps[0]", fetch.StepPath)
	assert.Equal(t, []string{}, fetch.Upstream)
	assert.Equal(t, api.ReferenceScopeStepOutputsKnownStatic, fetch.OutputsKnown)
	require.Len(t, fetch.Outputs, 1)
	assert.Equal(t, "${steps.fetch.outputs.body}", fetch.Outputs[0].Reference)
	assert.Equal(t, api.ReferenceScopeOutputSourceDeclared, fetch.Outputs[0].Source)
	require.NotNil(t, fetch.Outputs[0].Type)
	assert.Equal(t, "json", *fetch.Outputs[0].Type)

	plain := scope.Steps[1]
	assert.Equal(t, "steps[1]", plain.StepPath)
	assert.Nil(t, plain.Id)
	assert.Equal(t, []string{"fetch"}, plain.Upstream)
	// A step without an id cannot be referenced.
	assert.Empty(t, plain.Outputs)
	assert.NotNil(t, plain.Outputs)
}

func TestScopeConsts(t *testing.T) {
	ptr := func(s string) *string { return &s }
	tests := []struct {
		name   string
		consts map[string]any
		want   []api.ReferenceScopeVariable
	}{
		{name: "none", consts: nil, want: []api.ReferenceScopeVariable{}},
		{
			name: "sorted scalars",
			consts: map[string]any{
				"zeta":    "last",
				"alpha":   true,
				"count":   int64(4),
				"ratio":   1.5,
				"size":    uint8(7),
				"version": json.Number("12"),
			},
			want: []api.ReferenceScopeVariable{
				{Name: "alpha", Reference: "${consts.alpha}", DefaultValue: ptr("true")},
				{Name: "count", Reference: "${consts.count}", DefaultValue: ptr("4")},
				{Name: "ratio", Reference: "${consts.ratio}", DefaultValue: ptr("1.5")},
				{Name: "size", Reference: "${consts.size}", DefaultValue: ptr("7")},
				{Name: "version", Reference: "${consts.version}", DefaultValue: ptr("12")},
				{Name: "zeta", Reference: "${consts.zeta}", DefaultValue: ptr("last")},
			},
		},
		{
			name:   "empty string is a scalar",
			consts: map[string]any{"blank": ""},
			want: []api.ReferenceScopeVariable{
				{Name: "blank", Reference: "${consts.blank}", DefaultValue: ptr("")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scopeConsts(&ir.DAG{Consts: tt.consts}))
		})
	}
}

// Maps, lists and nil keep their name but give no default value.
func TestScopeConstsNonScalar(t *testing.T) {
	consts := scopeConsts(&ir.DAG{Consts: map[string]any{
		"list": []any{"a", "b"},
		"map":  map[string]any{"k": "v"},
		"nil":  nil,
	}})
	require.Len(t, consts, 3)
	for _, c := range consts {
		assert.Nil(t, c.DefaultValue, c.Name)
		assert.Equal(t, "${consts."+c.Name+"}", c.Reference)
	}
}

// Consts survive a load without evaluation and keep their load-time value.
func TestScopeConstsFromYAML(t *testing.T) {
	const doc = `
consts:
  - service: api
  - endpoint: http://localhost/${consts.service}
  - enabled: true
  - retries: 3
steps:
  - name: one
    run: echo ${consts.endpoint}
`
	dag, err := spec.LoadYAML(context.Background(), []byte(doc), spec.WithAllowBuildErrors(), spec.WithoutEval())
	require.NoError(t, err)

	scope := buildReferenceScope(dag)
	require.NotNil(t, scope)
	got := map[string]string{}
	names := []string{}
	for _, c := range scope.Consts {
		names = append(names, c.Name)
		require.NotNil(t, c.DefaultValue, c.Name)
		got[c.Name] = *c.DefaultValue
	}
	assert.Equal(t, []string{"enabled", "endpoint", "retries", "service"}, names)
	assert.Equal(t, "http://localhost/api", got["endpoint"])
	assert.Equal(t, "true", got["enabled"])
	assert.Equal(t, "3", got["retries"])
}

func TestScopeEnv(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{name: "none", env: nil, want: []string{}},
		{
			name: "sorted and deduplicated",
			env:  []string{"ZONE=eu", "API_URL=http://x", "ZONE=us", "_PRIVATE=1"},
			want: []string{"API_URL", "ZONE", "_PRIVATE"},
		},
		{
			name: "empty value and equal signs in value",
			env:  []string{"EMPTY=", "DSN=host=db user=app"},
			want: []string{"DSN", "EMPTY"},
		},
		{
			name: "invalid entries are skipped",
			env:  []string{"NOEQUALS", "=value", "1BAD=x", "has space=x", "OK=1"},
			want: []string{"OK"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scopeEnv(&ir.DAG{Env: tt.env})
			require.NotNil(t, got)
			names := make([]string, 0, len(got))
			for _, v := range got {
				names = append(names, v.Name)
				assert.Equal(t, "${env."+v.Name+"}", v.Reference)
			}
			assert.Equal(t, tt.want, names)
		})
	}
}

// An env value never reaches the scope, in any field.
func TestScopeEnvHidesValues(t *testing.T) {
	const secret = "s3cr3t-value-123"
	got := scopeEnv(&ir.DAG{Env: []string{"API_TOKEN=" + secret, "OTHER=" + secret}})
	require.Len(t, got, 2)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secret)
	for _, v := range got {
		assert.Nil(t, v.DefaultValue)
		assert.Nil(t, v.Description)
	}
}

func TestScopeEnvFromYAML(t *testing.T) {
	const doc = `
env:
  - API_URL: http://x
  - API_TOKEN: s3cr3t-value-123
  - REGION: ${env.API_URL}
steps:
  - name: one
    run: echo hi
`
	dag, err := spec.LoadYAML(context.Background(), []byte(doc), spec.WithAllowBuildErrors(), spec.WithoutEval())
	require.NoError(t, err)

	scope := buildReferenceScope(dag)
	require.NotNil(t, scope)
	names := []string{}
	for _, v := range scope.Env {
		names = append(names, v.Name)
	}
	assert.Equal(t, []string{"API_TOKEN", "API_URL", "REGION"}, names)
}

func TestScopeSecrets(t *testing.T) {
	desc := func(s string) *string { return &s }
	tests := []struct {
		name    string
		secrets []secretref.Ref
		want    []api.ReferenceScopeVariable
	}{
		{name: "none", want: []api.ReferenceScopeVariable{}},
		{
			name: "sorted and deduplicated",
			secrets: []secretref.Ref{
				{Name: "DB_PASSWORD", Provider: "aws", Key: "prod/db/password"},
				{Name: "API_TOKEN", Provider: "env", Key: "SOURCE_API_TOKEN"},
				{Name: "DB_PASSWORD", Provider: "file", Key: "/run/db"},
			},
			want: []api.ReferenceScopeVariable{
				{Name: "API_TOKEN", Reference: "${env.API_TOKEN}", Description: desc("Secret from provider env")},
				{Name: "DB_PASSWORD", Reference: "${env.DB_PASSWORD}", Description: desc("Secret from provider aws")},
			},
		},
		{
			name:    "registry ref has no description",
			secrets: []secretref.Ref{{Name: "REG_SECRET", Ref: "team/registry-secret"}},
			want: []api.ReferenceScopeVariable{
				{Name: "REG_SECRET", Reference: "${env.REG_SECRET}"},
			},
		},
		{
			name:    "invalid names are skipped",
			secrets: []secretref.Ref{{Name: ""}, {Name: "bad name", Provider: "env", Key: "X"}},
			want:    []api.ReferenceScopeVariable{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scopeSecrets(&ir.DAG{Secrets: tt.secrets}))
		})
	}
}

// Value, key, ref and options never reach the scope.
func TestScopeSecretsHideValues(t *testing.T) {
	got := scopeSecrets(&ir.DAG{Secrets: []secretref.Ref{
		{
			Name:     "API_TOKEN",
			Provider: "aws",
			Key:      "key-marker-77",
			Options:  map[string]string{"region": "option-marker-88", "field": "field-marker-99"},
		},
		{Name: "REG", Ref: "ref-marker-66"},
	}})
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	for _, marker := range []string{"key-marker-77", "option-marker-88", "field-marker-99", "ref-marker-66"} {
		assert.NotContains(t, string(raw), marker)
	}
	for _, v := range got {
		assert.Nil(t, v.DefaultValue)
	}
}

func TestScopeSecretsFromYAML(t *testing.T) {
	const doc = `
secrets:
  - name: DB_PASSWORD
    provider: aws
    key: prod/db/password
    options:
      region: us-east-1
  - name: API_TOKEN
    provider: env
    key: SOURCE_API_TOKEN
steps:
  - name: one
    run: echo hi
`
	dag, err := spec.LoadYAML(context.Background(), []byte(doc), spec.WithAllowBuildErrors(), spec.WithoutEval())
	require.NoError(t, err)

	scope := buildReferenceScope(dag)
	require.NotNil(t, scope)
	names := []string{}
	for _, v := range scope.Secrets {
		names = append(names, v.Name)
	}
	assert.Equal(t, []string{"API_TOKEN", "DB_PASSWORD"}, names)
	raw, err := json.Marshal(scope)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "SOURCE_API_TOKEN")
	assert.NotContains(t, string(raw), "prod/db/password")
}
