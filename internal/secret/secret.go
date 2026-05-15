// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package secret contains the team secret registry domain model.
package secret

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/google/uuid"
)

type ProviderType string

const (
	ProviderDaguManaged      ProviderType = "dagu-managed"
	ProviderVault            ProviderType = "vault"
	ProviderKubernetes       ProviderType = "kubernetes"
	ProviderGCPSecretManager ProviderType = "gcp-secret-manager"
	ProviderAWSSecrets       ProviderType = "aws-secrets-manager"
	ProviderAzureKeyVault    ProviderType = "azure-key-vault"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

var (
	ErrAlreadyExists       = errors.New("secret already exists")
	ErrDisabled            = errors.New("secret is disabled")
	ErrInvalidProviderType = errors.New("invalid secret provider type")
	ErrInvalidRef          = errors.New("invalid secret ref")
	ErrInvalidSecretID     = errors.New("invalid secret id")
	ErrInvalidStatus       = errors.New("invalid secret status")
	ErrInvalidWorkspace    = errors.New("invalid secret workspace")
	ErrNoValue             = errors.New("secret has no value")
	ErrNotFound            = errors.New("secret not found")
	ErrUnsupportedProvider = errors.New("secret provider is not supported for registry resolution")
)

var secretRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)*$`)

// Secret is the registry metadata for a workspace-local secret reference.
// It never contains plaintext secret values.
type Secret struct {
	ID                     string
	Workspace              string
	Ref                    string
	DisplayName            string
	Description            string
	ProviderType           ProviderType
	ProviderConnectionID   string
	ProviderRef            string
	ProviderRefFingerprint string
	CurrentVersion         int
	Status                 Status
	CreatedBy              string
	CreatedAt              time.Time
	UpdatedBy              string
	UpdatedAt              time.Time
	LastCheckedAt          *time.Time
	LastResolvedAt         *time.Time
	LastRotatedAt          *time.Time
}

type VersionMetadata struct {
	SecretID  string
	Version   int
	CreatedBy string
	CreatedAt time.Time
}

type CreateInput struct {
	Workspace              string
	Ref                    string
	DisplayName            string
	Description            string
	ProviderType           ProviderType
	ProviderConnectionID   string
	ProviderRef            string
	ProviderRefFingerprint string
	CreatedBy              string
}

type UpdateInput struct {
	DisplayName            *string
	Description            *string
	ProviderConnectionID   *string
	ProviderRef            *string
	ProviderRefFingerprint *string
	UpdatedBy              string
}

type WriteValueInput struct {
	Value     string
	CreatedBy string
	CreatedAt time.Time
}

func New(input CreateInput, now time.Time) (*Secret, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := ValidateWorkspace(input.Workspace); err != nil {
		return nil, err
	}
	if err := ValidateRef(input.Ref); err != nil {
		return nil, err
	}
	if err := ValidateProviderType(input.ProviderType); err != nil {
		return nil, err
	}

	return &Secret{
		ID:                     uuid.NewString(),
		Workspace:              input.Workspace,
		Ref:                    input.Ref,
		DisplayName:            input.DisplayName,
		Description:            input.Description,
		ProviderType:           input.ProviderType,
		ProviderConnectionID:   input.ProviderConnectionID,
		ProviderRef:            input.ProviderRef,
		ProviderRefFingerprint: input.ProviderRefFingerprint,
		Status:                 StatusActive,
		CreatedBy:              input.CreatedBy,
		CreatedAt:              now,
		UpdatedBy:              input.CreatedBy,
		UpdatedAt:              now,
	}, nil
}

func (s *Secret) Clone() *Secret {
	if s == nil {
		return nil
	}
	clone := *s
	clone.LastCheckedAt = cloneTimePtr(s.LastCheckedAt)
	clone.LastResolvedAt = cloneTimePtr(s.LastResolvedAt)
	clone.LastRotatedAt = cloneTimePtr(s.LastRotatedAt)
	return &clone
}

func (s *Secret) ApplyUpdate(input UpdateInput, now time.Time) {
	if input.DisplayName != nil {
		s.DisplayName = *input.DisplayName
	}
	if input.Description != nil {
		s.Description = *input.Description
	}
	if input.ProviderConnectionID != nil {
		s.ProviderConnectionID = *input.ProviderConnectionID
	}
	if input.ProviderRef != nil {
		s.ProviderRef = *input.ProviderRef
	}
	if input.ProviderRefFingerprint != nil {
		s.ProviderRefFingerprint = *input.ProviderRefFingerprint
	}
	s.UpdatedBy = input.UpdatedBy
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.UpdatedAt = now
}

func (s *Secret) SetStatus(status Status, actor string, now time.Time) error {
	if status != StatusActive && status != StatusDisabled {
		return ErrInvalidStatus
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.Status = status
	s.UpdatedBy = actor
	s.UpdatedAt = now
	return nil
}

func ValidateWorkspace(name string) error {
	if name == "" {
		return nil
	}
	if err := workspace.ValidateName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidWorkspace, err)
	}
	return nil
}

func ValidateRef(ref string) error {
	if !secretRefPattern.MatchString(ref) {
		return fmt.Errorf("%w: must be a slash-separated lowercase slug ref", ErrInvalidRef)
	}
	return nil
}

func ValidateProviderType(providerType ProviderType) error {
	switch providerType {
	case ProviderDaguManaged, ProviderVault, ProviderKubernetes, ProviderGCPSecretManager, ProviderAWSSecrets, ProviderAzureKeyVault:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidProviderType, providerType)
	}
}

func ProviderRefFingerprint(key string, providerType ProviderType, providerConnectionID, providerRef string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("fingerprint key cannot be empty")
	}
	if providerRef == "" {
		return "", nil
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(string(providerType)))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(providerConnectionID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(providerRef))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func cloneTimePtr(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
