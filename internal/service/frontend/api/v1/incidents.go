// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/api/v1"
	incidentmodel "github.com/dagucloud/dagu/internal/incident"
	"github.com/dagucloud/dagu/internal/service/audit"
	incidentservice "github.com/dagucloud/dagu/internal/service/incident"
)

var errIncidentManagementNotAvailable = &Error{
	HTTPStatus: http.StatusNotFound,
	Code:       api.ErrorCodeNotFound,
	Message:    "Incident management is not available",
}

func (a *API) ListIncidentProviders(ctx context.Context, _ api.ListIncidentProvidersRequestObject) (api.ListIncidentProvidersResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	providers, err := a.incidentService.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	return api.ListIncidentProviders200JSONResponse{
		Providers: toAPIIncidentProviders(providers),
	}, nil
}

func (a *API) CreateIncidentProvider(ctx context.Context, request api.CreateIncidentProviderRequestObject) (api.CreateIncidentProviderResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badIncidentRequest("request body is required")
	}
	provider := incidentProviderFromRequest("", *request.Body)
	saved, err := a.incidentService.SaveProvider(ctx, provider, getCreatorID(ctx))
	if err != nil {
		if incidentRequestError(err) {
			return nil, badIncidentRequest(err.Error())
		}
		return nil, err
	}
	a.logAudit(ctx, audit.CategoryIncident, "incident_provider_create", map[string]any{
		"provider_id": saved.ID,
		"type":        saved.Type,
		"enabled":     saved.Enabled,
	})
	return api.CreateIncidentProvider201JSONResponse(toAPIIncidentProvider(saved)), nil
}

func (a *API) GetIncidentProvider(ctx context.Context, request api.GetIncidentProviderRequestObject) (api.GetIncidentProviderResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	provider, err := a.incidentService.GetProvider(ctx, request.ProviderId)
	if err != nil {
		if errors.Is(err, incidentmodel.ErrProviderNotFound) {
			return nil, incidentNotFound(err.Error())
		}
		return nil, err
	}
	return api.GetIncidentProvider200JSONResponse(toAPIIncidentProvider(provider)), nil
}

func (a *API) UpdateIncidentProvider(ctx context.Context, request api.UpdateIncidentProviderRequestObject) (api.UpdateIncidentProviderResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badIncidentRequest("request body is required")
	}
	if _, err := a.incidentService.GetProvider(ctx, request.ProviderId); err != nil {
		if errors.Is(err, incidentmodel.ErrProviderNotFound) {
			return nil, incidentNotFound(err.Error())
		}
		return nil, err
	}
	provider := incidentProviderFromRequest(request.ProviderId, *request.Body)
	saved, err := a.incidentService.SaveProvider(ctx, provider, getCreatorID(ctx))
	if err != nil {
		switch {
		case errors.Is(err, incidentmodel.ErrProviderNotFound):
			return nil, incidentNotFound(err.Error())
		case incidentRequestError(err):
			return nil, badIncidentRequest(err.Error())
		default:
			return nil, err
		}
	}
	a.logAudit(ctx, audit.CategoryIncident, "incident_provider_update", map[string]any{
		"provider_id": saved.ID,
		"type":        saved.Type,
		"enabled":     saved.Enabled,
	})
	return api.UpdateIncidentProvider200JSONResponse(toAPIIncidentProvider(saved)), nil
}

func (a *API) DeleteIncidentProvider(ctx context.Context, request api.DeleteIncidentProviderRequestObject) (api.DeleteIncidentProviderResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.incidentService.DeleteProvider(ctx, request.ProviderId); err != nil {
		switch {
		case errors.Is(err, incidentmodel.ErrProviderNotFound):
			return nil, incidentNotFound(err.Error())
		case errors.Is(err, incidentmodel.ErrProviderInUse):
			return nil, &Error{
				HTTPStatus: http.StatusConflict,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		default:
			return nil, err
		}
	}
	a.logAudit(ctx, audit.CategoryIncident, "incident_provider_delete", map[string]any{
		"provider_id": request.ProviderId,
	})
	return api.DeleteIncidentProvider204Response{}, nil
}

func (a *API) TestIncidentProvider(ctx context.Context, request api.TestIncidentProviderRequestObject) (api.TestIncidentProviderResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	result, err := a.incidentService.SendProviderTest(ctx, request.ProviderId)
	if err != nil {
		switch {
		case errors.Is(err, incidentmodel.ErrProviderNotFound):
			return nil, incidentNotFound(err.Error())
		case incidentRequestError(err):
			return nil, badIncidentRequest(err.Error())
		default:
			return nil, err
		}
	}
	a.logAudit(ctx, audit.CategoryIncident, "incident_provider_test", map[string]any{
		"provider_id": request.ProviderId,
		"delivered":   result != nil && result.Delivered,
	})
	return api.TestIncidentProvider200JSONResponse{
		Result: toAPITestIncidentProviderResult(result),
	}, nil
}

func (a *API) ListIncidentPolicies(ctx context.Context, _ api.ListIncidentPoliciesRequestObject) (api.ListIncidentPoliciesResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	policySets, err := a.incidentService.ListPolicySets(ctx)
	if err != nil {
		return nil, err
	}
	return api.ListIncidentPolicies200JSONResponse{
		PolicySets: toAPIIncidentPolicySets(policySets),
	}, nil
}

func (a *API) GetGlobalIncidentPolicies(ctx context.Context, _ api.GetGlobalIncidentPoliciesRequestObject) (api.GetGlobalIncidentPoliciesResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	policySet, err := a.incidentService.GetPolicySet(ctx, incidentmodel.PolicyScopeGlobal, "", "")
	if err != nil {
		return nil, err
	}
	return api.GetGlobalIncidentPolicies200JSONResponse(toAPIIncidentPolicySet(policySet)), nil
}

func (a *API) UpdateGlobalIncidentPolicies(ctx context.Context, request api.UpdateGlobalIncidentPoliciesRequestObject) (api.UpdateGlobalIncidentPoliciesResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badIncidentRequest("request body is required")
	}
	policySet := incidentPolicySetFromRequest(incidentmodel.PolicyScopeGlobal, "", "", *request.Body)
	saved, err := a.incidentService.SavePolicySet(ctx, policySet, getCreatorID(ctx))
	if err != nil {
		if respErr := incidentPolicyError(err); respErr != nil {
			return nil, respErr
		}
		return nil, err
	}
	a.logAudit(ctx, audit.CategoryIncident, "incident_policy_set_update", map[string]any{
		"scope":    saved.Scope,
		"policies": len(saved.Policies),
		"enabled":  saved.Enabled,
	})
	return api.UpdateGlobalIncidentPolicies200JSONResponse(toAPIIncidentPolicySet(saved)), nil
}

func (a *API) GetWorkspaceIncidentPolicies(ctx context.Context, request api.GetWorkspaceIncidentPoliciesRequestObject) (api.GetWorkspaceIncidentPoliciesResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	workspaceName, err := a.resolveNotificationRouteWorkspace(ctx, string(request.WorkspaceName))
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return nil, err
	}
	policySet, err := a.incidentService.GetPolicySet(ctx, incidentmodel.PolicyScopeWorkspace, workspaceName, "")
	if err != nil {
		return nil, err
	}
	return api.GetWorkspaceIncidentPolicies200JSONResponse(toAPIIncidentPolicySet(policySet)), nil
}

func (a *API) UpdateWorkspaceIncidentPolicies(ctx context.Context, request api.UpdateWorkspaceIncidentPoliciesRequestObject) (api.UpdateWorkspaceIncidentPoliciesResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badIncidentRequest("request body is required")
	}
	workspaceName, err := a.resolveNotificationRouteWorkspace(ctx, string(request.WorkspaceName))
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceConfigWrite(ctx, workspaceName); err != nil {
		return nil, err
	}
	policySet := incidentPolicySetFromRequest(incidentmodel.PolicyScopeWorkspace, workspaceName, "", *request.Body)
	saved, err := a.incidentService.SavePolicySet(ctx, policySet, getCreatorID(ctx))
	if err != nil {
		if respErr := incidentPolicyError(err); respErr != nil {
			return nil, respErr
		}
		return nil, err
	}
	a.logAudit(ctx, audit.CategoryIncident, "incident_policy_set_update", map[string]any{
		"scope":     saved.Scope,
		"workspace": saved.Workspace,
		"policies":  len(saved.Policies),
		"enabled":   saved.Enabled,
	})
	return api.UpdateWorkspaceIncidentPolicies200JSONResponse(toAPIIncidentPolicySet(saved)), nil
}

func (a *API) GetDAGIncidents(ctx context.Context, request api.GetDAGIncidentsRequestObject) (api.GetDAGIncidentsResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.ensureDAGExists(ctx, string(request.FileName)); err != nil {
		return nil, err
	}
	policySet, err := a.incidentService.GetPolicySet(ctx, incidentmodel.PolicyScopeDAG, "", string(request.FileName))
	if err != nil {
		return nil, err
	}
	return api.GetDAGIncidents200JSONResponse(toAPIIncidentPolicySet(policySet)), nil
}

func (a *API) UpdateDAGIncidents(ctx context.Context, request api.UpdateDAGIncidentsRequestObject) (api.UpdateDAGIncidentsResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badIncidentRequest("request body is required")
	}
	if err := a.ensureDAGExists(ctx, string(request.FileName)); err != nil {
		return nil, err
	}
	policySet := incidentPolicySetFromRequest(incidentmodel.PolicyScopeDAG, "", string(request.FileName), *request.Body)
	saved, err := a.incidentService.SavePolicySet(ctx, policySet, getCreatorID(ctx))
	if err != nil {
		if respErr := incidentPolicyError(err); respErr != nil {
			return nil, respErr
		}
		return nil, err
	}
	a.logAudit(ctx, audit.CategoryIncident, "incident_policy_set_update", map[string]any{
		"scope":     saved.Scope,
		"dag_name":  saved.DAGName,
		"policies":  len(saved.Policies),
		"enabled":   saved.Enabled,
		"inherited": saved.InheritParent,
	})
	return api.UpdateDAGIncidents200JSONResponse(toAPIIncidentPolicySet(saved)), nil
}

func (a *API) DeleteDAGIncidents(ctx context.Context, request api.DeleteDAGIncidentsRequestObject) (api.DeleteDAGIncidentsResponseObject, error) {
	if err := a.requireIncidentManagement(ctx); err != nil {
		return nil, err
	}
	if err := a.ensureDAGExists(ctx, string(request.FileName)); err != nil {
		return nil, err
	}
	if err := a.incidentService.DeletePolicySet(ctx, incidentmodel.PolicyScopeDAG, "", string(request.FileName)); err != nil {
		if errors.Is(err, incidentmodel.ErrPolicySetNotFound) {
			return nil, incidentNotFound(fmt.Sprintf("no incident policy override configured for DAG %s", request.FileName))
		}
		return nil, err
	}
	a.logAudit(ctx, audit.CategoryIncident, "incident_policy_set_delete", map[string]any{
		"scope":    incidentmodel.PolicyScopeDAG,
		"dag_name": request.FileName,
	})
	return api.DeleteDAGIncidents204Response{}, nil
}

func (a *API) requireIncidentManagement(ctx context.Context) error {
	if a.incidentService == nil {
		return errIncidentManagementNotAvailable
	}
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return err
	}
	return a.requireLicensedIncidentManagement()
}

func incidentRequestError(err error) bool {
	return errors.Is(err, incidentmodel.ErrInvalidProvider) ||
		errors.Is(err, incidentmodel.ErrUnsupportedProvider) ||
		errors.Is(err, incidentmodel.ErrSecretStoreMissing) ||
		errors.Is(err, incidentmodel.ErrInvalidPolicySet)
}

func incidentPolicyError(err error) *Error {
	switch {
	case errors.Is(err, incidentmodel.ErrProviderNotFound):
		return incidentNotFound(err.Error())
	case incidentRequestError(err):
		return badIncidentRequest(err.Error())
	default:
		return nil
	}
}

func badIncidentRequest(message string) *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
	}
}

func incidentNotFound(message string) *Error {
	return &Error{
		HTTPStatus: http.StatusNotFound,
		Code:       api.ErrorCodeNotFound,
		Message:    message,
	}
}

func incidentProviderFromRequest(id string, input api.IncidentProviderInput) *incidentmodel.Provider {
	provider := &incidentmodel.Provider{
		ID:      id,
		Name:    input.Name,
		Type:    incidentmodel.ProviderType(input.Type),
		Enabled: input.Enabled,
	}
	switch provider.Type {
	case incidentmodel.ProviderPagerDuty:
		cfg := &incidentmodel.PagerDutyProvider{}
		if input.PagerDuty != nil {
			cfg.RoutingKey = valueOf(input.PagerDuty.RoutingKey)
			cfg.ClearRoutingKey = valueOf(input.PagerDuty.ClearRoutingKey)
		}
		provider.PagerDuty = cfg
	case incidentmodel.ProviderSolarWindsIncidentResponse:
		cfg := &incidentmodel.SolarWindsProvider{}
		if input.SolarWinds != nil {
			cfg.WebhookURL = valueOf(input.SolarWinds.WebhookUrl)
			cfg.AllowInsecureHTTP = valueOf(input.SolarWinds.AllowInsecureHttp)
			cfg.AllowPrivateNetwork = valueOf(input.SolarWinds.AllowPrivateNetwork)
			cfg.ClearWebhookURL = valueOf(input.SolarWinds.ClearWebhookUrl)
		}
		provider.SolarWinds = cfg
	}
	return provider
}

func incidentPolicySetFromRequest(scope incidentmodel.PolicyScope, workspaceName, dagName string, input api.IncidentPolicySetInput) *incidentmodel.PolicySet {
	policySet := &incidentmodel.PolicySet{
		Scope:         scope,
		Workspace:     workspaceName,
		DAGName:       dagName,
		Enabled:       input.Enabled,
		InheritParent: input.InheritParent,
		Policies:      make([]incidentmodel.Policy, 0, len(input.Policies)),
	}
	for _, policy := range input.Policies {
		policySet.Policies = append(policySet.Policies, incidentPolicyFromRequest(policy))
	}
	return policySet
}

func incidentPolicyFromRequest(input api.IncidentPolicyInput) incidentmodel.Policy {
	return incidentmodel.Policy{
		ID:                  valueOf(input.Id),
		ProviderID:          input.ProviderId,
		Enabled:             input.Enabled,
		Severity:            incidentmodel.Severity(input.Severity),
		ResolveOnRecovery:   input.ResolveOnRecovery,
		DedupKeyTemplate:    valueOf(input.DedupKeyTemplate),
		MessageTemplate:     valueOf(input.MessageTemplate),
		DescriptionTemplate: valueOf(input.DescriptionTemplate),
	}
}

func toAPIIncidentProviders(providers []*incidentmodel.Provider) []api.IncidentProvider {
	out := make([]api.IncidentProvider, 0, len(providers))
	for _, provider := range providers {
		out = append(out, toAPIIncidentProvider(provider))
	}
	return out
}

func toAPIIncidentProvider(provider *incidentmodel.Provider) api.IncidentProvider {
	if provider == nil {
		return api.IncidentProvider{}
	}
	pub := provider.ToPublic()
	result := api.IncidentProvider{
		Id:        pub.ID,
		Name:      pub.Name,
		Type:      api.IncidentProviderType(pub.Type),
		Enabled:   pub.Enabled,
		CreatedAt: pub.CreatedAt,
		UpdatedAt: pub.UpdatedAt,
	}
	if pub.UpdatedBy != "" {
		result.UpdatedBy = ptrOf(pub.UpdatedBy)
	}
	if pub.PagerDuty != nil {
		result.PagerDuty = &api.IncidentPagerDutyProvider{
			RoutingKeyConfigured: pub.PagerDuty.RoutingKeyConfigured,
		}
		if pub.PagerDuty.RoutingKeyPreview != "" {
			result.PagerDuty.RoutingKeyPreview = ptrOf(pub.PagerDuty.RoutingKeyPreview)
		}
	}
	if pub.SolarWinds != nil {
		result.SolarWinds = &api.IncidentSolarWindsProvider{
			WebhookUrlConfigured: pub.SolarWinds.WebhookURLConfigured,
			AllowInsecureHttp:    ptrOf(pub.SolarWinds.AllowInsecureHTTP),
			AllowPrivateNetwork:  ptrOf(pub.SolarWinds.AllowPrivateNetwork),
		}
		if pub.SolarWinds.WebhookURLPreview != "" {
			result.SolarWinds.WebhookUrlPreview = ptrOf(pub.SolarWinds.WebhookURLPreview)
		}
	}
	return result
}

func toAPIIncidentPolicySets(policySets []*incidentmodel.PolicySet) []api.IncidentPolicySet {
	out := make([]api.IncidentPolicySet, 0, len(policySets))
	for _, policySet := range policySets {
		out = append(out, toAPIIncidentPolicySet(policySet))
	}
	return out
}

func toAPIIncidentPolicySet(policySet *incidentmodel.PolicySet) api.IncidentPolicySet {
	if policySet == nil {
		return api.IncidentPolicySet{
			Scope:         api.IncidentPolicyScopeGlobal,
			Enabled:       true,
			InheritParent: false,
			Policies:      []api.IncidentPolicy{},
		}
	}
	result := api.IncidentPolicySet{
		Scope:         api.IncidentPolicyScope(policySet.Scope),
		Enabled:       policySet.Enabled,
		InheritParent: policySet.InheritParent,
		Policies:      make([]api.IncidentPolicy, 0, len(policySet.Policies)),
	}
	if policySet.ID != "" {
		result.Id = ptrOf(policySet.ID)
	}
	if policySet.Workspace != "" {
		result.Workspace = ptrOf(policySet.Workspace)
	}
	if policySet.DAGName != "" {
		result.DagName = ptrOf(policySet.DAGName)
	}
	if !policySet.CreatedAt.IsZero() {
		result.CreatedAt = ptrOf(policySet.CreatedAt)
	}
	if !policySet.UpdatedAt.IsZero() {
		result.UpdatedAt = ptrOf(policySet.UpdatedAt)
	}
	if policySet.UpdatedBy != "" {
		result.UpdatedBy = ptrOf(policySet.UpdatedBy)
	}
	for _, policy := range policySet.Policies {
		result.Policies = append(result.Policies, toAPIIncidentPolicy(policy))
	}
	return result
}

func toAPIIncidentPolicy(policy incidentmodel.Policy) api.IncidentPolicy {
	return api.IncidentPolicy{
		Id:                  policy.ID,
		ProviderId:          policy.ProviderID,
		Enabled:             policy.Enabled,
		Severity:            api.IncidentSeverity(policy.Severity),
		ResolveOnRecovery:   policy.ResolveOnRecovery,
		DedupKeyTemplate:    policy.DedupKeyTemplate,
		MessageTemplate:     policy.MessageTemplate,
		DescriptionTemplate: policy.DescriptionTemplate,
	}
}

func toAPITestIncidentProviderResult(result *incidentservice.TestResult) api.TestIncidentProviderResult {
	if result == nil {
		return api.TestIncidentProviderResult{}
	}
	out := api.TestIncidentProviderResult{
		ProviderId:   result.ProviderID,
		ProviderName: result.ProviderName,
		ProviderType: api.IncidentProviderType(result.ProviderType),
		Delivered:    result.Delivered,
	}
	if result.Error != "" {
		out.Error = ptrOf(result.Error)
	}
	return out
}
