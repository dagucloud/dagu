// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileincident

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/incident"
)

const (
	fileExtension        = ".json"
	globalPolicyFileName = "global.json"
	dirPermissions       = 0750
	filePermissions      = 0600
	timeFormat           = "2006-01-02T15:04:05.999999999Z07:00"
)

type Option func(*Store)

func WithEncryptor(enc *crypto.Encryptor) Option {
	return func(s *Store) {
		s.encryptor = enc
	}
}

type Store struct {
	baseDir   string
	encryptor *crypto.Encryptor
	mu        sync.RWMutex
}

var _ incident.Store = (*Store)(nil)

func New(baseDir string, opts ...Option) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("fileincident: baseDir cannot be empty")
	}
	store := &Store{baseDir: baseDir}
	for _, opt := range opts {
		opt(store)
	}
	for _, dir := range []string{
		baseDir,
		store.providerDir(),
		store.policyWorkspaceDir(),
		store.policyDAGDir(),
		store.stateDir(),
	} {
		if err := os.MkdirAll(dir, dirPermissions); err != nil {
			return nil, fmt.Errorf("fileincident: failed to create directory %s: %w", dir, err)
		}
	}
	return store, nil
}

func (s *Store) SaveProvider(_ context.Context, provider *incident.Provider) error {
	if provider == nil {
		return errors.New("fileincident: provider cannot be nil")
	}
	stored, err := s.providerToStorage(provider)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(s.providerFilePath(provider.ID), stored, filePermissions); err != nil {
		return fmt.Errorf("fileincident: %w", err)
	}
	return nil
}

func (s *Store) GetProvider(_ context.Context, providerID string) (*incident.Provider, error) {
	if providerID == "" {
		return nil, incident.ErrProviderNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, err := s.loadProviderFromFile(s.providerFilePath(providerID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, incident.ErrProviderNotFound
		}
		return nil, err
	}
	return provider, nil
}

func (s *Store) ListProviders(_ context.Context) ([]*incident.Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.providerDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("fileincident: list providers: %w", err)
	}
	result := make([]*incident.Provider, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != fileExtension {
			continue
		}
		provider, err := s.loadProviderFromFile(filepath.Join(s.providerDir(), entry.Name()))
		if err != nil {
			slog.Warn("fileincident: failed to load provider file",
				slog.String("file", entry.Name()),
				slog.String("error", err.Error()),
			)
			continue
		}
		result = append(result, provider)
	}
	return result, nil
}

func (s *Store) DeleteProvider(_ context.Context, providerID string) error {
	if providerID == "" {
		return incident.ErrProviderNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.Remove(s.providerFilePath(providerID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return incident.ErrProviderNotFound
		}
		return fmt.Errorf("fileincident: delete provider: %w", err)
	}
	return nil
}

func (s *Store) SavePolicySet(_ context.Context, policySet *incident.PolicySet) error {
	if policySet == nil {
		return errors.New("fileincident: policy set cannot be nil")
	}
	stored := policySetToStorage(policySet)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(s.policySetFilePath(policySet.Scope, policySet.Workspace, policySet.DAGName), stored, filePermissions); err != nil {
		return fmt.Errorf("fileincident: %w", err)
	}
	return nil
}

func (s *Store) GetPolicySet(_ context.Context, scope incident.PolicyScope, workspaceName, dagName string) (*incident.PolicySet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	policySet, err := s.loadPolicySetFromFile(s.policySetFilePath(scope, workspaceName, dagName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, incident.ErrPolicySetNotFound
		}
		return nil, err
	}
	return policySet, nil
}

func (s *Store) ListPolicySets(_ context.Context) ([]*incident.PolicySet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*incident.PolicySet, 0)
	if policySet, err := s.loadPolicySetFromFile(s.policySetFilePath(incident.PolicyScopeGlobal, "", "")); err == nil {
		result = append(result, policySet)
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("fileincident: failed to load global policy set", slog.String("error", err.Error()))
	}
	for _, dir := range []string{s.policyWorkspaceDir(), s.policyDAGDir()} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("fileincident: list policy sets: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != fileExtension {
				continue
			}
			policySet, err := s.loadPolicySetFromFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				slog.Warn("fileincident: failed to load policy set file",
					slog.String("file", entry.Name()),
					slog.String("error", err.Error()),
				)
				continue
			}
			result = append(result, policySet)
		}
	}
	return result, nil
}

func (s *Store) DeletePolicySet(_ context.Context, scope incident.PolicyScope, workspaceName, dagName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.Remove(s.policySetFilePath(scope, workspaceName, dagName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return incident.ErrPolicySetNotFound
		}
		return fmt.Errorf("fileincident: delete policy set: %w", err)
	}
	return nil
}

func (s *Store) SaveState(_ context.Context, state *incident.IncidentState) error {
	if state == nil {
		return errors.New("fileincident: state cannot be nil")
	}
	stored := stateToStorage(state)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(s.stateFilePath(state.ProviderID, state.DedupKey), stored, filePermissions); err != nil {
		return fmt.Errorf("fileincident: %w", err)
	}
	return nil
}

func (s *Store) GetState(_ context.Context, providerID, dedupKey string) (*incident.IncidentState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, err := s.loadStateFromFile(s.stateFilePath(providerID, dedupKey))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return state, nil
}

func (s *Store) ListOpenStatesByDAG(_ context.Context, dagName string) ([]*incident.IncidentState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.stateDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	states := make([]*incident.IncidentState, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != fileExtension {
			continue
		}
		state, err := s.loadStateFromFile(filepath.Join(s.stateDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		if state.DAGName != dagName || state.Status != incident.IncidentStatusOpen {
			continue
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].ProviderID != states[j].ProviderID {
			return states[i].ProviderID < states[j].ProviderID
		}
		return states[i].DedupKey < states[j].DedupKey
	})
	return states, nil
}

func (s *Store) DeleteState(_ context.Context, providerID, dedupKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.Remove(s.stateFilePath(providerID, dedupKey)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return fmt.Errorf("fileincident: delete state: %w", err)
	}
	return nil
}

func (s *Store) providerDir() string {
	return filepath.Join(s.baseDir, "providers")
}

func (s *Store) policyDir() string {
	return filepath.Join(s.baseDir, "policies")
}

func (s *Store) policyWorkspaceDir() string {
	return filepath.Join(s.policyDir(), "workspaces")
}

func (s *Store) policyDAGDir() string {
	return filepath.Join(s.policyDir(), "dags")
}

func (s *Store) stateDir() string {
	return filepath.Join(s.baseDir, "states")
}

func (s *Store) providerFilePath(providerID string) string {
	return filepath.Join(s.providerDir(), hashFileName(providerID))
}

func (s *Store) policySetFilePath(scope incident.PolicyScope, workspaceName, dagName string) string {
	switch scope {
	case incident.PolicyScopeGlobal:
		return filepath.Join(s.policyDir(), globalPolicyFileName)
	case incident.PolicyScopeWorkspace:
		return filepath.Join(s.policyWorkspaceDir(), hashFileName(workspaceName))
	case incident.PolicyScopeDAG:
		return filepath.Join(s.policyDAGDir(), hashFileName(dagName))
	default:
		return filepath.Join(s.policyDir(), globalPolicyFileName)
	}
}

func (s *Store) stateFilePath(providerID, dedupKey string) string {
	return filepath.Join(s.stateDir(), hashFileName(providerID+"\x00"+dedupKey))
}

func hashFileName(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:]) + fileExtension
}

func (s *Store) loadProviderFromFile(path string) (*incident.Provider, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var stored providerForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("fileincident: parse provider: %w", err)
	}
	return s.providerFromStorage(&stored)
}

func (s *Store) loadPolicySetFromFile(path string) (*incident.PolicySet, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var stored policySetForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("fileincident: parse policy set: %w", err)
	}
	return policySetFromStorage(&stored), nil
}

func (s *Store) loadStateFromFile(path string) (*incident.IncidentState, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var stored stateForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("fileincident: parse state: %w", err)
	}
	return stateFromStorage(&stored), nil
}

type providerForStorage struct {
	ID         string                     `json:"id"`
	Name       string                     `json:"name"`
	Type       incident.ProviderType      `json:"type"`
	Enabled    bool                       `json:"enabled"`
	PagerDuty  *pagerDutyProviderStorage  `json:"pagerDuty,omitempty"`
	SolarWinds *solarWindsProviderStorage `json:"solarWinds,omitempty"`
	CreatedAt  string                     `json:"createdAt"`
	UpdatedAt  string                     `json:"updatedAt"`
	UpdatedBy  string                     `json:"updatedBy,omitempty"`
}

type pagerDutyProviderStorage struct {
	RoutingKeyEnc string `json:"routingKeyEnc,omitempty"`
}

type solarWindsProviderStorage struct {
	WebhookURLEnc       string `json:"webhookUrlEnc,omitempty"`
	AllowInsecureHTTP   bool   `json:"allowInsecureHttp,omitempty"`
	AllowPrivateNetwork bool   `json:"allowPrivateNetwork,omitempty"`
}

type policySetForStorage struct {
	ID            string               `json:"id"`
	Scope         incident.PolicyScope `json:"scope"`
	Workspace     string               `json:"workspace,omitempty"`
	DAGName       string               `json:"dagName,omitempty"`
	Enabled       bool                 `json:"enabled"`
	InheritParent bool                 `json:"inheritParent"`
	Policies      []policyForStorage   `json:"policies"`
	CreatedAt     string               `json:"createdAt"`
	UpdatedAt     string               `json:"updatedAt"`
	UpdatedBy     string               `json:"updatedBy,omitempty"`
}

type policyForStorage struct {
	ID                  string            `json:"id"`
	ProviderID          string            `json:"providerId"`
	Enabled             bool              `json:"enabled"`
	Severity            incident.Severity `json:"severity"`
	ResolveOnRecovery   bool              `json:"resolveOnRecovery"`
	DedupKeyTemplate    string            `json:"dedupKeyTemplate,omitempty"`
	MessageTemplate     string            `json:"messageTemplate,omitempty"`
	DescriptionTemplate string            `json:"descriptionTemplate,omitempty"`
}

type stateForStorage struct {
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

func (s *Store) providerToStorage(provider *incident.Provider) (*providerForStorage, error) {
	stored := &providerForStorage{
		ID:        provider.ID,
		Name:      provider.Name,
		Type:      provider.Type,
		Enabled:   provider.Enabled,
		CreatedAt: provider.CreatedAt.Format(timeFormat),
		UpdatedAt: provider.UpdatedAt.Format(timeFormat),
		UpdatedBy: provider.UpdatedBy,
	}
	var err error
	if provider.PagerDuty != nil {
		stored.PagerDuty = &pagerDutyProviderStorage{}
		if stored.PagerDuty.RoutingKeyEnc, err = s.encryptRequired(provider.PagerDuty.RoutingKey); err != nil {
			return nil, err
		}
	}
	if provider.SolarWinds != nil {
		stored.SolarWinds = &solarWindsProviderStorage{
			AllowInsecureHTTP:   provider.SolarWinds.AllowInsecureHTTP,
			AllowPrivateNetwork: provider.SolarWinds.AllowPrivateNetwork,
		}
		if stored.SolarWinds.WebhookURLEnc, err = s.encryptRequired(provider.SolarWinds.WebhookURL); err != nil {
			return nil, err
		}
	}
	return stored, nil
}

func (s *Store) providerFromStorage(stored *providerForStorage) (*incident.Provider, error) {
	provider := &incident.Provider{
		ID:        stored.ID,
		Name:      stored.Name,
		Type:      stored.Type,
		Enabled:   stored.Enabled,
		CreatedAt: parseTime(stored.CreatedAt),
		UpdatedAt: parseTime(stored.UpdatedAt),
		UpdatedBy: stored.UpdatedBy,
	}
	var err error
	if stored.PagerDuty != nil {
		provider.PagerDuty = &incident.PagerDutyProvider{}
		if provider.PagerDuty.RoutingKey, err = s.decryptOptional(stored.PagerDuty.RoutingKeyEnc); err != nil {
			return nil, err
		}
	}
	if stored.SolarWinds != nil {
		provider.SolarWinds = &incident.SolarWindsProvider{
			AllowInsecureHTTP:   stored.SolarWinds.AllowInsecureHTTP,
			AllowPrivateNetwork: stored.SolarWinds.AllowPrivateNetwork,
		}
		if provider.SolarWinds.WebhookURL, err = s.decryptOptional(stored.SolarWinds.WebhookURLEnc); err != nil {
			return nil, err
		}
	}
	return provider, nil
}

func policySetToStorage(policySet *incident.PolicySet) *policySetForStorage {
	policies := make([]policyForStorage, 0, len(policySet.Policies))
	for _, policy := range policySet.Policies {
		policies = append(policies, policyForStorage(policy))
	}
	return &policySetForStorage{
		ID:            policySet.ID,
		Scope:         policySet.Scope,
		Workspace:     policySet.Workspace,
		DAGName:       policySet.DAGName,
		Enabled:       policySet.Enabled,
		InheritParent: policySet.InheritParent,
		Policies:      policies,
		CreatedAt:     policySet.CreatedAt.Format(timeFormat),
		UpdatedAt:     policySet.UpdatedAt.Format(timeFormat),
		UpdatedBy:     policySet.UpdatedBy,
	}
}

func policySetFromStorage(stored *policySetForStorage) *incident.PolicySet {
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
		CreatedAt:     parseTime(stored.CreatedAt),
		UpdatedAt:     parseTime(stored.UpdatedAt),
		UpdatedBy:     stored.UpdatedBy,
	}
}

func stateToStorage(state *incident.IncidentState) *stateForStorage {
	stored := &stateForStorage{
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
		OpenedAt:      state.OpenedAt.Format(timeFormat),
		UpdatedAt:     state.UpdatedAt.Format(timeFormat),
	}
	if !state.ResolvedAt.IsZero() {
		stored.ResolvedAt = state.ResolvedAt.Format(timeFormat)
	}
	return stored
}

func stateFromStorage(stored *stateForStorage) *incident.IncidentState {
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
		OpenedAt:      parseTime(stored.OpenedAt),
		ResolvedAt:    parseTime(stored.ResolvedAt),
		UpdatedAt:     parseTime(stored.UpdatedAt),
	}
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(timeFormat, value)
	if err != nil {
		slog.Default().Debug("Failed to parse incident timestamp",
			"value", value,
			"error", err,
		)
		return time.Time{}
	}
	return parsed
}

func (s *Store) encryptRequired(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", incident.ErrSecretStoreMissing
	}
	return s.encryptor.Encrypt(value)
}

func (s *Store) decryptOptional(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", incident.ErrSecretStoreMissing
	}
	return s.encryptor.Decrypt(value)
}
