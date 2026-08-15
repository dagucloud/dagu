// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const incidentTimeFormat = "2006-01-02T15:04:05.999999999Z07:00"

var _ incident.Store = (*IncidentStore)(nil)

// IncidentStore persists incident configuration and state in a collection.
type IncidentStore struct {
	col       persis.Collection
	encryptor *crypto.Encryptor
}

// NewIncidentStore creates an incident store backed by col.
func NewIncidentStore(col persis.Collection, enc *crypto.Encryptor) (*IncidentStore, error) {
	if col == nil {
		return nil, errors.New("incident store: collection cannot be nil")
	}
	return &IncidentStore{col: col, encryptor: enc}, nil
}

func (s *IncidentStore) SaveProvider(ctx context.Context, provider *incident.Provider) error {
	if provider == nil {
		return errors.New("incident store: provider cannot be nil")
	}
	stored, err := s.providerToStorage(provider)
	if err != nil {
		return err
	}
	return s.put(ctx, incidentProviderID(provider.ID), stored)
}

func (s *IncidentStore) GetProvider(ctx context.Context, providerID string) (*incident.Provider, error) {
	if providerID == "" {
		return nil, incident.ErrProviderNotFound
	}
	rec, err := s.get(ctx, incidentProviderID(providerID), incident.ErrProviderNotFound)
	if err != nil {
		return nil, err
	}
	return s.providerFromRecord(rec)
}

func (s *IncidentStore) ListProviders(ctx context.Context) ([]*incident.Provider, error) {
	recs, err := s.listTolerant(ctx, "providers/", "provider")
	if err != nil {
		return nil, fmt.Errorf("incident store: list providers: %w", err)
	}
	providers := make([]*incident.Provider, 0, len(recs))
	for _, rec := range recs {
		provider, err := s.providerFromRecord(rec)
		if err != nil {
			slog.Warn("incident store: failed to load provider", "record", rec.ID, "error", err)
			continue
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func (s *IncidentStore) DeleteProvider(ctx context.Context, providerID string) error {
	if providerID == "" {
		return incident.ErrProviderNotFound
	}
	return s.delete(ctx, incidentProviderID(providerID), incident.ErrProviderNotFound, "provider")
}

func (s *IncidentStore) SavePolicySet(ctx context.Context, policySet *incident.PolicySet) error {
	if policySet == nil {
		return errors.New("incident store: policy set cannot be nil")
	}
	return s.put(ctx, incidentPolicySetID(policySet.Scope, policySet.Workspace, policySet.DAGName), policySetToStorage(policySet))
}

func (s *IncidentStore) GetPolicySet(
	ctx context.Context,
	scope incident.PolicyScope,
	workspaceName, dagName string,
) (*incident.PolicySet, error) {
	rec, err := s.get(ctx, incidentPolicySetID(scope, workspaceName, dagName), incident.ErrPolicySetNotFound)
	if err != nil {
		return nil, err
	}
	return policySetFromRecord(rec)
}

func (s *IncidentStore) ListPolicySets(ctx context.Context) ([]*incident.PolicySet, error) {
	result := make([]*incident.PolicySet, 0)

	if rec, err := s.col.Get(ctx, incidentPolicySetID(incident.PolicyScopeGlobal, "", "")); err == nil {
		policySet, decodeErr := policySetFromRecord(rec)
		if decodeErr != nil {
			slog.Warn("incident store: failed to load global policy set", "error", decodeErr)
		} else {
			result = append(result, policySet)
		}
	} else if !errors.Is(err, persis.ErrNotFound) {
		slog.Warn("incident store: failed to load global policy set", "error", err)
	}

	for _, prefix := range []string{"policies/workspaces/", "policies/dags/"} {
		recs, err := s.listTolerant(ctx, prefix, "policy set")
		if err != nil {
			return nil, fmt.Errorf("incident store: list policy sets: %w", err)
		}
		for _, rec := range recs {
			policySet, err := policySetFromRecord(rec)
			if err != nil {
				slog.Warn("incident store: failed to load policy set", "record", rec.ID, "error", err)
				continue
			}
			result = append(result, policySet)
		}
	}
	return result, nil
}

func (s *IncidentStore) DeletePolicySet(
	ctx context.Context,
	scope incident.PolicyScope,
	workspaceName, dagName string,
) error {
	return s.delete(ctx, incidentPolicySetID(scope, workspaceName, dagName), incident.ErrPolicySetNotFound, "policy set")
}

func (s *IncidentStore) SaveState(ctx context.Context, state *incident.IncidentState) error {
	if state == nil {
		return errors.New("incident store: state cannot be nil")
	}
	return s.put(ctx, incidentStateID(state.ProviderID, state.DedupKey), stateToStorage(state))
}

func (s *IncidentStore) GetState(ctx context.Context, providerID, dedupKey string) (*incident.IncidentState, error) {
	rec, err := s.get(ctx, incidentStateID(providerID, dedupKey), os.ErrNotExist)
	if err != nil {
		return nil, err
	}
	return stateFromRecord(rec)
}

func (s *IncidentStore) ListOpenStates(ctx context.Context) ([]*incident.IncidentState, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{Prefix: "states/"})
	if err != nil {
		return nil, err
	}
	states := make([]*incident.IncidentState, 0, len(recs))
	for _, rec := range recs {
		state, err := stateFromRecord(rec)
		if err != nil {
			return nil, err
		}
		if state.Status == incident.IncidentStatusOpen {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].ProviderID != states[j].ProviderID {
			return states[i].ProviderID < states[j].ProviderID
		}
		return states[i].DedupKey < states[j].DedupKey
	})
	return states, nil
}

func (s *IncidentStore) DeleteState(ctx context.Context, providerID, dedupKey string) error {
	return s.delete(ctx, incidentStateID(providerID, dedupKey), os.ErrNotExist, "state")
}

func (s *IncidentStore) put(ctx context.Context, id string, value any) error {
	data, err := persis.Encode(value)
	if err != nil {
		return fmt.Errorf("incident store: encode record: %w", err)
	}
	now := time.Now().UTC()
	if err := s.col.Put(ctx, &persis.Record{ID: id, Data: data, CreatedAt: now, UpdatedAt: now}); err != nil {
		return fmt.Errorf("incident store: save record: %w", err)
	}
	return nil
}

func (s *IncidentStore) get(ctx context.Context, id string, notFound error) (*persis.Record, error) {
	rec, err := s.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, notFound
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *IncidentStore) delete(ctx context.Context, id string, notFound error, kind string) error {
	if _, err := s.col.Get(ctx, id); errors.Is(err, persis.ErrNotFound) {
		return notFound
	} else if err != nil {
		return fmt.Errorf("incident store: delete %s: %w", kind, err)
	}
	if err := s.col.Delete(ctx, id); err != nil {
		return fmt.Errorf("incident store: delete %s: %w", kind, err)
	}
	return nil
}

func (s *IncidentStore) listTolerant(ctx context.Context, prefix, kind string) ([]*persis.Record, error) {
	recs, err := listAllStrictWithReadError(ctx, s.col, persis.ListQuery{Prefix: prefix}, func(id string, err error) (bool, error) {
		slog.Warn("incident store: failed to load "+kind, "record", id, "error", err)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
	return recs, nil
}

func incidentProviderID(providerID string) string {
	return "providers/" + hashRecordID(providerID)
}

func incidentPolicySetID(scope incident.PolicyScope, workspaceName, dagName string) string {
	switch scope {
	case incident.PolicyScopeGlobal:
		return "policies/global"
	case incident.PolicyScopeWorkspace:
		return "policies/workspaces/" + hashRecordID(workspaceName)
	case incident.PolicyScopeDAG:
		return "policies/dags/" + hashRecordID(dagName)
	default:
		return "policies/global"
	}
}

func incidentStateID(providerID, dedupKey string) string {
	return "states/" + hashRecordID(providerID+"\x00"+dedupKey)
}

type providerRecord struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Type       incident.ProviderType `json:"type"`
	Enabled    bool                  `json:"enabled"`
	PagerDuty  *pagerDutyRecord      `json:"pagerDuty,omitempty"`
	SolarWinds *solarWindsRecord     `json:"solarWinds,omitempty"`
	CreatedAt  string                `json:"createdAt"`
	UpdatedAt  string                `json:"updatedAt"`
	UpdatedBy  string                `json:"updatedBy,omitempty"`
}

type pagerDutyRecord struct {
	RoutingKeyEnc string `json:"routingKeyEnc,omitempty"`
}

type solarWindsRecord struct {
	WebhookURLEnc       string `json:"webhookUrlEnc,omitempty"`
	AllowInsecureHTTP   bool   `json:"allowInsecureHttp,omitempty"`
	AllowPrivateNetwork bool   `json:"allowPrivateNetwork,omitempty"`
}

type policySetRecord struct {
	ID            string               `json:"id"`
	Scope         incident.PolicyScope `json:"scope"`
	Workspace     string               `json:"workspace,omitempty"`
	DAGName       string               `json:"dagName,omitempty"`
	Enabled       bool                 `json:"enabled"`
	InheritParent bool                 `json:"inheritParent"`
	Policies      []policyRecord       `json:"policies"`
	CreatedAt     string               `json:"createdAt"`
	UpdatedAt     string               `json:"updatedAt"`
	UpdatedBy     string               `json:"updatedBy,omitempty"`
}

type policyRecord struct {
	ID                  string            `json:"id"`
	ProviderID          string            `json:"providerId"`
	Enabled             bool              `json:"enabled"`
	Severity            incident.Severity `json:"severity"`
	ResolveOnRecovery   bool              `json:"resolveOnRecovery"`
	DedupKeyTemplate    string            `json:"dedupKeyTemplate,omitempty"`
	MessageTemplate     string            `json:"messageTemplate,omitempty"`
	DescriptionTemplate string            `json:"descriptionTemplate,omitempty"`
}

type stateRecord struct {
	ID            string                  `json:"id"`
	ProviderID    string                  `json:"providerId"`
	PolicyID      string                  `json:"policyId"`
	Workspace     string                  `json:"workspace,omitempty"`
	DAGName       string                  `json:"dagName"`
	DedupKey      string                  `json:"dedupKey"`
	Status        incident.IncidentStatus `json:"status"`
	ExternalID    string                  `json:"externalId,omitempty"`
	LastRequestID string                  `json:"lastRequestId,omitempty"`
	LastEventID   string                  `json:"lastEventId,omitempty"`
	OpenedAt      string                  `json:"openedAt"`
	ResolvedAt    string                  `json:"resolvedAt,omitempty"`
	UpdatedAt     string                  `json:"updatedAt"`
}

func (s *IncidentStore) providerToStorage(provider *incident.Provider) (*providerRecord, error) {
	stored := &providerRecord{
		ID:        provider.ID,
		Name:      provider.Name,
		Type:      provider.Type,
		Enabled:   provider.Enabled,
		CreatedAt: provider.CreatedAt.Format(incidentTimeFormat),
		UpdatedAt: provider.UpdatedAt.Format(incidentTimeFormat),
		UpdatedBy: provider.UpdatedBy,
	}
	var err error
	if provider.PagerDuty != nil {
		stored.PagerDuty = &pagerDutyRecord{}
		stored.PagerDuty.RoutingKeyEnc, err = s.encryptSecret(provider.PagerDuty.RoutingKey)
		if err != nil {
			return nil, err
		}
	}
	if provider.SolarWinds != nil {
		stored.SolarWinds = &solarWindsRecord{
			AllowInsecureHTTP:   provider.SolarWinds.AllowInsecureHTTP,
			AllowPrivateNetwork: provider.SolarWinds.AllowPrivateNetwork,
		}
		stored.SolarWinds.WebhookURLEnc, err = s.encryptSecret(provider.SolarWinds.WebhookURL)
		if err != nil {
			return nil, err
		}
	}
	return stored, nil
}

func (s *IncidentStore) providerFromRecord(rec *persis.Record) (*incident.Provider, error) {
	var stored providerRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("incident store: parse provider: %w", err)
	}
	provider := &incident.Provider{
		ID:        stored.ID,
		Name:      stored.Name,
		Type:      stored.Type,
		Enabled:   stored.Enabled,
		CreatedAt: parseIncidentTime(stored.CreatedAt),
		UpdatedAt: parseIncidentTime(stored.UpdatedAt),
		UpdatedBy: stored.UpdatedBy,
	}
	var err error
	if stored.PagerDuty != nil {
		provider.PagerDuty = &incident.PagerDutyProvider{}
		provider.PagerDuty.RoutingKey, err = s.decryptSecret(stored.PagerDuty.RoutingKeyEnc)
		if err != nil {
			return nil, err
		}
	}
	if stored.SolarWinds != nil {
		provider.SolarWinds = &incident.SolarWindsProvider{
			AllowInsecureHTTP:   stored.SolarWinds.AllowInsecureHTTP,
			AllowPrivateNetwork: stored.SolarWinds.AllowPrivateNetwork,
		}
		provider.SolarWinds.WebhookURL, err = s.decryptSecret(stored.SolarWinds.WebhookURLEnc)
		if err != nil {
			return nil, err
		}
	}
	return provider, nil
}

func policySetToStorage(policySet *incident.PolicySet) *policySetRecord {
	policies := make([]policyRecord, 0, len(policySet.Policies))
	for _, policy := range policySet.Policies {
		policies = append(policies, policyRecord(policy))
	}
	return &policySetRecord{
		ID:            policySet.ID,
		Scope:         policySet.Scope,
		Workspace:     policySet.Workspace,
		DAGName:       policySet.DAGName,
		Enabled:       policySet.Enabled,
		InheritParent: policySet.InheritParent,
		Policies:      policies,
		CreatedAt:     policySet.CreatedAt.Format(incidentTimeFormat),
		UpdatedAt:     policySet.UpdatedAt.Format(incidentTimeFormat),
		UpdatedBy:     policySet.UpdatedBy,
	}
}

func policySetFromRecord(rec *persis.Record) (*incident.PolicySet, error) {
	var stored policySetRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("incident store: parse policy set: %w", err)
	}
	policies := make([]incident.Policy, 0, len(stored.Policies))
	for _, policy := range stored.Policies {
		policies = append(policies, incident.Policy(policy))
	}
	return &incident.PolicySet{
		ID:            stored.ID,
		Scope:         stored.Scope,
		Workspace:     stored.Workspace,
		DAGName:       stored.DAGName,
		Enabled:       stored.Enabled,
		InheritParent: stored.InheritParent,
		Policies:      policies,
		CreatedAt:     parseIncidentTime(stored.CreatedAt),
		UpdatedAt:     parseIncidentTime(stored.UpdatedAt),
		UpdatedBy:     stored.UpdatedBy,
	}, nil
}

func stateToStorage(state *incident.IncidentState) *stateRecord {
	stored := &stateRecord{
		ID:            state.ID,
		ProviderID:    state.ProviderID,
		PolicyID:      state.PolicyID,
		Workspace:     state.Workspace,
		DAGName:       state.DAGName,
		DedupKey:      state.DedupKey,
		Status:        state.Status,
		ExternalID:    state.ExternalID,
		LastRequestID: state.LastRequestID,
		LastEventID:   state.LastEventID,
		OpenedAt:      state.OpenedAt.Format(incidentTimeFormat),
		UpdatedAt:     state.UpdatedAt.Format(incidentTimeFormat),
	}
	if !state.ResolvedAt.IsZero() {
		stored.ResolvedAt = state.ResolvedAt.Format(incidentTimeFormat)
	}
	return stored
}

func stateFromRecord(rec *persis.Record) (*incident.IncidentState, error) {
	var stored stateRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("incident store: parse state: %w", err)
	}
	return &incident.IncidentState{
		ID:            stored.ID,
		ProviderID:    stored.ProviderID,
		PolicyID:      stored.PolicyID,
		Workspace:     stored.Workspace,
		DAGName:       stored.DAGName,
		DedupKey:      stored.DedupKey,
		Status:        stored.Status,
		ExternalID:    stored.ExternalID,
		LastRequestID: stored.LastRequestID,
		LastEventID:   stored.LastEventID,
		OpenedAt:      parseIncidentTime(stored.OpenedAt),
		ResolvedAt:    parseIncidentTime(stored.ResolvedAt),
		UpdatedAt:     parseIncidentTime(stored.UpdatedAt),
	}, nil
}

func parseIncidentTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(incidentTimeFormat, value)
	if err != nil {
		slog.Debug("Failed to parse incident timestamp", "value", value, "error", err)
		return time.Time{}
	}
	return parsed
}

func (s *IncidentStore) encryptSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", incident.ErrSecretStoreMissing
	}
	return s.encryptor.Encrypt(value)
}

func (s *IncidentStore) decryptSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", incident.ErrSecretStoreMissing
	}
	return s.encryptor.Decrypt(value)
}
