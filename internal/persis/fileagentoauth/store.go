// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileagentoauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
)

const (
	fileExtension   = ".json"
	dirPermissions  = 0750
	filePermissions = 0600
)

type storedCredential struct {
	Provider        string `json:"provider"`
	AccessTokenEnc  string `json:"accessTokenEnc,omitempty"`
	RefreshTokenEnc string `json:"refreshTokenEnc,omitempty"`
	ExpiresAt       string `json:"expiresAt,omitempty"`
	AccountID       string `json:"accountId,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
}

// Store persists encrypted OAuth credentials per provider.
type Store struct {
	baseDir   string
	encryptor *crypto.Encryptor

	mu sync.RWMutex
}

var _ agentoauth.Store = (*Store)(nil)

func New(baseDir string, enc *crypto.Encryptor) (*Store, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("fileagentoauth: baseDir cannot be empty")
	}
	if enc == nil {
		return nil, errors.New("fileagentoauth: encryptor cannot be nil")
	}
	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("fileagentoauth: failed to create directory %s: %w", baseDir, err)
	}
	return &Store{baseDir: baseDir, encryptor: enc}, nil
}

func (s *Store) Get(_ context.Context, provider string) (*agentoauth.Credential, error) {
	path, err := s.credentialPath(provider)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := fileutil.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, agentoauth.ErrCredentialNotFound
		}
		return nil, fmt.Errorf("fileagentoauth: read credential: %w", err)
	}

	var stored storedCredential
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("fileagentoauth: decode credential: %w", err)
	}
	return s.decryptStored(&stored)
}

func (s *Store) Set(_ context.Context, cred *agentoauth.Credential) error {
	if cred == nil {
		return errors.New("fileagentoauth: credential cannot be nil")
	}
	path, err := s.credentialPath(cred.Provider)
	if err != nil {
		return err
	}

	stored, err := s.encryptCredential(cred)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := fileutil.WriteJSONAtomic(path, stored, filePermissions); err != nil {
		return fmt.Errorf("fileagentoauth: %w", err)
	}
	return nil
}

func (s *Store) Delete(_ context.Context, provider string) error {
	path, err := s.credentialPath(provider)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := fileutil.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("fileagentoauth: delete credential: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context) ([]*agentoauth.Credential, error) {
	s.mu.RLock()
	entries, err := os.ReadDir(s.baseDir)
	s.mu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("fileagentoauth: list directory: %w", err)
	}

	var providers []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != fileExtension {
			continue
		}
		providers = append(providers, strings.TrimSuffix(entry.Name(), fileExtension))
	}
	sort.Strings(providers)

	result := make([]*agentoauth.Credential, 0, len(providers))
	for _, provider := range providers {
		cred, err := s.Get(ctx, provider)
		switch {
		case err == nil:
			result = append(result, cred)
		case errors.Is(err, agentoauth.ErrCredentialNotFound):
			continue
		default:
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) credentialPath(provider string) (string, error) {
	if err := validateProviderName(provider); err != nil {
		return "", err
	}
	path := filepath.Join(s.baseDir, provider+fileExtension)
	base := filepath.Clean(s.baseDir) + string(filepath.Separator)
	if !strings.HasPrefix(path, base) {
		return "", fmt.Errorf("fileagentoauth: path traversal detected for provider %q", provider)
	}
	return path, nil
}

func validateProviderName(provider string) error {
	if strings.TrimSpace(provider) == "" {
		return fmt.Errorf("fileagentoauth: provider is required")
	}
	for _, r := range provider {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("fileagentoauth: invalid provider %q", provider)
		}
	}
	return nil
}

func (s *Store) encryptCredential(cred *agentoauth.Credential) (*storedCredential, error) {
	accessTokenEnc, err := s.encryptor.Encrypt(cred.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("fileagentoauth: encrypt access token: %w", err)
	}
	refreshTokenEnc, err := s.encryptor.Encrypt(cred.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("fileagentoauth: encrypt refresh token: %w", err)
	}

	return &storedCredential{
		Provider:        cred.Provider,
		AccessTokenEnc:  accessTokenEnc,
		RefreshTokenEnc: refreshTokenEnc,
		ExpiresAt:       cred.ExpiresAt.UTC().Format(timeLayout),
		AccountID:       cred.AccountID,
		UpdatedAt:       cred.UpdatedAt.UTC().Format(timeLayout),
	}, nil
}

func (s *Store) decryptStored(stored *storedCredential) (*agentoauth.Credential, error) {
	accessToken, err := s.encryptor.Decrypt(stored.AccessTokenEnc)
	if err != nil {
		return nil, fmt.Errorf("fileagentoauth: decrypt access token: %w", err)
	}
	refreshToken, err := s.encryptor.Decrypt(stored.RefreshTokenEnc)
	if err != nil {
		return nil, fmt.Errorf("fileagentoauth: decrypt refresh token: %w", err)
	}

	cred := &agentoauth.Credential{
		Provider:     stored.Provider,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		AccountID:    stored.AccountID,
	}
	if stored.ExpiresAt != "" {
		if expiresAt, err := parseTime(stored.ExpiresAt); err == nil {
			cred.ExpiresAt = expiresAt
		} else {
			slog.Warn("fileagentoauth: failed to parse timestamp", slog.String("field", "expiresAt"), slog.String("value", stored.ExpiresAt), slog.Any("error", err))
		}
	}
	if stored.UpdatedAt != "" {
		if updatedAt, err := parseTime(stored.UpdatedAt); err == nil {
			cred.UpdatedAt = updatedAt
		} else {
			slog.Warn("fileagentoauth: failed to parse timestamp", slog.String("field", "updatedAt"), slog.String("value", stored.UpdatedAt), slog.Any("error", err))
		}
	}
	return cred, nil
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

func parseTime(value string) (t time.Time, err error) {
	return time.Parse(timeLayout, value)
}
