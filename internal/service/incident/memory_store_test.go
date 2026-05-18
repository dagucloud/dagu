// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incident

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	incidentmodel "github.com/dagucloud/dagu/internal/incident"
)

type memoryStore struct {
	t          *testing.T
	mu         sync.RWMutex
	providers  map[string]*incidentmodel.Provider
	policySets map[string]*incidentmodel.PolicySet
	states     map[string]*incidentmodel.IncidentState
}

var _ incidentmodel.Store = (*memoryStore)(nil)

func newMemoryStore(t *testing.T) *memoryStore {
	t.Helper()
	return &memoryStore{
		t:          t,
		providers:  map[string]*incidentmodel.Provider{},
		policySets: map[string]*incidentmodel.PolicySet{},
		states:     map[string]*incidentmodel.IncidentState{},
	}
}

func (s *memoryStore) saveProvider(t *testing.T, name string, providerType incidentmodel.ProviderType) *incidentmodel.Provider {
	t.Helper()
	provider := &incidentmodel.Provider{
		Name:    name,
		Type:    providerType,
		Enabled: true,
	}
	switch providerType {
	case incidentmodel.ProviderPagerDuty:
		provider.PagerDuty = &incidentmodel.PagerDutyProvider{RoutingKey: name + "-routing-key"}
	case incidentmodel.ProviderSolarWindsIncidentResponse:
		provider.SolarWinds = &incidentmodel.SolarWindsProvider{
			WebhookURL:          "http://127.0.0.1/incoming",
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
		}
	default:
	}
	normalized, err := incidentmodel.NormalizeProvider(provider, "test")
	require.NoError(t, err)
	require.NoError(t, s.SaveProvider(context.Background(), normalized))
	return normalized
}

func (s *memoryStore) savePolicySet(t *testing.T, policySet *incidentmodel.PolicySet) *incidentmodel.PolicySet {
	t.Helper()
	normalized, err := incidentmodel.NormalizePolicySet(policySet, "test")
	require.NoError(t, err)
	require.NoError(t, s.SavePolicySet(context.Background(), normalized))
	return normalized
}

func (s *memoryStore) SaveProvider(_ context.Context, provider *incidentmodel.Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *provider
	s.providers[provider.ID] = &copy
	return nil
}

func (s *memoryStore) GetProvider(_ context.Context, providerID string) (*incidentmodel.Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, ok := s.providers[providerID]
	if !ok {
		return nil, incidentmodel.ErrProviderNotFound
	}
	copy := *provider
	return &copy, nil
}

func (s *memoryStore) ListProviders(context.Context) ([]*incidentmodel.Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*incidentmodel.Provider, 0, len(s.providers))
	for _, provider := range s.providers {
		copy := *provider
		out = append(out, &copy)
	}
	return out, nil
}

func (s *memoryStore) DeleteProvider(_ context.Context, providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[providerID]; !ok {
		return incidentmodel.ErrProviderNotFound
	}
	delete(s.providers, providerID)
	return nil
}

func (s *memoryStore) SavePolicySet(_ context.Context, policySet *incidentmodel.PolicySet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *policySet
	copy.Policies = append([]incidentmodel.Policy(nil), policySet.Policies...)
	s.policySets[policySetKey(policySet.Scope, policySet.Workspace, policySet.DAGName)] = &copy
	return nil
}

func (s *memoryStore) GetPolicySet(_ context.Context, scope incidentmodel.PolicyScope, workspaceName, dagName string) (*incidentmodel.PolicySet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	policySet, ok := s.policySets[policySetKey(scope, workspaceName, dagName)]
	if !ok {
		return nil, incidentmodel.ErrPolicySetNotFound
	}
	copy := *policySet
	copy.Policies = append([]incidentmodel.Policy(nil), policySet.Policies...)
	return &copy, nil
}

func (s *memoryStore) ListPolicySets(context.Context) ([]*incidentmodel.PolicySet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*incidentmodel.PolicySet, 0, len(s.policySets))
	for _, policySet := range s.policySets {
		copy := *policySet
		copy.Policies = append([]incidentmodel.Policy(nil), policySet.Policies...)
		out = append(out, &copy)
	}
	return out, nil
}

func (s *memoryStore) DeletePolicySet(_ context.Context, scope incidentmodel.PolicyScope, workspaceName, dagName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := policySetKey(scope, workspaceName, dagName)
	if _, ok := s.policySets[key]; !ok {
		return incidentmodel.ErrPolicySetNotFound
	}
	delete(s.policySets, key)
	return nil
}

func (s *memoryStore) SaveState(_ context.Context, state *incidentmodel.IncidentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *state
	s.states[stateKey(state.ProviderID, state.DedupKey)] = &copy
	return nil
}

func (s *memoryStore) GetState(_ context.Context, providerID, dedupKey string) (*incidentmodel.IncidentState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[stateKey(providerID, dedupKey)]
	if !ok {
		return nil, os.ErrNotExist
	}
	copy := *state
	return &copy, nil
}

func (s *memoryStore) ListOpenStatesByDAG(_ context.Context, dagName string) ([]*incidentmodel.IncidentState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	states := make([]*incidentmodel.IncidentState, 0)
	for _, state := range s.states {
		if state.DAGName != dagName || state.Status != incidentmodel.IncidentStatusOpen {
			continue
		}
		copy := *state
		states = append(states, &copy)
	}
	return states, nil
}

func (s *memoryStore) DeleteState(_ context.Context, providerID, dedupKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stateKey(providerID, dedupKey)
	if _, ok := s.states[key]; !ok {
		return os.ErrNotExist
	}
	delete(s.states, key)
	return nil
}

func policySetKey(scope incidentmodel.PolicyScope, workspaceName, dagName string) string {
	return string(scope) + "\x00" + workspaceName + "\x00" + dagName
}

func stateKey(providerID, dedupKey string) string {
	return providerID + "\x00" + dedupKey
}
