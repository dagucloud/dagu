// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incident

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/workspace"
)

type ProviderType string

const (
	ProviderPagerDuty                  ProviderType = "pagerduty"
	ProviderSolarWindsIncidentResponse ProviderType = "solarwinds_incident_response"
	PagerDutyEventsAPIEndpoint                      = "https://events.pagerduty.com/v2/enqueue"
	DefaultDedupKeyTemplate                         = "dagu:workspace:{{workspace}}:dag:{{dag.name}}:failure"
	DefaultMessageTemplate                          = "Dagu DAG {{dag.name}} failed"
	DefaultDescriptionTemplate                      = "Run {{run.id}} finished with status {{run.status}}.\n{{run.link}}\n{{run.error}}"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityError    Severity = "error"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

type PolicyScope string

const (
	PolicyScopeGlobal    PolicyScope = "global"
	PolicyScopeWorkspace PolicyScope = "workspace"
	PolicyScopeDAG       PolicyScope = "dag"
)

type IncidentStatus string

const (
	IncidentStatusOpen     IncidentStatus = "open"
	IncidentStatusResolved IncidentStatus = "resolved"
)

var (
	ErrInvalidProvider     = errors.New("invalid incident provider")
	ErrProviderNotFound    = errors.New("incident provider not found")
	ErrProviderInUse       = errors.New("incident provider is in use")
	ErrInvalidPolicySet    = errors.New("invalid incident policy set")
	ErrPolicySetNotFound   = errors.New("incident policy set not found")
	ErrPolicyNotFound      = errors.New("incident policy not found")
	ErrUnsupportedProvider = errors.New("unsupported incident provider")
	ErrSecretStoreMissing  = errors.New("incident secret store is not configured")
)

type Provider struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Type    ProviderType `json:"type"`
	Enabled bool         `json:"enabled"`

	PagerDuty  *PagerDutyProvider  `json:"pagerDuty,omitempty"`
	SolarWinds *SolarWindsProvider `json:"solarWinds,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
}

type PagerDutyProvider struct {
	RoutingKey      string `json:"routingKey,omitempty"`
	ClearRoutingKey bool   `json:"-"`
}

type SolarWindsProvider struct {
	WebhookURL          string `json:"webhookUrl,omitempty"`
	AllowInsecureHTTP   bool   `json:"allowInsecureHttp,omitempty"`
	AllowPrivateNetwork bool   `json:"allowPrivateNetwork,omitempty"`
	ClearWebhookURL     bool   `json:"-"`
}

type PolicySet struct {
	ID            string      `json:"id"`
	Scope         PolicyScope `json:"scope"`
	Workspace     string      `json:"workspace,omitempty"`
	DAGName       string      `json:"dagName,omitempty"`
	Enabled       bool        `json:"enabled"`
	InheritParent bool        `json:"inheritParent"`
	Policies      []Policy    `json:"policies"`
	CreatedAt     time.Time   `json:"createdAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
	UpdatedBy     string      `json:"updatedBy,omitempty"`
}

type Policy struct {
	ID                  string   `json:"id"`
	ProviderID          string   `json:"providerId"`
	Enabled             bool     `json:"enabled"`
	Severity            Severity `json:"severity"`
	ResolveOnRecovery   bool     `json:"resolveOnRecovery"`
	DedupKeyTemplate    string   `json:"dedupKeyTemplate,omitempty"`
	MessageTemplate     string   `json:"messageTemplate,omitempty"`
	DescriptionTemplate string   `json:"descriptionTemplate,omitempty"`
}

type IncidentState struct {
	ID            string         `json:"id"`
	ProviderID    string         `json:"providerId"`
	PolicyID      string         `json:"policyId"`
	Workspace     string         `json:"workspace,omitempty"`
	DAGName       string         `json:"dagName"`
	DedupKey      string         `json:"dedupKey"`
	Status        IncidentStatus `json:"status"`
	ExternalID    string         `json:"externalId,omitempty"`
	LastRequestID string         `json:"lastRequestId,omitempty"`
	LastEventID   string         `json:"lastEventId,omitempty"`
	OpenedAt      time.Time      `json:"openedAt"`
	ResolvedAt    time.Time      `json:"resolvedAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

type PublicProvider struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Type    ProviderType `json:"type"`
	Enabled bool         `json:"enabled"`

	PagerDuty  *PublicPagerDutyProvider  `json:"pagerDuty,omitempty"`
	SolarWinds *PublicSolarWindsProvider `json:"solarWinds,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
}

type PublicPagerDutyProvider struct {
	RoutingKeyConfigured bool   `json:"routingKeyConfigured"`
	RoutingKeyPreview    string `json:"routingKeyPreview,omitempty"`
}

type PublicSolarWindsProvider struct {
	WebhookURLConfigured bool   `json:"webhookUrlConfigured"`
	WebhookURLPreview    string `json:"webhookUrlPreview,omitempty"`
	AllowInsecureHTTP    bool   `json:"allowInsecureHttp"`
	AllowPrivateNetwork  bool   `json:"allowPrivateNetwork"`
}

type Store interface {
	SaveProvider(ctx context.Context, provider *Provider) error
	GetProvider(ctx context.Context, providerID string) (*Provider, error)
	ListProviders(ctx context.Context) ([]*Provider, error)
	DeleteProvider(ctx context.Context, providerID string) error

	SavePolicySet(ctx context.Context, policySet *PolicySet) error
	GetPolicySet(ctx context.Context, scope PolicyScope, workspace, dagName string) (*PolicySet, error)
	ListPolicySets(ctx context.Context) ([]*PolicySet, error)
	DeletePolicySet(ctx context.Context, scope PolicyScope, workspace, dagName string) error

	SaveState(ctx context.Context, state *IncidentState) error
	GetState(ctx context.Context, providerID, dedupKey string) (*IncidentState, error)
	ListOpenStatesByDAG(ctx context.Context, dagName string) ([]*IncidentState, error)
	DeleteState(ctx context.Context, providerID, dedupKey string) error
}

func NormalizeProvider(provider *Provider, updatedBy string) (*Provider, error) {
	if provider == nil {
		return nil, fmt.Errorf("%w: provider is nil", ErrInvalidProvider)
	}
	provider.ID = strings.TrimSpace(provider.ID)
	if provider.ID == "" {
		provider.ID = uuid.New().String()
	}
	provider.Name = strings.TrimSpace(provider.Name)
	if provider.Name == "" {
		return nil, fmt.Errorf("%w: provider name is required", ErrInvalidProvider)
	}
	switch provider.Type {
	case ProviderPagerDuty:
		if provider.PagerDuty == nil {
			return nil, fmt.Errorf("%w: pagerduty config is required", ErrInvalidProvider)
		}
		provider.PagerDuty.RoutingKey = strings.TrimSpace(provider.PagerDuty.RoutingKey)
		if provider.Enabled && provider.PagerDuty.RoutingKey == "" {
			return nil, fmt.Errorf("%w: pagerduty routing key is required", ErrInvalidProvider)
		}
		provider.SolarWinds = nil
	case ProviderSolarWindsIncidentResponse:
		if provider.SolarWinds == nil {
			return nil, fmt.Errorf("%w: solarwinds config is required", ErrInvalidProvider)
		}
		provider.SolarWinds.WebhookURL = strings.TrimSpace(provider.SolarWinds.WebhookURL)
		if provider.Enabled && provider.SolarWinds.WebhookURL == "" {
			return nil, fmt.Errorf("%w: solarwinds webhook url is required", ErrInvalidProvider)
		}
		if provider.SolarWinds.WebhookURL != "" {
			if err := ValidateWebhookURL(provider.SolarWinds.WebhookURL, provider.SolarWinds.AllowInsecureHTTP, provider.SolarWinds.AllowPrivateNetwork); err != nil {
				return nil, err
			}
		}
		provider.PagerDuty = nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProvider, provider.Type)
	}
	now := time.Now().UTC()
	if provider.CreatedAt.IsZero() {
		provider.CreatedAt = now
	}
	provider.UpdatedAt = now
	provider.UpdatedBy = updatedBy
	return provider, nil
}

func NormalizePolicySet(policySet *PolicySet, updatedBy string) (*PolicySet, error) {
	if policySet == nil {
		return nil, fmt.Errorf("%w: policy set is nil", ErrInvalidPolicySet)
	}
	policySet.Scope = PolicyScope(strings.TrimSpace(string(policySet.Scope)))
	policySet.Workspace = strings.TrimSpace(policySet.Workspace)
	policySet.DAGName = strings.TrimSpace(policySet.DAGName)
	switch policySet.Scope {
	case PolicyScopeGlobal:
		policySet.Workspace = ""
		policySet.DAGName = ""
		policySet.InheritParent = false
	case PolicyScopeWorkspace:
		policySet.DAGName = ""
		if err := workspace.ValidateName(policySet.Workspace); err != nil {
			return nil, fmt.Errorf("%w: invalid workspace scope: %w", ErrInvalidPolicySet, err)
		}
	case PolicyScopeDAG:
		if policySet.DAGName == "" {
			return nil, fmt.Errorf("%w: dagName is required", ErrInvalidPolicySet)
		}
	default:
		return nil, fmt.Errorf("%w: invalid incident policy scope", ErrInvalidPolicySet)
	}
	if policySet.ID == "" {
		policySet.ID = uuid.New().String()
	}
	seenProviders := map[string]struct{}{}
	for i := range policySet.Policies {
		if err := normalizePolicy(&policySet.Policies[i]); err != nil {
			return nil, err
		}
		if _, ok := seenProviders[policySet.Policies[i].ProviderID]; ok {
			return nil, fmt.Errorf("%w: duplicate incident provider policy %s", ErrInvalidPolicySet, policySet.Policies[i].ProviderID)
		}
		seenProviders[policySet.Policies[i].ProviderID] = struct{}{}
	}
	now := time.Now().UTC()
	if policySet.CreatedAt.IsZero() {
		policySet.CreatedAt = now
	}
	policySet.UpdatedAt = now
	policySet.UpdatedBy = updatedBy
	return policySet, nil
}

func normalizePolicy(policy *Policy) error {
	policy.ID = strings.TrimSpace(policy.ID)
	if policy.ID == "" {
		policy.ID = uuid.New().String()
	}
	policy.ProviderID = strings.TrimSpace(policy.ProviderID)
	if policy.ProviderID == "" {
		return fmt.Errorf("%w: provider id is required", ErrInvalidPolicySet)
	}
	if policy.Severity == "" {
		policy.Severity = SeverityError
	}
	if !slices.Contains([]Severity{SeverityCritical, SeverityError, SeverityWarning, SeverityInfo}, policy.Severity) {
		return fmt.Errorf("%w: invalid severity %s", ErrInvalidPolicySet, policy.Severity)
	}
	policy.DedupKeyTemplate = defaultIfBlank(policy.DedupKeyTemplate, DefaultDedupKeyTemplate)
	policy.MessageTemplate = defaultIfBlank(policy.MessageTemplate, DefaultMessageTemplate)
	policy.DescriptionTemplate = defaultIfBlank(policy.DescriptionTemplate, DefaultDescriptionTemplate)
	return nil
}

func NormalizeState(state *IncidentState) (*IncidentState, error) {
	if state == nil {
		return nil, errors.New("incident state is nil")
	}
	state.ID = strings.TrimSpace(state.ID)
	if state.ID == "" {
		state.ID = uuid.New().String()
	}
	state.ProviderID = strings.TrimSpace(state.ProviderID)
	state.PolicyID = strings.TrimSpace(state.PolicyID)
	state.Workspace = strings.TrimSpace(state.Workspace)
	state.DAGName = strings.TrimSpace(state.DAGName)
	state.DedupKey = strings.TrimSpace(state.DedupKey)
	if state.ProviderID == "" || state.PolicyID == "" || state.DAGName == "" || state.DedupKey == "" {
		return nil, errors.New("incident state requires provider id, policy id, dag name, and dedup key")
	}
	switch state.Status {
	case IncidentStatusOpen, IncidentStatusResolved:
	default:
		return nil, fmt.Errorf("invalid incident status %q", state.Status)
	}
	now := time.Now().UTC()
	if state.OpenedAt.IsZero() {
		state.OpenedAt = now
	}
	state.UpdatedAt = now
	return state, nil
}

func PreserveProviderSecrets(next, existing *Provider) {
	if next == nil || existing == nil || next.Type != existing.Type {
		return
	}
	switch next.Type {
	case ProviderPagerDuty:
		if next.PagerDuty == nil {
			return
		}
		if next.PagerDuty.ClearRoutingKey {
			next.PagerDuty.RoutingKey = ""
			return
		}
		if next.PagerDuty.RoutingKey == "" && existing.PagerDuty != nil {
			next.PagerDuty.RoutingKey = existing.PagerDuty.RoutingKey
		}
	case ProviderSolarWindsIncidentResponse:
		if next.SolarWinds == nil {
			return
		}
		if next.SolarWinds.ClearWebhookURL {
			next.SolarWinds.WebhookURL = ""
			return
		}
		if next.SolarWinds.WebhookURL == "" && existing.SolarWinds != nil {
			next.SolarWinds.WebhookURL = existing.SolarWinds.WebhookURL
		}
	default:
	}
}

func IsPolicySetConfigured(policySet *PolicySet) bool {
	return policySet != nil && !policySet.CreatedAt.IsZero()
}

func (p Provider) ToPublic() PublicProvider {
	pub := PublicProvider{
		ID:        p.ID,
		Name:      p.Name,
		Type:      p.Type,
		Enabled:   p.Enabled,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
		UpdatedBy: p.UpdatedBy,
	}
	switch p.Type {
	case ProviderPagerDuty:
		if p.PagerDuty != nil {
			pub.PagerDuty = &PublicPagerDutyProvider{
				RoutingKeyConfigured: p.PagerDuty.RoutingKey != "",
				RoutingKeyPreview:    PreviewSecret(p.PagerDuty.RoutingKey),
			}
		}
	case ProviderSolarWindsIncidentResponse:
		if p.SolarWinds != nil {
			pub.SolarWinds = &PublicSolarWindsProvider{
				WebhookURLConfigured: p.SolarWinds.WebhookURL != "",
				WebhookURLPreview:    PreviewSecret(p.SolarWinds.WebhookURL),
				AllowInsecureHTTP:    p.SolarWinds.AllowInsecureHTTP,
				AllowPrivateNetwork:  p.SolarWinds.AllowPrivateNetwork,
			}
		}
	default:
	}
	return pub
}

func PreviewSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "********"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func defaultIfBlank(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func ValidateWebhookURL(value string, allowInsecureHTTP, allowPrivateNetwork bool) error {
	parsed, err := validateAbsoluteURL(value)
	if err != nil {
		return err
	}
	if parsed.Scheme == "http" && !allowInsecureHTTP {
		return fmt.Errorf("%w: webhook url must use https unless allowInsecureHttp is enabled", ErrInvalidProvider)
	}
	if !allowPrivateNetwork && isBlockedPrivateHostLiteral(parsed.Hostname()) {
		return fmt.Errorf("%w: webhook url must not target loopback or private network unless allowPrivateNetwork is enabled", ErrInvalidProvider)
	}
	return nil
}

func validateAbsoluteURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid target url", ErrInvalidProvider)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("%w: target url must use http or https", ErrInvalidProvider)
	}
	return parsed, nil
}

func isBlockedPrivateHostLiteral(host string) bool {
	host = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(host), "."))
	if host == "" {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}
