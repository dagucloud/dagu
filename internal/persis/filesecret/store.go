// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package filesecret provides an encrypted file-backed secret registry store.
package filesecret

import (
	"context"
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
	"github.com/dagucloud/dagu/internal/secret"
)

const (
	fileExtension   = ".json"
	dirPermissions  = 0750
	filePermissions = 0600
)

type Store struct {
	baseDir   string
	encryptor *crypto.Encryptor

	mu    sync.RWMutex
	byID  map[string]string
	byRef map[string]string
}

var _ secret.Store = (*Store)(nil)

type storedRecord struct {
	Secret   *secret.Secret  `json:"secret"`
	Versions []storedVersion `json:"versions,omitempty"`
}

type storedVersion struct {
	SecretID       string    `json:"secret_id"`
	Version        int       `json:"version"`
	EncryptedValue string    `json:"encrypted_value"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func New(baseDir string, enc *crypto.Encryptor) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("filesecret: baseDir cannot be empty")
	}
	if enc == nil {
		return nil, errors.New("filesecret: encryptor cannot be nil")
	}

	store := &Store{
		baseDir:   baseDir,
		encryptor: enc,
		byID:      make(map[string]string),
		byRef:     make(map[string]string),
	}
	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("filesecret: failed to create directory %s: %w", baseDir, err)
	}
	if err := store.rebuildIndex(); err != nil {
		return nil, fmt.Errorf("filesecret: failed to build index: %w", err)
	}
	return store, nil
}

func NewFromDataDir(dataDir string) (*Store, error) {
	key, err := crypto.ResolveKey(dataDir)
	if err != nil {
		return nil, err
	}
	enc, err := crypto.NewEncryptor(key)
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(dataDir, "secrets"), enc)
}

func (s *Store) rebuildIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID = make(map[string]string)
	s.byRef = make(map[string]string)

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", s.baseDir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != fileExtension {
			continue
		}
		filePath := filepath.Join(s.baseDir, entry.Name())
		record, err := loadRecordFromFile(filePath)
		if err != nil {
			slog.Warn("Failed to load secret file during index rebuild",
				slog.String("file", filePath),
				slog.String("error", err.Error()))
			continue
		}
		if record.Secret == nil || record.Secret.ID == "" {
			continue
		}
		s.byID[record.Secret.ID] = filePath
		s.byRef[refKey(record.Secret.Workspace, record.Secret.Ref)] = record.Secret.ID
	}
	return nil
}

func (s *Store) Create(_ context.Context, sec *secret.Secret, initialValue *secret.WriteValueInput) error {
	if sec == nil {
		return errors.New("filesecret: secret cannot be nil")
	}
	if sec.ID == "" {
		return secret.ErrInvalidSecretID
	}
	storedSecret := sec.Clone()
	storedSecret.Workspace = secret.NormalizeWorkspace(storedSecret.Workspace)
	if err := secret.ValidateWorkspace(storedSecret.Workspace); err != nil {
		return err
	}
	if err := secret.ValidateRef(storedSecret.Ref); err != nil {
		return err
	}
	if err := secret.ValidateProviderType(storedSecret.ProviderType); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byID[sec.ID]; exists {
		return secret.ErrAlreadyExists
	}
	ref := refKey(storedSecret.Workspace, storedSecret.Ref)
	if _, exists := s.byRef[ref]; exists {
		return secret.ErrAlreadyExists
	}

	record := &storedRecord{Secret: storedSecret}
	if initialValue != nil {
		if err := s.appendVersion(record, *initialValue); err != nil {
			return err
		}
	}

	filePath := s.secretFilePath(sec.ID)
	if err := writeRecordToFile(filePath, record); err != nil {
		return err
	}
	s.byID[sec.ID] = filePath
	s.byRef[ref] = sec.ID
	return nil
}

func (s *Store) GetByID(_ context.Context, id string) (*secret.Secret, error) {
	if id == "" {
		return nil, secret.ErrInvalidSecretID
	}
	record, err := s.recordByID(id)
	if err != nil {
		return nil, err
	}
	return record.Secret.Clone(), nil
}

func (s *Store) GetByRef(_ context.Context, workspaceName, ref string) (*secret.Secret, error) {
	workspaceName = secret.NormalizeWorkspace(workspaceName)
	if err := secret.ValidateWorkspace(workspaceName); err != nil {
		return nil, err
	}
	if err := secret.ValidateRef(ref); err != nil {
		return nil, err
	}

	s.mu.RLock()
	id, ok := s.byRef[refKey(workspaceName, ref)]
	s.mu.RUnlock()
	if !ok {
		return nil, secret.ErrNotFound
	}
	record, err := s.recordByID(id)
	if err != nil {
		return nil, err
	}
	return record.Secret.Clone(), nil
}

func (s *Store) List(ctx context.Context, opts secret.ListOptions) ([]*secret.Secret, error) {
	s.mu.RLock()
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	ret := make([]*secret.Secret, 0, len(ids))
	for _, id := range ids {
		sec, err := s.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, secret.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if opts.Workspace != nil && sec.Workspace != secret.NormalizeWorkspace(*opts.Workspace) {
			continue
		}
		ret = append(ret, sec)
	}
	sort.Slice(ret, func(i, j int) bool {
		if ret[i].Workspace != ret[j].Workspace {
			return ret[i].Workspace < ret[j].Workspace
		}
		return ret[i].Ref < ret[j].Ref
	})
	return ret, nil
}

func (s *Store) Update(_ context.Context, sec *secret.Secret) error {
	if sec == nil {
		return errors.New("filesecret: secret cannot be nil")
	}
	if sec.ID == "" {
		return secret.ErrInvalidSecretID
	}
	storedSecret := sec.Clone()
	storedSecret.Workspace = secret.NormalizeWorkspace(storedSecret.Workspace)
	if err := secret.ValidateWorkspace(storedSecret.Workspace); err != nil {
		return err
	}
	if err := secret.ValidateRef(storedSecret.Ref); err != nil {
		return err
	}
	if err := secret.ValidateProviderType(storedSecret.ProviderType); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, ok := s.byID[sec.ID]
	if !ok {
		return secret.ErrNotFound
	}
	record, err := loadRecordFromFile(filePath)
	if err != nil {
		return fmt.Errorf("filesecret: failed to load existing secret: %w", err)
	}

	oldRef := refKey(record.Secret.Workspace, record.Secret.Ref)
	newRef := refKey(storedSecret.Workspace, storedSecret.Ref)
	if oldRef != newRef {
		if existingID, taken := s.byRef[newRef]; taken && existingID != sec.ID {
			return secret.ErrAlreadyExists
		}
	}

	record.Secret = storedSecret
	if err := writeRecordToFile(filePath, record); err != nil {
		return err
	}
	if oldRef != newRef {
		delete(s.byRef, oldRef)
		s.byRef[newRef] = storedSecret.ID
	}
	return nil
}

func (s *Store) Delete(_ context.Context, id string) error {
	if id == "" {
		return secret.ErrInvalidSecretID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, ok := s.byID[id]
	if !ok {
		return secret.ErrNotFound
	}
	record, err := loadRecordFromFile(filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filesecret: failed to load secret before delete: %w", err)
	}
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filesecret: failed to delete secret %s: %w", id, err)
	}
	delete(s.byID, id)
	if record != nil && record.Secret != nil {
		delete(s.byRef, refKey(record.Secret.Workspace, record.Secret.Ref))
	}
	return nil
}

func (s *Store) WriteValue(_ context.Context, id string, input secret.WriteValueInput) (*secret.Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, ok := s.byID[id]
	if !ok {
		return nil, secret.ErrNotFound
	}
	record, err := loadRecordFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("filesecret: failed to load secret %s: %w", id, err)
	}
	if err := s.appendVersion(record, input); err != nil {
		return nil, err
	}
	if err := writeRecordToFile(filePath, record); err != nil {
		return nil, err
	}
	return record.Secret.Clone(), nil
}

func (s *Store) GetCurrentVersion(_ context.Context, id string) (*secret.VersionMetadata, error) {
	record, err := s.recordByID(id)
	if err != nil {
		return nil, err
	}
	version, ok := currentVersion(record)
	if !ok {
		return nil, secret.ErrNoValue
	}
	return versionMetadata(version), nil
}

func (s *Store) ResolveValue(_ context.Context, id string) (string, *secret.VersionMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, ok := s.byID[id]
	if !ok {
		return "", nil, secret.ErrNotFound
	}
	record, err := loadRecordFromFile(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("filesecret: failed to load secret %s: %w", id, err)
	}
	if record.Secret.Status == secret.StatusDisabled {
		return "", nil, secret.ErrDisabled
	}
	version, ok := currentVersion(record)
	if !ok {
		return "", nil, secret.ErrNoValue
	}
	value, err := s.encryptor.Decrypt(version.EncryptedValue)
	if err != nil {
		return "", nil, fmt.Errorf("filesecret: failed to decrypt secret %s version %d: %w", id, version.Version, err)
	}
	resolvedAt := time.Now().UTC()
	record.Secret.LastResolvedAt = &resolvedAt
	record.Secret.UpdatedAt = resolvedAt
	if err := writeRecordToFile(filePath, record); err != nil {
		return "", nil, err
	}
	return value, versionMetadata(version), nil
}

func (s *Store) appendVersion(record *storedRecord, input secret.WriteValueInput) error {
	if record.Secret.ProviderType != secret.ProviderDaguManaged {
		return secret.ErrUnsupportedProvider
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	encrypted, err := s.encryptor.Encrypt(input.Value)
	if err != nil {
		return fmt.Errorf("filesecret: failed to encrypt secret value: %w", err)
	}
	version := record.Secret.CurrentVersion + 1
	record.Versions = append(record.Versions, storedVersion{
		SecretID:       record.Secret.ID,
		Version:        version,
		EncryptedValue: encrypted,
		CreatedBy:      input.CreatedBy,
		CreatedAt:      input.CreatedAt,
	})
	record.Secret.CurrentVersion = version
	record.Secret.LastRotatedAt = &input.CreatedAt
	record.Secret.UpdatedBy = input.CreatedBy
	record.Secret.UpdatedAt = input.CreatedAt
	return nil
}

func (s *Store) recordByID(id string) (*storedRecord, error) {
	s.mu.RLock()
	filePath, ok := s.byID[id]
	s.mu.RUnlock()
	if !ok {
		return nil, secret.ErrNotFound
	}
	record, err := loadRecordFromFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, secret.ErrNotFound
		}
		return nil, fmt.Errorf("filesecret: failed to load secret %s: %w", id, err)
	}
	return record, nil
}

func (s *Store) secretFilePath(id string) string {
	return filepath.Join(s.baseDir, id+fileExtension)
}

func loadRecordFromFile(filePath string) (*storedRecord, error) {
	data, err := os.ReadFile(filePath) //nolint:gosec // filePath is constructed internally
	if err != nil {
		return nil, err
	}
	var record storedRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("failed to parse secret file %s: %w", filePath, err)
	}
	if record.Secret == nil {
		return nil, fmt.Errorf("secret file %s is missing metadata", filePath)
	}
	record.Secret.Workspace = secret.NormalizeWorkspace(record.Secret.Workspace)
	return &record, nil
}

func writeRecordToFile(filePath string, record *storedRecord) error {
	if err := fileutil.WriteJSONAtomic(filePath, record, filePermissions); err != nil {
		return fmt.Errorf("filesecret: %w", err)
	}
	return nil
}

func currentVersion(record *storedRecord) (storedVersion, bool) {
	for _, version := range record.Versions {
		if version.Version == record.Secret.CurrentVersion {
			return version, true
		}
	}
	return storedVersion{}, false
}

func versionMetadata(version storedVersion) *secret.VersionMetadata {
	return &secret.VersionMetadata{
		SecretID:  version.SecretID,
		Version:   version.Version,
		CreatedBy: version.CreatedBy,
		CreatedAt: version.CreatedAt,
	}
}

func refKey(workspace, ref string) string {
	return workspace + "\x00" + ref
}
