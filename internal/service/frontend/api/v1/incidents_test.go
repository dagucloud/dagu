// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/api/v1"
	incidentmodel "github.com/dagucloud/dagu/internal/incident"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/service/frontend"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncidentManagement_RequireActiveLicense(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	server.Client().Get("/api/v1/incident-providers").
		ExpectStatus(http.StatusForbidden).Send(t)
}

func TestIncidentManagement_AcceptsExistingLicenseWithoutFeatureClaim(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithServerOptions(frontend.WithLicenseManager(license.NewTestManager())),
	)
	resp := server.Client().Get("/api/v1/incident-providers").
		ExpectStatus(http.StatusOK).Send(t)

	var result api.IncidentProviderListResponse
	resp.Unmarshal(t, &result)
	assert.Empty(t, result.Providers)
}

func TestIncidentManagement_GlobalWorkspaceAndDAGPolicySets(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithServerOptions(frontend.WithLicenseManager(license.NewTestManager())),
	)

	routingKey := "pagerduty-routing-key"
	providerInput := api.IncidentProviderInput{}
	require.NoError(t, providerInput.FromIncidentPagerDutyProviderInputEnvelope(api.IncidentPagerDutyProviderInputEnvelope{
		Name:    "PagerDuty",
		Enabled: true,
		PagerDuty: api.IncidentPagerDutyProviderInput{
			RoutingKey: &routingKey,
		},
	}))
	providerResp := server.Client().Post("/api/v1/incident-providers", providerInput).
		ExpectStatus(http.StatusCreated).Send(t)
	var provider api.IncidentProvider
	providerResp.Unmarshal(t, &provider)
	require.NotEmpty(t, provider.Id)
	require.NotNil(t, provider.PagerDuty)
	assert.True(t, provider.PagerDuty.RoutingKeyConfigured)

	messageTemplate := "Dagu DAG {{dag.name}} failed"
	descriptionTemplate := "Run {{run.id}} failed\n{{run.link}}"
	dedupKeyTemplate := "dagu:{{workspace}}:{{dag.name}}"
	globalResp := server.Client().Put("/api/v1/incident-policies/global", api.IncidentPolicySetInput{
		Enabled:       true,
		InheritParent: false,
		Policies: []api.IncidentPolicyInput{{
			Id:                  new("global-policy"),
			ProviderId:          provider.Id,
			Enabled:             true,
			Severity:            api.IncidentSeverityCritical,
			ResolveOnRecovery:   new(false),
			DedupKeyTemplate:    &dedupKeyTemplate,
			MessageTemplate:     &messageTemplate,
			DescriptionTemplate: &descriptionTemplate,
		}},
	}).ExpectStatus(http.StatusOK).Send(t)
	var globalSet api.IncidentPolicySet
	globalResp.Unmarshal(t, &globalSet)
	assert.Equal(t, api.IncidentPolicyScopeGlobal, globalSet.Scope)
	assert.False(t, globalSet.InheritParent)
	require.Len(t, globalSet.Policies, 1)
	assert.Equal(t, "global-policy", globalSet.Policies[0].Id)
	assert.True(t, globalSet.Policies[0].ResolveOnRecovery)
	assert.Equal(t, incidentmodel.DefaultDedupKeyTemplate, globalSet.Policies[0].DedupKeyTemplate)

	server.Client().Post("/api/v1/workspaces", api.CreateWorkspaceRequest{
		Name: "ops",
	}).ExpectStatus(http.StatusCreated).Send(t)
	workspaceResp := server.Client().Put("/api/v1/incident-policies/workspaces/ops", api.IncidentPolicySetInput{
		Enabled:       true,
		InheritParent: true,
		Policies:      []api.IncidentPolicyInput{},
	}).ExpectStatus(http.StatusOK).Send(t)
	var workspaceSet api.IncidentPolicySet
	workspaceResp.Unmarshal(t, &workspaceSet)
	assert.Equal(t, api.IncidentPolicyScopeWorkspace, workspaceSet.Scope)
	assert.Equal(t, "ops", testValue(workspaceSet.Workspace))
	assert.True(t, workspaceSet.InheritParent)

	dagName := "daily-report"
	createTestDAG(t, server, "", dagName)
	dagDefaultResp := server.Client().Get("/api/v1/dags/" + dagName + "/incidents").
		ExpectStatus(http.StatusOK).Send(t)
	var dagDefault api.IncidentPolicySet
	dagDefaultResp.Unmarshal(t, &dagDefault)
	assert.Equal(t, api.IncidentPolicyScopeDag, dagDefault.Scope)
	assert.True(t, dagDefault.InheritParent)
	assert.Empty(t, dagDefault.Policies)

	dagResp := server.Client().Put("/api/v1/dags/"+dagName+"/incidents", api.IncidentPolicySetInput{
		Enabled:       true,
		InheritParent: false,
		Policies: []api.IncidentPolicyInput{{
			Id:                new("dag-policy"),
			ProviderId:        provider.Id,
			Enabled:           true,
			Severity:          api.IncidentSeverityError,
			ResolveOnRecovery: new(true),
		}},
	}).ExpectStatus(http.StatusOK).Send(t)
	var dagSet api.IncidentPolicySet
	dagResp.Unmarshal(t, &dagSet)
	assert.Equal(t, api.IncidentPolicyScopeDag, dagSet.Scope)
	assert.Equal(t, dagName, testValue(dagSet.DagName))
	assert.False(t, dagSet.InheritParent)
	require.Len(t, dagSet.Policies, 1)
	assert.Equal(t, "dag-policy", dagSet.Policies[0].Id)
	assert.Equal(t, "Dagu DAG {{dag.name}} failed", dagSet.Policies[0].MessageTemplate)

	listResp := server.Client().Get("/api/v1/incident-policies").
		ExpectStatus(http.StatusOK).Send(t)
	var list api.IncidentPolicySetListResponse
	listResp.Unmarshal(t, &list)
	require.Len(t, list.PolicySets, 3)
}
