// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	incidentmodel "github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncidentManagement_RequireActiveLicense(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	server.Client().Get("/api/v1/incident-providers").
		ExpectStatus(http.StatusForbidden).Send(t)
}

func TestIncidentManagement_RefusesFreePlan(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithServerOptions(frontend.WithLicenseManager(license.NewFreeTestManager())),
	)
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

// While projects are on, providers are server-wide: only admins change or
// test them, and a developer sees a provider's name and type, never its
// credentials or a preview of them. With projects off, a developer manages
// providers as before.
func TestIncidentManagement_ProjectsAdminOnlyProviders(t *testing.T) {
	t.Parallel()

	for _, projectsOn := range []bool{true, false} {
		t.Run(fmt.Sprintf("ProjectsOn=%t", projectsOn), func(t *testing.T) {
			t.Parallel()

			server := setupWebhookTestServer(t, func(cfg *config.Config) { cfg.ProjectsEnabled = projectsOn })
			adminToken := getWebhookAdminToken(t, server)
			developerToken := createRuntimeProfileUserToken(t, server, adminToken, "developer-user", "developer1", api.UserRoleDeveloper)
			input := api.IncidentProviderInput{}
			require.NoError(t, input.FromIncidentPagerDutyProviderInputEnvelope(api.IncidentPagerDutyProviderInputEnvelope{
				Name:      "PagerDuty",
				Enabled:   true,
				PagerDuty: api.IncidentPagerDutyProviderInput{RoutingKey: new("pagerduty-routing-key")},
			}))
			changeStatus := http.StatusCreated
			if projectsOn {
				changeStatus = http.StatusForbidden
			}
			server.Client().Post("/api/v1/incident-providers", input).
				WithBearerToken(developerToken).ExpectStatus(changeStatus).Send(t)

			var provider api.IncidentProvider
			server.Client().Post("/api/v1/incident-providers", input).
				WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t).Unmarshal(t, &provider)
			listed := server.Client().Get("/api/v1/incident-providers").
				WithBearerToken(developerToken).ExpectStatus(http.StatusOK).Send(t)
			var list api.IncidentProviderListResponse
			listed.Unmarshal(t, &list)
			idx := slices.IndexFunc(list.Providers, func(p api.IncidentProvider) bool { return p.Id == provider.Id })
			require.NotEqual(t, -1, idx)
			got := server.Client().Get("/api/v1/incident-providers/" + provider.Id).
				WithBearerToken(developerToken).ExpectStatus(http.StatusOK).Send(t)
			var fetched api.IncidentProvider
			got.Unmarshal(t, &fetched)
			// Each answer is checked on its own, since the list and the
			// detail endpoint build theirs separately.
			for _, seen := range []api.IncidentProvider{list.Providers[idx], fetched} {
				assert.Equal(t, "PagerDuty", seen.Name)
				assert.Equal(t, api.IncidentProviderTypePagerduty, seen.Type)
				assert.Equal(t, !projectsOn, seen.PagerDuty != nil)
			}
			for _, body := range []string{listed.Body, got.Body} {
				assert.Equal(t, !projectsOn, strings.Contains(body, "routingKeyPreview"), body)
				assert.NotContains(t, body, "pagerduty-routing-key")
			}
			server.Client().Get("/api/v1/incident-providers/"+provider.Id).
				WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &fetched)
			assert.NotNil(t, fetched.PagerDuty)
			if !projectsOn {
				return
			}

			server.Client().Put("/api/v1/incident-providers/"+provider.Id, input).
				WithBearerToken(developerToken).ExpectStatus(http.StatusForbidden).Send(t)
			server.Client().Post("/api/v1/incident-providers/"+provider.Id+"/test", nil).
				WithBearerToken(developerToken).ExpectStatus(http.StatusForbidden).Send(t)
			server.Client().Delete("/api/v1/incident-providers/" + provider.Id).
				WithBearerToken(developerToken).ExpectStatus(http.StatusForbidden).Send(t)
		})
	}
}

// A developer's test send to a provider reports a failed delivery by the
// provider's name and type, since the transport error names the provider's
// webhook URL. An admin still gets the error itself. With projects on, only
// admins test a provider.
func TestIncidentManagement_HideProviderTestErrors(t *testing.T) {
	t.Parallel()

	server := setupWebhookTestServer(t)
	adminToken := getWebhookAdminToken(t, server)
	developerToken := createRuntimeProfileUserToken(t, server, adminToken, "developer-user", "developer1", api.UserRoleDeveloper)
	// Nothing listens on port 1, so the delivery fails in transport.
	input := api.IncidentProviderInput{}
	require.NoError(t, input.FromIncidentSolarWindsProviderInputEnvelope(api.IncidentSolarWindsProviderInputEnvelope{
		Name:    "On-call",
		Type:    api.IncidentSolarWindsProviderInputEnvelopeTypeSolarwindsIncidentResponse,
		Enabled: true,
		SolarWinds: api.IncidentSolarWindsProviderInput{
			WebhookUrl:          new("http://127.0.0.1:1/hooks/secret-token"),
			AllowInsecureHttp:   new(true),
			AllowPrivateNetwork: new(true),
		},
	}))
	var provider api.IncidentProvider
	server.Client().Post("/api/v1/incident-providers", input).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t).Unmarshal(t, &provider)

	for token, hidden := range map[string]bool{developerToken: true, adminToken: false} {
		var resp api.TestIncidentProviderResponse
		server.Client().Post("/api/v1/incident-providers/"+provider.Id+"/test", nil).
			WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &resp)
		require.False(t, resp.Result.Delivered)
		require.NotNil(t, resp.Result.Error)
		message := *resp.Result.Error
		assert.Equal(t, !hidden, strings.Contains(message, "secret-token"), message)
		if hidden {
			assert.Equal(t, `delivery to solarwinds_incident_response provider "On-call" failed; see the server log`, message)
		}
	}
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
