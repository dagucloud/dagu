// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/service/audit"
)

const (
	auditActionAgentConfigUpdate = "agent_config_update"
	auditFieldEnabled            = "enabled"
	auditFieldDefaultModelID     = "default_model_id"
	auditFieldToolPolicy         = "tool_policy"
)

var (
	// ErrAgentConfigNotAvailable is returned when agent config management is disabled.
	ErrAgentConfigNotAvailable = &Error{
		Code:       api.ErrorCodeForbidden,
		Message:    "Agent configuration management is not available",
		HTTPStatus: http.StatusForbidden,
	}

	// ErrFailedToLoadAgentConfig is returned when reading config fails.
	ErrFailedToLoadAgentConfig = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to load agent configuration",
		HTTPStatus: http.StatusInternalServerError,
	}

	// ErrFailedToSaveAgentConfig is returned when writing config fails.
	ErrFailedToSaveAgentConfig = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to save agent configuration",
		HTTPStatus: http.StatusInternalServerError,
	}

	// ErrInvalidRequestBody is returned when the request body is missing or invalid.
	ErrInvalidRequestBody = &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    "Invalid request body",
		HTTPStatus: http.StatusBadRequest,
	}

	// ErrInvalidToolPolicy is returned when tool policy validation fails.
	ErrInvalidToolPolicy = &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    "Invalid tool policy configuration",
		HTTPStatus: http.StatusBadRequest,
	}
)

// GetAgentConfig returns the current agent configuration. Requires admin role.
func (a *API) GetAgentConfig(ctx context.Context, _ api.GetAgentConfigRequestObject) (api.GetAgentConfigResponseObject, error) {
	if err := a.requireAgentConfigManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	cfg, err := a.agentConfigStore.Load(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to load agent config", tag.Error(err))
		return nil, ErrFailedToLoadAgentConfig
	}

	return api.GetAgentConfig200JSONResponse(toAgentConfigResponse(cfg)), nil
}

// UpdateAgentConfig updates the agent configuration. Requires admin role.
func (a *API) UpdateAgentConfig(ctx context.Context, request api.UpdateAgentConfigRequestObject) (api.UpdateAgentConfigResponseObject, error) {
	if err := a.requireAgentConfigManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}

	cfg, err := a.agentConfigStore.Load(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to load agent config", tag.Error(err))
		return nil, ErrFailedToLoadAgentConfig
	}

	if err := applyAgentConfigUpdates(cfg, request.Body); err != nil {
		return nil, invalidAgentConfigError(err)
	}

	// Validate that the selected soul exists (only when explicitly changed).
	if request.Body.SelectedSoulId != nil && cfg.SelectedSoulID != "" && a.agentSoulStore != nil {
		if _, err := a.agentSoulStore.GetByID(ctx, cfg.SelectedSoulID); err != nil {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Selected soul not found: " + cfg.SelectedSoulID,
				HTTPStatus: http.StatusBadRequest,
			}
		}
	}

	if err := a.agentConfigStore.Save(ctx, cfg); err != nil {
		logger.Error(ctx, "Failed to save agent config", tag.Error(err))
		return nil, ErrFailedToSaveAgentConfig
	}

	a.logAudit(ctx, audit.CategoryAgent, auditActionAgentConfigUpdate, buildAgentConfigChanges(request.Body))

	return api.UpdateAgentConfig200JSONResponse(toAgentConfigResponse(cfg)), nil
}

// ListAgentTools returns metadata for all registered agent tools.
func (a *API) ListAgentTools(ctx context.Context, _ api.ListAgentToolsRequestObject) (api.ListAgentToolsResponseObject, error) {
	if err := a.requireAgentConfigManagement(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	regs := agent.RegisteredTools()
	tools := make([]api.AgentToolInfo, len(regs))
	for i, reg := range regs {
		tools[i] = api.AgentToolInfo{
			Name:        reg.Name,
			Label:       reg.Label,
			Description: reg.Description,
		}
	}

	return api.ListAgentTools200JSONResponse{Tools: tools}, nil
}

func (a *API) requireAgentConfigManagement() error {
	if a.agentConfigStore == nil {
		return ErrAgentConfigNotAvailable
	}
	return nil
}

func invalidAgentConfigError(err error) *Error {
	message := "Invalid agent configuration"
	if err != nil {
		message += ": " + err.Error()
	}
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

func toAgentConfigResponse(cfg *agent.Config) api.AgentConfigResponse {
	resp := api.AgentConfigResponse{
		Enabled:        &cfg.Enabled,
		DefaultModelId: ptrOf(cfg.DefaultModelID),
		ToolPolicy:     toAPIToolPolicy(cfg.ToolPolicy),
		SelectedSoulId: ptrOf(cfg.SelectedSoulID),
	}
	if cfg.WebSearch != nil {
		resp.WebSearch = &api.AgentWebSearchConfig{
			Enabled: &cfg.WebSearch.Enabled,
			MaxUses: cfg.WebSearch.MaxUses,
		}
	}
	if cfg.WebTools != nil {
		resp.WebTools = toAPIWebToolsConfig(cfg.WebTools)
	}
	return resp
}

// applyAgentConfigUpdates applies non-nil fields from the update request to the agent configuration.
func applyAgentConfigUpdates(cfg *agent.Config, update *api.UpdateAgentConfigRequest) error {
	if update.Enabled != nil {
		cfg.Enabled = *update.Enabled
	}
	if update.DefaultModelId != nil {
		cfg.DefaultModelID = *update.DefaultModelId
	}
	if update.ToolPolicy != nil {
		policy := toInternalToolPolicy(*update.ToolPolicy)
		if err := agent.ValidateToolPolicy(policy); err != nil {
			return err
		}
		cfg.ToolPolicy = agent.ResolveToolPolicy(policy)
	}
	if update.SelectedSoulId != nil {
		cfg.SelectedSoulID = *update.SelectedSoulId
	}
	if update.WebSearch != nil {
		ws := &agent.WebSearchConfig{}
		if update.WebSearch.Enabled != nil {
			ws.Enabled = *update.WebSearch.Enabled
		}
		ws.MaxUses = update.WebSearch.MaxUses
		cfg.WebSearch = ws
	}
	if update.WebTools != nil {
		if err := applyWebToolsUpdate(cfg, update.WebTools); err != nil {
			return err
		}
	}
	return nil
}

// buildAgentConfigChanges constructs a map of changed fields for audit logging.
func buildAgentConfigChanges(update *api.UpdateAgentConfigRequest) map[string]any {
	changes := make(map[string]any)
	if update.Enabled != nil {
		changes[auditFieldEnabled] = *update.Enabled
	}
	if update.DefaultModelId != nil {
		changes[auditFieldDefaultModelID] = *update.DefaultModelId
	}
	if update.ToolPolicy != nil {
		changes[auditFieldToolPolicy] = update.ToolPolicy
	}
	if update.SelectedSoulId != nil {
		changes["selected_soul_id"] = *update.SelectedSoulId
	}
	if update.WebSearch != nil {
		changes["web_search"] = update.WebSearch
	}
	if update.WebTools != nil {
		changes["web_tools"] = sanitizeWebToolsForAudit(update.WebTools)
	}
	return changes
}

func toAPIWebToolsConfig(cfg *agent.WebToolsConfig) *api.AgentWebToolsConfig {
	if cfg == nil {
		return nil
	}
	resolved := agent.ResolveWebToolsConfig(*cfg)
	enabled := cfg.Enabled
	backend := api.AgentWebToolsBackend(resolved.Backend)
	resp := &api.AgentWebToolsConfig{
		Enabled: &enabled,
		Backend: &backend,
	}
	tavilyCfg := agent.TavilyWebToolsConfig{}
	if cfg.Tavily != nil {
		tavilyCfg = *cfg.Tavily
	}
	apiKeyConfigured := strings.TrimSpace(tavilyCfg.APIKey) != ""
	tavily := &api.AgentTavilyWebToolsConfig{
		ApiKeyConfigured: &apiKeyConfigured,
	}
	if tavilyCfg.BaseURL != "" {
		tavily.BaseUrl = ptrOf(tavilyCfg.BaseURL)
	}
	if tavilyCfg.MaxResults > 0 {
		tavily.MaxResults = ptrOf(tavilyCfg.MaxResults)
	}
	if tavilyCfg.SearchDepth != "" {
		searchDepth := api.AgentTavilyWebToolsConfigSearchDepth(resolved.Tavily.SearchDepth)
		tavily.SearchDepth = &searchDepth
	}
	resp.Tavily = tavily
	return resp
}

func applyWebToolsUpdate(cfg *agent.Config, update *api.AgentWebToolsConfig) error {
	next := agent.WebToolsConfig{}
	if cfg.WebTools != nil {
		next = *cfg.WebTools
	}
	if update.Enabled != nil {
		next.Enabled = *update.Enabled
	}
	if update.Backend != nil {
		next.Backend = agent.WebToolsBackend(*update.Backend)
	}
	if next.Enabled && next.Backend == "" {
		next.Backend = agent.WebToolsBackendTavily
	}
	if update.Tavily != nil {
		if next.Tavily == nil {
			next.Tavily = &agent.TavilyWebToolsConfig{}
		}
		if clear := update.Tavily.ClearApiKey; clear != nil && *clear {
			next.Tavily.APIKey = ""
		}
		if update.Tavily.ApiKey != nil {
			apiKey := strings.TrimSpace(*update.Tavily.ApiKey)
			if apiKey != "" {
				next.Tavily.APIKey = apiKey
			}
		}
		if update.Tavily.BaseUrl != nil {
			baseURL, err := agent.ValidateTavilyBaseURL(*update.Tavily.BaseUrl)
			if err != nil {
				return fmt.Errorf("webTools.tavily.baseUrl %w", err)
			}
			next.Tavily.BaseURL = baseURL
		}
		if update.Tavily.MaxResults != nil {
			next.Tavily.MaxResults = *update.Tavily.MaxResults
		}
		if update.Tavily.SearchDepth != nil {
			next.Tavily.SearchDepth = strings.TrimSpace(string(*update.Tavily.SearchDepth))
		}
	}
	if err := agent.ValidateWebToolsConfig(next); err != nil {
		return err
	}
	cfg.WebTools = &next
	return nil
}

func sanitizeWebToolsForAudit(update *api.AgentWebToolsConfig) map[string]any {
	out := map[string]any{}
	if update == nil {
		return out
	}
	if update.Enabled != nil {
		out["enabled"] = *update.Enabled
	}
	if update.Backend != nil {
		out["backend"] = *update.Backend
	}
	if update.Tavily != nil {
		tavily := map[string]any{}
		if update.Tavily.ApiKey != nil && strings.TrimSpace(*update.Tavily.ApiKey) != "" {
			tavily["api_key_provided"] = true
		}
		if update.Tavily.ClearApiKey != nil {
			tavily["clear_api_key"] = *update.Tavily.ClearApiKey
		}
		if update.Tavily.BaseUrl != nil {
			tavily["base_url"] = *update.Tavily.BaseUrl
		}
		if update.Tavily.MaxResults != nil {
			tavily["max_results"] = *update.Tavily.MaxResults
		}
		if update.Tavily.SearchDepth != nil {
			tavily["search_depth"] = *update.Tavily.SearchDepth
		}
		out["tavily"] = tavily
	}
	return out
}

func toAPIToolPolicy(policy agent.ToolPolicyConfig) *api.AgentToolPolicy {
	resolved := agent.ResolveToolPolicy(policy)
	tools := make(map[string]bool, len(resolved.Tools))
	maps.Copy(tools, resolved.Tools)

	rules := make([]api.AgentBashRule, 0, len(resolved.Bash.Rules))
	for _, rule := range resolved.Bash.Rules {
		r := api.AgentBashRule{
			Name:    ptrOf(rule.Name),
			Pattern: rule.Pattern,
			Action:  api.AgentBashRuleAction(rule.Action),
		}
		if rule.Enabled != nil {
			r.Enabled = rule.Enabled
		}
		rules = append(rules, r)
	}

	return &api.AgentToolPolicy{
		Tools: &tools,
		Bash: &api.AgentBashPolicy{
			Rules:           &rules,
			DefaultBehavior: (*api.AgentBashPolicyDefaultBehavior)(&resolved.Bash.DefaultBehavior),
			DenyBehavior:    (*api.AgentBashPolicyDenyBehavior)(&resolved.Bash.DenyBehavior),
		},
	}
}

func toInternalToolPolicy(policy api.AgentToolPolicy) agent.ToolPolicyConfig {
	out := agent.ToolPolicyConfig{
		Tools: map[string]bool{},
	}

	if policy.Tools != nil {
		maps.Copy(out.Tools, *policy.Tools)
	}

	if policy.Bash == nil {
		return out
	}

	if policy.Bash.DefaultBehavior != nil {
		out.Bash.DefaultBehavior = agent.BashDefaultBehavior(*policy.Bash.DefaultBehavior)
	}
	if policy.Bash.DenyBehavior != nil {
		out.Bash.DenyBehavior = agent.BashDenyBehavior(*policy.Bash.DenyBehavior)
	}
	if policy.Bash.Rules != nil {
		out.Bash.Rules = make([]agent.BashRule, 0, len(*policy.Bash.Rules))
		for _, rule := range *policy.Bash.Rules {
			r := agent.BashRule{
				Pattern: rule.Pattern,
				Action:  agent.BashRuleAction(rule.Action),
			}
			if rule.Name != nil {
				r.Name = *rule.Name
			}
			if rule.Enabled != nil {
				r.Enabled = rule.Enabled
			}
			out.Bash.Rules = append(out.Bash.Rules, r)
		}
	}

	return out
}
