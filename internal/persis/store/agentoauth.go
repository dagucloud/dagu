// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis"
)

// agentOAuthTimeLayout matches the released on-disk format for ExpiresAt /
// UpdatedAt timestamps. RFC3339Nano with the optional fractional seconds is
// preserved bit-for-bit through the adapter.
const agentOAuthTimeLayout = "2006-01-02T15:04:05.999999999Z07:00"

type agentOAuthStoredCredential struct {
	Provider        string `json:"provider"`
	AccessTokenEnc  string `json:"accessTokenEnc,omitempty"`
	RefreshTokenEnc string `json:"refreshTokenEnc,omitempty"`
	ExpiresAt       string `json:"expiresAt,omitempty"`
	AccountID       string `json:"accountId,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
}

var _ agentoauth.Store = (*AgentOAuthStore)(nil)

// AgentOAuthStore implements [agentoauth.Store] by persisting one encrypted
// credential record per provider in a [persis.Collection].
type AgentOAuthStore struct {
	col       persis.Collection
	encryptor *crypto.Encryptor

	mu sync.RWMutex
}

// NewAgentOAuthStore creates an AgentOAuthStore backed by col.
func NewAgentOAuthStore(col persis.Collection, enc *crypto.Encryptor) (*AgentOAuthStore, error) {
	if enc == nil {
		return nil, errors.New("agent-oauth store: encryptor cannot be nil")
	}
	return &AgentOAuthStore{col: col, encryptor: enc}, nil
}

// Get returns the credential for the given provider, decrypted.
func (s *AgentOAuthStore) Get(ctx context.Context, provider string) (*agentoauth.Credential, error) {
	if err := validateAgentOAuthProvider(provider); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, err := s.col.Get(ctx, provider)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, agentoauth.ErrCredentialNotFound
		}
		return nil, fmt.Errorf("agent-oauth store: get: %w", err)
	}
	var stored agentOAuthStoredCredential
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("agent-oauth store: decode: %w", err)
	}
	return s.decryptStored(&stored)
}

// Set stores cred encrypted.
func (s *AgentOAuthStore) Set(ctx context.Context, cred *agentoauth.Credential) error {
	if cred == nil {
		return errors.New("agent-oauth store: credential cannot be nil")
	}
	if err := validateAgentOAuthProvider(cred.Provider); err != nil {
		return err
	}
	stored, err := s.encryptCredential(cred)
	if err != nil {
		return err
	}
	data, err := persis.Encode(stored)
	if err != nil {
		return fmt.Errorf("agent-oauth store: encode: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.col.Put(ctx, &persis.Record{ID: cred.Provider, Data: data}); err != nil {
		return fmt.Errorf("agent-oauth store: set: %w", err)
	}
	return nil
}

// Delete removes the credential for the given provider. Missing record is a no-op.
func (s *AgentOAuthStore) Delete(ctx context.Context, provider string) error {
	if err := validateAgentOAuthProvider(provider); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.col.Delete(ctx, provider); err != nil {
		return fmt.Errorf("agent-oauth store: delete: %w", err)
	}
	return nil
}

// List returns all credentials sorted by provider.
func (s *AgentOAuthStore) List(ctx context.Context) ([]*agentoauth.Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, fmt.Errorf("agent-oauth store: list: %w", err)
	}
	providers := make([]string, 0, len(recs))
	credByProvider := make(map[string]*agentoauth.Credential, len(recs))
	for _, rec := range recs {
		var stored agentOAuthStoredCredential
		if err := persis.Decode(rec, &stored); err != nil {
			continue
		}
		cred, err := s.decryptStored(&stored)
		if err != nil {
			return nil, err
		}
		providers = append(providers, rec.ID)
		credByProvider[rec.ID] = cred
	}
	sort.Strings(providers)
	out := make([]*agentoauth.Credential, 0, len(providers))
	for _, p := range providers {
		out = append(out, credByProvider[p])
	}
	return out, nil
}

func (s *AgentOAuthStore) encryptCredential(cred *agentoauth.Credential) (*agentOAuthStoredCredential, error) {
	accessTokenEnc, err := s.encryptor.Encrypt(cred.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("agent-oauth store: encrypt access token: %w", err)
	}
	refreshTokenEnc, err := s.encryptor.Encrypt(cred.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("agent-oauth store: encrypt refresh token: %w", err)
	}
	return &agentOAuthStoredCredential{
		Provider:        cred.Provider,
		AccessTokenEnc:  accessTokenEnc,
		RefreshTokenEnc: refreshTokenEnc,
		ExpiresAt:       cred.ExpiresAt.UTC().Format(agentOAuthTimeLayout),
		AccountID:       cred.AccountID,
		UpdatedAt:       cred.UpdatedAt.UTC().Format(agentOAuthTimeLayout),
	}, nil
}

func (s *AgentOAuthStore) decryptStored(stored *agentOAuthStoredCredential) (*agentoauth.Credential, error) {
	accessToken, err := s.encryptor.Decrypt(stored.AccessTokenEnc)
	if err != nil {
		return nil, fmt.Errorf("agent-oauth store: decrypt access token: %w", err)
	}
	refreshToken, err := s.encryptor.Decrypt(stored.RefreshTokenEnc)
	if err != nil {
		return nil, fmt.Errorf("agent-oauth store: decrypt refresh token: %w", err)
	}
	cred := &agentoauth.Credential{
		Provider:     stored.Provider,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		AccountID:    stored.AccountID,
	}
	if stored.ExpiresAt != "" {
		if t, err := time.Parse(agentOAuthTimeLayout, stored.ExpiresAt); err == nil {
			cred.ExpiresAt = t
		} else {
			slog.Warn("agent-oauth store: failed to parse timestamp",
				slog.String("field", "expiresAt"),
				slog.String("value", stored.ExpiresAt),
				slog.Any("error", err))
		}
	}
	if stored.UpdatedAt != "" {
		if t, err := time.Parse(agentOAuthTimeLayout, stored.UpdatedAt); err == nil {
			cred.UpdatedAt = t
		} else {
			slog.Warn("agent-oauth store: failed to parse timestamp",
				slog.String("field", "updatedAt"),
				slog.String("value", stored.UpdatedAt),
				slog.Any("error", err))
		}
	}
	return cred, nil
}

func validateAgentOAuthProvider(provider string) error {
	if provider == "" {
		return fmt.Errorf("agent-oauth store: provider is required")
	}
	for _, r := range provider {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("agent-oauth store: invalid provider %q", provider)
		}
	}
	return nil
}
