// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scopeSecretValue = "s3cr3t-value-123"

const scopeVariablesSpec = `
env:
  - API_TOKEN: ` + scopeSecretValue + `
  - REGION: eu
consts:
  - service: api
  - retries: 3
secrets:
  - name: DB_PASSWORD
    provider: aws
    key: prod/db/password-key-marker
    options:
      region: option-marker-region
steps:
  - name: one
    run: echo hi
`

func validateScope(t *testing.T, name, spec string) api.ValidateDAGSpec200JSONResponse {
	t.Helper()
	server := test.SetupServer(t)
	var body api.ValidateDAGSpec200JSONResponse
	server.Client().Post("/api/v1/dags/validate", api.ValidateDAGSpecJSONRequestBody{
		Name: &name, Spec: spec,
	}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &body)
	return body
}

func TestValidateScopeVariables(t *testing.T) {
	body := validateScope(t, "scope_variables", scopeVariablesSpec)
	require.True(t, body.Valid, "%v", body.Errors)
	require.NotNil(t, body.ReferenceScope)
	scope := body.ReferenceScope

	require.Len(t, scope.Consts, 2)
	assert.Equal(t, "retries", scope.Consts[0].Name)
	assert.Equal(t, "${consts.retries}", scope.Consts[0].Reference)
	require.NotNil(t, scope.Consts[0].DefaultValue)
	assert.Equal(t, "3", *scope.Consts[0].DefaultValue)
	assert.Equal(t, "${consts.service}", scope.Consts[1].Reference)

	require.Len(t, scope.Env, 2)
	assert.Equal(t, "API_TOKEN", scope.Env[0].Name)
	assert.Equal(t, "${env.API_TOKEN}", scope.Env[0].Reference)
	assert.Equal(t, "REGION", scope.Env[1].Name)

	require.Len(t, scope.Secrets, 1)
	assert.Equal(t, "DB_PASSWORD", scope.Secrets[0].Name)
	assert.Equal(t, "${env.DB_PASSWORD}", scope.Secrets[0].Reference)
}

// The reference scope never carries an env value, a secret key or a secret
// option. The "dag" details can echo the spec, so only the scope is searched.
func TestValidateScopeHidesValues(t *testing.T) {
	body := validateScope(t, "scope_hides_values", scopeVariablesSpec)
	require.NotNil(t, body.ReferenceScope)

	raw, err := json.Marshal(body.ReferenceScope)
	require.NoError(t, err)
	for _, hidden := range []string{
		scopeSecretValue,
		"password-key-marker",
		"option-marker-region",
		"prod/db",
	} {
		assert.NotContains(t, string(raw), hidden)
	}
	for _, v := range append(body.ReferenceScope.Env, body.ReferenceScope.Secrets...) {
		assert.Nil(t, v.DefaultValue, v.Name)
	}
}

// A map const is invalid (specs/004): the load reports it and the scope lists
// no const for it. The env value must not leak from the partial scope.
func TestValidateScopeMapConst(t *testing.T) {
	spec := `
env:
  - API_TOKEN: ` + scopeSecretValue + `
consts:
  - service: api
  - table:
      key: value
steps:
  - name: one
    run: echo hi
`
	body := validateScope(t, "scope_map_const", spec)
	require.False(t, body.Valid)
	require.Len(t, body.Errors, 1)
	assert.Contains(t, body.Errors[0], "consts.table")

	require.NotNil(t, body.ReferenceScope)
	for _, c := range body.ReferenceScope.Consts {
		assert.NotEqual(t, "table", c.Name)
	}
	raw, err := json.Marshal(body.ReferenceScope)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), scopeSecretValue)
}
