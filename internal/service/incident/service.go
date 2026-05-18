// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incident

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	incidentmodel "github.com/dagucloud/dagu/internal/incident"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

type Service struct {
	store            incidentmodel.Store
	http             *http.Client
	logger           *slog.Logger
	incidentsEnabled func() bool
	publicURL        func() string
	retry            DeliveryRetryConfig
}

type DeliveryRetryConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type Option func(*Service)

type TestResult struct {
	ProviderID   string
	ProviderName string
	ProviderType incidentmodel.ProviderType
	Delivered    bool
	Error        string
}

func WithHTTPClient(client *http.Client) Option {
	return func(s *Service) {
		if client != nil {
			s.http = client
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

func WithIncidentsEnabled(enabled func() bool) Option {
	return func(s *Service) {
		if enabled != nil {
			s.incidentsEnabled = enabled
		}
	}
}

func WithPublicURL(publicURL string) Option {
	return WithPublicURLResolver(func() string { return publicURL })
}

func WithPublicURLResolver(resolver func() string) Option {
	return func(s *Service) {
		if resolver != nil {
			s.publicURL = resolver
		}
	}
}

func WithDeliveryRetry(cfg DeliveryRetryConfig) Option {
	return func(s *Service) {
		if cfg.MaxAttempts > 0 {
			s.retry.MaxAttempts = cfg.MaxAttempts
		}
		if cfg.InitialBackoff >= 0 {
			s.retry.InitialBackoff = cfg.InitialBackoff
		}
		if cfg.MaxBackoff >= 0 {
			s.retry.MaxBackoff = cfg.MaxBackoff
		}
	}
}

func New(store incidentmodel.Store, opts ...Option) *Service {
	svc := &Service{
		store:            store,
		http:             &http.Client{Timeout: 30 * time.Second},
		logger:           slog.Default(),
		incidentsEnabled: func() bool { return true },
		publicURL:        func() string { return "" },
		retry: DeliveryRetryConfig{
			MaxAttempts:    3,
			InitialBackoff: 250 * time.Millisecond,
			MaxBackoff:     2 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func (s *Service) SetPublicURLResolver(resolver func() string) {
	if s == nil || resolver == nil {
		return
	}
	s.publicURL = resolver
}

func (s *Service) incidentsAllowed() bool {
	return s.incidentsEnabled == nil || s.incidentsEnabled()
}

func (s *Service) ListProviders(ctx context.Context) ([]*incidentmodel.Provider, error) {
	if s.store == nil {
		return nil, incidentmodel.ErrProviderNotFound
	}
	providers, err := s.store.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(providers, func(a, b *incidentmodel.Provider) int {
		if a == nil || b == nil {
			switch {
			case a == nil && b == nil:
				return 0
			case a == nil:
				return -1
			default:
				return 1
			}
		}
		if cmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	return providers, nil
}

func (s *Service) GetProvider(ctx context.Context, providerID string) (*incidentmodel.Provider, error) {
	if s.store == nil {
		return nil, incidentmodel.ErrProviderNotFound
	}
	return s.store.GetProvider(ctx, providerID)
}

func (s *Service) SaveProvider(ctx context.Context, provider *incidentmodel.Provider, updatedBy string) (*incidentmodel.Provider, error) {
	if s.store == nil {
		return nil, incidentmodel.ErrProviderNotFound
	}
	if provider == nil {
		return nil, incidentmodel.ErrInvalidProvider
	}
	existing, err := s.store.GetProvider(ctx, provider.ID)
	if err != nil && !errors.Is(err, incidentmodel.ErrProviderNotFound) {
		return nil, err
	}
	if existing != nil {
		provider.ID = existing.ID
		provider.CreatedAt = existing.CreatedAt
		incidentmodel.PreserveProviderSecrets(provider, existing)
	}
	normalized, err := incidentmodel.NormalizeProvider(provider, updatedBy)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveProvider(ctx, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (s *Service) DeleteProvider(ctx context.Context, providerID string) error {
	if s.store == nil {
		return incidentmodel.ErrProviderNotFound
	}
	policySets, err := s.store.ListPolicySets(ctx)
	if err != nil {
		return err
	}
	for _, policySet := range policySets {
		if policySet == nil {
			continue
		}
		for _, policy := range policySet.Policies {
			if policy.ProviderID == providerID {
				return fmt.Errorf("%w: %s is used by policy set %s", incidentmodel.ErrProviderInUse, providerID, policySetID(policySet))
			}
		}
	}
	return s.store.DeleteProvider(ctx, providerID)
}

func (s *Service) GetPolicySet(ctx context.Context, scope incidentmodel.PolicyScope, workspaceName, dagName string) (*incidentmodel.PolicySet, error) {
	policySet, err := s.loadPolicySet(ctx, scope, workspaceName, dagName)
	if err != nil {
		if errors.Is(err, incidentmodel.ErrPolicySetNotFound) {
			return defaultPolicySet(scope, workspaceName, dagName), nil
		}
		return nil, err
	}
	return policySet, nil
}

func (s *Service) ListPolicySets(ctx context.Context) ([]*incidentmodel.PolicySet, error) {
	if s.store == nil {
		return nil, nil
	}
	policySets, err := s.store.ListPolicySets(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(policySets, func(a, b *incidentmodel.PolicySet) int {
		if a == nil || b == nil {
			switch {
			case a == nil && b == nil:
				return 0
			case a == nil:
				return -1
			default:
				return 1
			}
		}
		if cmp := strings.Compare(string(a.Scope), string(b.Scope)); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(a.Workspace, b.Workspace); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.DAGName, b.DAGName)
	})
	return policySets, nil
}

func (s *Service) SavePolicySet(ctx context.Context, policySet *incidentmodel.PolicySet, updatedBy string) (*incidentmodel.PolicySet, error) {
	if s.store == nil {
		return nil, incidentmodel.ErrPolicySetNotFound
	}
	if policySet == nil {
		return nil, incidentmodel.ErrInvalidPolicySet
	}
	existing, err := s.loadPolicySet(ctx, policySet.Scope, policySet.Workspace, policySet.DAGName)
	if err != nil && !errors.Is(err, incidentmodel.ErrPolicySetNotFound) {
		return nil, err
	}
	if existing != nil {
		policySet.ID = existing.ID
		policySet.CreatedAt = existing.CreatedAt
	}
	normalized, err := incidentmodel.NormalizePolicySet(policySet, updatedBy)
	if err != nil {
		return nil, err
	}
	if err := s.validatePolicies(ctx, normalized); err != nil {
		return nil, err
	}
	if err := s.store.SavePolicySet(ctx, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (s *Service) DeletePolicySet(ctx context.Context, scope incidentmodel.PolicyScope, workspaceName, dagName string) error {
	if s.store == nil {
		return incidentmodel.ErrPolicySetNotFound
	}
	return s.store.DeletePolicySet(ctx, scope, workspaceName, dagName)
}

func (s *Service) validatePolicies(ctx context.Context, policySet *incidentmodel.PolicySet) error {
	for _, policy := range policySet.Policies {
		if _, err := s.store.GetProvider(ctx, policy.ProviderID); err != nil {
			if errors.Is(err, incidentmodel.ErrProviderNotFound) {
				return fmt.Errorf("%w: %s", incidentmodel.ErrProviderNotFound, policy.ProviderID)
			}
			return err
		}
	}
	return nil
}

func (s *Service) SendProviderTest(ctx context.Context, providerID string) (*TestResult, error) {
	provider, err := s.GetProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	event := s.testEvent()
	policy := incidentmodel.Policy{
		ID:                  "incident-test",
		ProviderID:          provider.ID,
		Enabled:             true,
		Severity:            incidentmodel.SeverityInfo,
		ResolveOnRecovery:   true,
		DedupKeyTemplate:    "dagu:test:{{run.id}}",
		MessageTemplate:     "Dagu test incident",
		DescriptionTemplate: "This is a test incident from Dagu.\n{{run.link}}",
	}
	dedupKey := renderIncidentTemplate(policy.DedupKeyTemplate, event, s.publicURL())
	result := &TestResult{
		ProviderID:   provider.ID,
		ProviderName: provider.Name,
		ProviderType: provider.Type,
	}
	if _, err := s.sendProviderEvent(ctx, provider, providerActionTrigger, dedupKey, policy, event); err != nil {
		result.Error = err.Error()
		return result, nil
	}
	resolveEvent := event
	resolveEvent.Type = eventstore.TypeDAGRunSucceeded
	resolveEvent.Status.Status = core.Succeeded
	resolveEvent.Status.Error = ""
	if _, err := s.sendProviderEvent(ctx, provider, providerActionResolve, dedupKey, policy, resolveEvent); err != nil {
		result.Error = err.Error()
		return result, nil
	}
	result.Delivered = true
	return result, nil
}

func (s *Service) NotificationDestinations() []string {
	if !s.incidentsAllowed() || s.store == nil {
		return nil
	}
	policySets, err := s.ListPolicySets(context.Background())
	if err != nil {
		s.logger.Warn("Failed to list incident destinations", slog.String("error", err.Error()))
		return nil
	}
	var destinations []string
	for _, policySet := range policySets {
		if !policySetDeliversOwnPolicies(policySet) {
			continue
		}
		for _, policy := range policySet.Policies {
			if policy.Enabled {
				destinations = append(destinations, policyDestinationID(policySet, policy.ID))
			}
		}
	}
	slices.Sort(destinations)
	return destinations
}

func (s *Service) NotificationDestinationsForEvent(event chatbridge.NotificationEvent) []string {
	if !s.incidentsAllowed() || !incidentEventSupported(event) {
		return nil
	}
	policySet := s.effectivePolicySetForEvent(context.Background(), event)
	if !policySetDeliversOwnPolicies(policySet) {
		return nil
	}
	destinations := make([]string, 0, len(policySet.Policies))
	for _, policy := range policySet.Policies {
		if !policyMatchesEvent(policy, event.Type) {
			continue
		}
		destinations = append(destinations, policyDestinationID(policySet, policy.ID))
	}
	slices.Sort(destinations)
	return destinations
}

func (s *Service) FlushNotificationBatch(ctx context.Context, destination string, batch chatbridge.NotificationBatch, _ bool) bool {
	if !s.incidentsAllowed() {
		return true
	}
	parsed := parsePolicyDestinationID(destination)
	if !parsed.OK {
		return false
	}
	policySet, err := s.loadPolicySet(ctx, parsed.Scope, parsed.Workspace, parsed.DAGName)
	if err != nil {
		if errors.Is(err, incidentmodel.ErrPolicySetNotFound) {
			return true
		}
		s.logger.Warn("Failed to load incident policy set",
			slog.String("destination", destination),
			slog.String("error", err.Error()),
		)
		return false
	}
	policy, ok := findPolicy(policySet, parsed.PolicyID)
	if !ok || !policy.Enabled || !policySetDeliversOwnPolicies(policySet) {
		return true
	}
	provider, err := s.GetProvider(ctx, policy.ProviderID)
	if err != nil {
		if errors.Is(err, incidentmodel.ErrProviderNotFound) {
			return true
		}
		s.logger.Warn("Failed to load incident provider",
			slog.String("provider", policy.ProviderID),
			slog.String("error", err.Error()),
		)
		return false
	}
	if !provider.Enabled {
		return true
	}
	for _, event := range batch.Events {
		if !s.deliverIncidentEvent(ctx, policySet, policy, provider, event) {
			return false
		}
	}
	return true
}

func (s *Service) ShouldDeliverNotificationBatch(chatbridge.NotificationBatch) bool {
	return true
}

func (s *Service) deliverIncidentEvent(
	ctx context.Context,
	policySet *incidentmodel.PolicySet,
	policy incidentmodel.Policy,
	provider *incidentmodel.Provider,
	event chatbridge.NotificationEvent,
) bool {
	if !incidentEventSupported(event) || !policyMatchesEvent(policy, event.Type) {
		return true
	}
	if effective := s.effectivePolicySetForEvent(ctx, event); policySetID(effective) != policySetID(policySet) {
		return true
	}
	dedupKey := strings.TrimSpace(renderIncidentTemplate(policy.DedupKeyTemplate, event, s.publicURL()))
	if dedupKey == "" {
		s.logger.Warn("Rendered incident dedup key is empty",
			slog.String("policy_id", policy.ID),
			slog.String("dag", event.Status.Name),
		)
		return true
	}
	switch event.Type {
	case eventstore.TypeDAGRunFailed:
		return s.openIncident(ctx, provider, policy, event, dedupKey)
	case eventstore.TypeDAGRunSucceeded:
		return s.resolveIncident(ctx, provider, policy, event, dedupKey)
	default:
		return true
	}
}

func (s *Service) openIncident(ctx context.Context, provider *incidentmodel.Provider, policy incidentmodel.Policy, event chatbridge.NotificationEvent, dedupKey string) bool {
	delivery, err := s.withRetry(ctx, func() (*providerDeliveryResult, error) {
		return s.sendProviderEvent(ctx, provider, providerActionTrigger, dedupKey, policy, event)
	})
	if err != nil {
		s.logger.Warn("Failed to trigger incident",
			slog.String("provider", provider.ID),
			slog.String("dag", event.Status.Name),
			slog.String("dedup_key", dedupKey),
			slog.String("error", err.Error()),
		)
		return false
	}
	openedAt := time.Now().UTC()
	externalID := delivery.ExternalID
	if existing, err := s.store.GetState(ctx, provider.ID, dedupKey); err == nil && existing != nil && existing.Status == incidentmodel.IncidentStatusOpen {
		if !existing.OpenedAt.IsZero() {
			openedAt = existing.OpenedAt
		}
		if externalID == "" {
			externalID = existing.ExternalID
		}
	}
	state, err := incidentmodel.NormalizeState(&incidentmodel.IncidentState{
		ProviderID:    provider.ID,
		PolicyID:      policy.ID,
		Workspace:     eventWorkspaceName(event),
		DAGName:       event.Status.Name,
		DedupKey:      dedupKey,
		Status:        incidentmodel.IncidentStatusOpen,
		ExternalID:    externalID,
		LastRequestID: delivery.RequestID,
		LastEventID:   delivery.EventID,
		OpenedAt:      openedAt,
	})
	if err != nil {
		s.logger.Warn("Failed to normalize incident state", slog.String("error", err.Error()))
		return true
	}
	if err := s.store.SaveState(ctx, state); err != nil {
		s.logger.Warn("Failed to save incident state", slog.String("error", err.Error()))
	}
	return true
}

func (s *Service) resolveIncident(ctx context.Context, provider *incidentmodel.Provider, policy incidentmodel.Policy, event chatbridge.NotificationEvent, dedupKey string) bool {
	if !policy.ResolveOnRecovery {
		return true
	}
	state, err := s.store.GetState(ctx, provider.ID, dedupKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		s.logger.Warn("Failed to load incident state", slog.String("error", err.Error()))
		return false
	}
	if state.Status != incidentmodel.IncidentStatusOpen {
		return true
	}
	delivery, err := s.withRetry(ctx, func() (*providerDeliveryResult, error) {
		return s.sendProviderEvent(ctx, provider, providerActionResolve, dedupKey, policy, event)
	})
	if err != nil {
		s.logger.Warn("Failed to resolve incident",
			slog.String("provider", provider.ID),
			slog.String("dag", event.Status.Name),
			slog.String("dedup_key", dedupKey),
			slog.String("error", err.Error()),
		)
		return false
	}
	state.Status = incidentmodel.IncidentStatusResolved
	state.ResolvedAt = time.Now().UTC()
	if delivery.ExternalID != "" {
		state.ExternalID = delivery.ExternalID
	}
	if delivery.RequestID != "" {
		state.LastRequestID = delivery.RequestID
	}
	if delivery.EventID != "" {
		state.LastEventID = delivery.EventID
	}
	state.PolicyID = policy.ID
	normalized, err := incidentmodel.NormalizeState(state)
	if err != nil {
		s.logger.Warn("Failed to normalize resolved incident state", slog.String("error", err.Error()))
		return true
	}
	if err := s.store.SaveState(ctx, normalized); err != nil {
		s.logger.Warn("Failed to save resolved incident state", slog.String("error", err.Error()))
	}
	return true
}

func (s *Service) effectivePolicySetForEvent(ctx context.Context, event chatbridge.NotificationEvent) *incidentmodel.PolicySet {
	if event.Status == nil {
		return nil
	}
	if policySet, err := s.loadPolicySet(ctx, incidentmodel.PolicyScopeDAG, "", event.Status.Name); err == nil {
		if !policySet.InheritParent {
			return policySet
		}
	} else if !errors.Is(err, incidentmodel.ErrPolicySetNotFound) {
		s.logger.Warn("Failed to load DAG incident policy set",
			slog.String("dag", event.Status.Name),
			slog.String("error", err.Error()),
		)
	}
	workspaceName := eventWorkspaceName(event)
	if workspaceName != "" {
		if policySet, err := s.loadPolicySet(ctx, incidentmodel.PolicyScopeWorkspace, workspaceName, ""); err == nil {
			if !policySet.InheritParent {
				return policySet
			}
		} else if !errors.Is(err, incidentmodel.ErrPolicySetNotFound) {
			s.logger.Warn("Failed to load workspace incident policy set",
				slog.String("workspace", workspaceName),
				slog.String("error", err.Error()),
			)
		}
	}
	policySet, err := s.loadPolicySet(ctx, incidentmodel.PolicyScopeGlobal, "", "")
	if err != nil {
		if !errors.Is(err, incidentmodel.ErrPolicySetNotFound) {
			s.logger.Warn("Failed to load global incident policy set", slog.String("error", err.Error()))
		}
		return defaultPolicySet(incidentmodel.PolicyScopeGlobal, "", "")
	}
	return policySet
}

func (s *Service) loadPolicySet(ctx context.Context, scope incidentmodel.PolicyScope, workspaceName, dagName string) (*incidentmodel.PolicySet, error) {
	if s.store == nil {
		return nil, incidentmodel.ErrPolicySetNotFound
	}
	return s.store.GetPolicySet(ctx, scope, workspaceName, dagName)
}

func defaultPolicySet(scope incidentmodel.PolicyScope, workspaceName, dagName string) *incidentmodel.PolicySet {
	return &incidentmodel.PolicySet{
		Scope:         scope,
		Workspace:     workspaceName,
		DAGName:       dagName,
		Enabled:       true,
		InheritParent: scope != incidentmodel.PolicyScopeGlobal,
		Policies:      []incidentmodel.Policy{},
	}
}

func policySetDeliversOwnPolicies(policySet *incidentmodel.PolicySet) bool {
	return policySet != nil && policySet.Enabled && !policySet.InheritParent
}

func policyMatchesEvent(policy incidentmodel.Policy, eventType eventstore.EventType) bool {
	if !policy.Enabled {
		return false
	}
	switch eventType {
	case eventstore.TypeDAGRunFailed:
		return true
	case eventstore.TypeDAGRunSucceeded:
		return policy.ResolveOnRecovery
	default:
		return false
	}
}

func incidentEventSupported(event chatbridge.NotificationEvent) bool {
	if event.Status == nil || event.Status.Name == "" {
		return false
	}
	switch event.Type {
	case eventstore.TypeDAGRunFailed:
		return isFinalFailure(event.Status)
	case eventstore.TypeDAGRunSucceeded:
		return true
	default:
		return false
	}
}

func isFinalFailure(status *exec.DAGRunStatus) bool {
	if status == nil || status.Status != core.Failed {
		return false
	}
	return status.AutoRetryLimit <= 0 || status.AutoRetryCount >= status.AutoRetryLimit
}

func eventWorkspaceName(event chatbridge.NotificationEvent) string {
	if event.Status == nil {
		return ""
	}
	workspaceName, state := exec.WorkspaceLabelFromLabels(core.NewLabels(event.Status.Labels))
	if state == exec.WorkspaceLabelValid {
		return workspaceName
	}
	return ""
}

func findPolicy(policySet *incidentmodel.PolicySet, policyID string) (incidentmodel.Policy, bool) {
	if policySet == nil || policyID == "" {
		return incidentmodel.Policy{}, false
	}
	for _, policy := range policySet.Policies {
		if policy.ID == policyID {
			return policy, true
		}
	}
	return incidentmodel.Policy{}, false
}

func policySetID(policySet *incidentmodel.PolicySet) string {
	if policySet == nil {
		return ""
	}
	switch policySet.Scope {
	case incidentmodel.PolicyScopeWorkspace:
		return string(policySet.Scope) + ":" + policySet.Workspace
	case incidentmodel.PolicyScopeDAG:
		return string(policySet.Scope) + ":" + policySet.DAGName
	default:
		return string(incidentmodel.PolicyScopeGlobal)
	}
}

func policyDestinationID(policySet *incidentmodel.PolicySet, policyID string) string {
	if policySet == nil || policyID == "" {
		return ""
	}
	return "incident-policy" + "\x00" + string(policySet.Scope) + "\x00" + policySet.Workspace + "\x00" + policySet.DAGName + "\x00" + policyID
}

type parsedPolicyDestination struct {
	OK        bool
	Scope     incidentmodel.PolicyScope
	Workspace string
	DAGName   string
	PolicyID  string
}

func parsePolicyDestinationID(value string) parsedPolicyDestination {
	parts := strings.Split(value, "\x00")
	if len(parts) != 5 || parts[0] != "incident-policy" {
		return parsedPolicyDestination{}
	}
	return parsedPolicyDestination{
		OK:        true,
		Scope:     incidentmodel.PolicyScope(parts[1]),
		Workspace: parts[2],
		DAGName:   parts[3],
		PolicyID:  parts[4],
	}
}

type providerAction string

const (
	providerActionTrigger providerAction = "trigger"
	providerActionResolve providerAction = "resolve"
)

type providerDeliveryResult struct {
	ExternalID string
	RequestID  string
	EventID    string
}

func (s *Service) sendProviderEvent(
	ctx context.Context,
	provider *incidentmodel.Provider,
	action providerAction,
	dedupKey string,
	policy incidentmodel.Policy,
	event chatbridge.NotificationEvent,
) (*providerDeliveryResult, error) {
	switch provider.Type {
	case incidentmodel.ProviderPagerDuty:
		return s.sendPagerDuty(ctx, provider, action, dedupKey, policy, event)
	case incidentmodel.ProviderSolarWindsIncidentResponse:
		return s.sendSolarWinds(ctx, provider, action, dedupKey, policy, event)
	default:
		return nil, incidentmodel.ErrUnsupportedProvider
	}
}

func (s *Service) sendPagerDuty(
	ctx context.Context,
	provider *incidentmodel.Provider,
	action providerAction,
	dedupKey string,
	policy incidentmodel.Policy,
	event chatbridge.NotificationEvent,
) (*providerDeliveryResult, error) {
	if provider.PagerDuty == nil || provider.PagerDuty.RoutingKey == "" {
		return nil, errors.New("pagerduty routing key is not configured")
	}
	payload := map[string]any{
		"routing_key":  provider.PagerDuty.RoutingKey,
		"event_action": string(action),
		"dedup_key":    dedupKey,
	}
	if action == providerActionTrigger {
		payload["payload"] = map[string]any{
			"summary":        renderIncidentTemplate(policy.MessageTemplate, event, s.publicURL()),
			"source":         "Dagu",
			"severity":       string(policy.Severity),
			"custom_details": incidentCustomDetails(event, s.publicURL()),
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, incidentmodel.PagerDutyEventsAPIEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	result, err := s.doJSONRequest(req)
	if err != nil {
		return nil, err
	}
	if result.EventID == "" {
		result.EventID = dedupKey
	}
	return result, nil
}

func (s *Service) sendSolarWinds(
	ctx context.Context,
	provider *incidentmodel.Provider,
	action providerAction,
	dedupKey string,
	policy incidentmodel.Policy,
	event chatbridge.NotificationEvent,
) (*providerDeliveryResult, error) {
	if provider.SolarWinds == nil || provider.SolarWinds.WebhookURL == "" {
		return nil, errors.New("solarwinds webhook url is not configured")
	}
	if err := validateOutboundURL(ctx, provider.SolarWinds.WebhookURL, provider.SolarWinds.AllowInsecureHTTP, provider.SolarWinds.AllowPrivateNetwork); err != nil {
		return nil, err
	}
	payload := map[string]any{
		"status":   string(action),
		"event_id": dedupKey,
	}
	if action == providerActionTrigger {
		payload["message"] = renderIncidentTemplate(policy.MessageTemplate, event, s.publicURL())
		payload["description"] = renderIncidentTemplate(policy.DescriptionTemplate, event, s.publicURL())
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.SolarWinds.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	result, err := s.doJSONRequest(req)
	if err != nil {
		return nil, err
	}
	if result.EventID == "" {
		result.EventID = dedupKey
	}
	return result, nil
}

func (s *Service) doJSONRequest(req *http.Request) (*providerDeliveryResult, error) {
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, temporaryDeliveryError{err: err}
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body := limitedResponseBody(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("incident provider returned HTTP %d%s", resp.StatusCode, responseBodySuffix(body))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, temporaryDeliveryError{err: err}
		}
		return nil, err
	}
	return providerDeliveryResultFromBody(body), nil
}

func providerDeliveryResultFromBody(body []byte) *providerDeliveryResult {
	if len(body) == 0 {
		return &providerDeliveryResult{}
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return &providerDeliveryResult{}
	}
	result := &providerDeliveryResult{}
	for _, key := range []string{"dedup_key", "event_id"} {
		if value, ok := raw[key].(string); ok && value != "" {
			result.EventID = value
			break
		}
	}
	for _, key := range []string{"request_id", "requestId"} {
		if value, ok := raw[key].(string); ok && value != "" {
			result.RequestID = value
			break
		}
	}
	for _, key := range []string{"incident_id", "incidentId"} {
		if value, ok := raw[key].(string); ok && value != "" {
			result.ExternalID = value
			break
		}
	}
	return result
}

func limitedResponseBody(body io.Reader) []byte {
	if body == nil {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(body, 1024))
	return bytes.TrimSpace(data)
}

func responseBodySuffix(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return ": " + string(body)
}

type temporaryDeliveryError struct {
	err error
}

func (e temporaryDeliveryError) Error() string {
	if e.err == nil {
		return "temporary incident delivery error"
	}
	return e.err.Error()
}

func (e temporaryDeliveryError) Unwrap() error {
	return e.err
}

func isTemporaryDeliveryError(err error) bool {
	var temporary temporaryDeliveryError
	return errors.As(err, &temporary)
}

func (s *Service) withRetry(ctx context.Context, send func() (*providerDeliveryResult, error)) (*providerDeliveryResult, error) {
	attempts := s.retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	backoff := s.retry.InitialBackoff
	maxBackoff := s.retry.MaxBackoff
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := send()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == attempts || !isTemporaryDeliveryError(err) {
			return nil, err
		}
		if backoff <= 0 {
			continue
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
		if maxBackoff > 0 && backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return nil, lastErr
}

func validateOutboundURL(ctx context.Context, rawURL string, allowInsecureHTTP, allowPrivateNetwork bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if req.URL.Scheme == "http" && !allowInsecureHTTP {
		return errors.New("webhook url must use https unless allowInsecureHttp is enabled")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return errors.New("webhook url must use http or https")
	}
	host := req.URL.Hostname()
	if host == "" {
		return errors.New("webhook url host is required")
	}
	if allowPrivateNetwork {
		return nil
	}
	if isPrivateHostLiteral(host) {
		return errors.New("webhook url targets loopback or private network")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return rejectPrivateAddress(addr)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve webhook host: %w", err)
	}
	for _, addr := range addrs {
		if parsed, ok := netip.AddrFromSlice(addr.IP); ok {
			if err := rejectPrivateAddress(parsed); err != nil {
				return err
			}
		}
	}
	return nil
}

func isPrivateHostLiteral(host string) bool {
	host = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(host), "."))
	return host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func rejectPrivateAddress(addr netip.Addr) error {
	addr = addr.Unmap()
	if addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() {
		return errors.New("webhook url resolves to loopback or private network")
	}
	return nil
}

func (s *Service) testEvent() chatbridge.NotificationEvent {
	now := time.Now().UTC()
	return chatbridge.NotificationEvent{
		Key:  "incident-test:" + uuid.NewString(),
		Type: eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{
			Name:       "incident-test",
			DAGRunID:   "incident-test-" + uuid.NewString(),
			AttemptID:  "incident-test",
			Status:     core.Failed,
			Error:      "This is a test incident from Dagu.",
			StartedAt:  stringutil.FormatTime(now.Add(-time.Minute)),
			FinishedAt: stringutil.FormatTime(now),
		},
		ObservedAt: now,
	}
}

var incidentTemplateTokenRE = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

func renderIncidentTemplate(template string, event chatbridge.NotificationEvent, publicURL string) string {
	values := incidentTemplateValues(event, publicURL)
	return incidentTemplateTokenRE.ReplaceAllStringFunc(template, func(token string) string {
		matches := incidentTemplateTokenRE.FindStringSubmatch(token)
		if len(matches) != 2 {
			return ""
		}
		return values[matches[1]]
	})
}

func incidentTemplateValues(event chatbridge.NotificationEvent, publicURL string) map[string]string {
	values := map[string]string{"event.type": string(event.Type)}
	if !event.ObservedAt.IsZero() {
		values["event.observedAt"] = event.ObservedAt.Format(time.RFC3339)
	}
	if event.Status == nil {
		return values
	}
	status := event.Status
	workspaceName := eventWorkspaceName(event)
	values["dag.name"] = status.Name
	values["dagName"] = status.Name
	values["run.id"] = status.DAGRunID
	values["dagRunId"] = status.DAGRunID
	values["run.status"] = status.Status.String()
	values["status"] = status.Status.String()
	values["run.error"] = status.Error
	values["error"] = status.Error
	values["run.startedAt"] = incidentTemplateTime(status.StartedAt)
	values["run.finishedAt"] = incidentTemplateTime(status.FinishedAt)
	values["run.attemptId"] = status.AttemptID
	values["attempt.id"] = status.AttemptID
	values["attemptId"] = status.AttemptID
	values["workspace"] = workspaceName
	values["worker.id"] = status.WorkerID
	values["eventType"] = string(event.Type)
	runPath := incidentRunPath(status)
	runURL := incidentRunURL(publicURL, runPath)
	runLink := ""
	if runURL != "" {
		runLink = "Run: " + runURL
	}
	values["run.path"] = runPath
	values["runPath"] = runPath
	values["run.url"] = runURL
	values["runUrl"] = runURL
	values["run.link"] = runLink
	values["runLink"] = runLink
	return values
}

func incidentTemplateTime(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := stringutil.ParseTime(value)
	if err != nil || parsed.IsZero() {
		return value
	}
	return parsed.Format(time.RFC3339)
}

func incidentRunPath(status *exec.DAGRunStatus) string {
	if status == nil || status.Name == "" || status.DAGRunID == "" {
		return ""
	}
	root := status.Root
	if root.Zero() {
		root = exec.NewDAGRunRef(status.Name, status.DAGRunID)
	}
	if root.Name == "" || root.ID == "" {
		return ""
	}
	base := "/dag-runs/" + url.PathEscape(root.Name) + "/" + url.PathEscape(root.ID)
	if status.Parent.Zero() || (status.Name == root.Name && status.DAGRunID == root.ID) {
		return base
	}
	query := url.Values{}
	query.Set("subDAGRunId", status.DAGRunID)
	query.Set("dagRunId", root.ID)
	query.Set("dagRunName", root.Name)
	return base + "?" + query.Encode()
}

func incidentRunURL(publicURL, runPath string) string {
	if runPath == "" {
		return ""
	}
	publicURL = normalizeIncidentPublicURL(publicURL)
	if publicURL == "" {
		return ""
	}
	return strings.TrimRight(publicURL, "/") + "/" + strings.TrimLeft(runPath, "/")
}

func normalizeIncidentPublicURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

func incidentCustomDetails(event chatbridge.NotificationEvent, publicURL string) map[string]any {
	if event.Status == nil {
		return map[string]any{}
	}
	runPath := incidentRunPath(event.Status)
	details := map[string]any{
		"dag":        event.Status.Name,
		"dagRunId":   event.Status.DAGRunID,
		"attemptId":  event.Status.AttemptID,
		"status":     event.Status.Status.String(),
		"workspace":  eventWorkspaceName(event),
		"runPath":    runPath,
		"observedAt": event.ObservedAt.Format(time.RFC3339Nano),
	}
	if event.Status.Error != "" {
		details["error"] = event.Status.Error
	}
	if runURL := incidentRunURL(publicURL, runPath); runURL != "" {
		details["runUrl"] = runURL
	}
	return details
}
