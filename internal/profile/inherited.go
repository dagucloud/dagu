// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/google/uuid"
)

const (
	inheritedGlobalStorageName     = "_global"
	inheritedWorkspaceNamePrefix   = "_workspace."
	inheritedWorkspacePublicPrefix = "_workspaces/"
)

// InheritedRef identifies a non-selectable profile layer inherited by a run.
type InheritedRef struct {
	storageName string
	publicName  string
	secretScope string
}

type InheritedCreateInput struct {
	Description string
	CreatedBy   string
}

func GlobalInheritedRef() InheritedRef {
	return InheritedRef{
		storageName: inheritedGlobalStorageName,
		publicName:  inheritedGlobalStorageName,
		secretScope: "global",
	}
}

func WorkspaceInheritedRef(name string) (InheritedRef, error) {
	trimmed := strings.TrimSpace(name)
	if err := workspace.ValidateName(trimmed); err != nil {
		return InheritedRef{}, err
	}
	encoded := hex.EncodeToString([]byte(trimmed))
	return InheritedRef{
		storageName: inheritedWorkspaceNamePrefix + encoded,
		publicName:  inheritedWorkspacePublicPrefix + trimmed,
		secretScope: "workspaces/" + encoded,
	}, nil
}

func (r InheritedRef) StorageName() string {
	return r.storageName
}

func (r InheritedRef) PublicName() string {
	return r.publicName
}

func (r InheritedRef) SecretRef(key string) string {
	return fmt.Sprintf("runtime-profile-defaults/%s/key-%s", r.secretScope, hex.EncodeToString([]byte(key)))
}

func (r InheritedRef) Valid() bool {
	return r.storageName != "" && r.publicName != "" && r.secretScope != ""
}

func NewInherited(ref InheritedRef, input InheritedCreateInput, now time.Time) (*Profile, error) {
	if !ref.Valid() {
		return nil, fmt.Errorf("%w: inherited profile ref is empty", ErrInvalidName)
	}
	if !IsInheritedStorageName(ref.StorageName()) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, ref.StorageName())
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return &Profile{
		ID:          uuid.NewString(),
		Name:        ref.StorageName(),
		Description: input.Description,
		Status:      StatusActive,
		Protected:   true,
		CreatedBy:   input.CreatedBy,
		CreatedAt:   now,
		UpdatedBy:   input.CreatedBy,
		UpdatedAt:   now,
	}, nil
}

func IsInheritedStorageName(name string) bool {
	if name == inheritedGlobalStorageName {
		return true
	}
	if !strings.HasPrefix(name, inheritedWorkspaceNamePrefix) {
		return false
	}
	encoded := strings.TrimPrefix(name, inheritedWorkspaceNamePrefix)
	if encoded == "" {
		return false
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		return false
	}
	return workspace.ValidateName(string(decoded)) == nil
}

func SecretRefForProfileName(profileName, key string) string {
	switch {
	case profileName == inheritedGlobalStorageName:
		return GlobalInheritedRef().SecretRef(key)
	case strings.HasPrefix(profileName, inheritedWorkspaceNamePrefix):
		return fmt.Sprintf("runtime-profile-defaults/workspaces/%s/key-%s",
			strings.TrimPrefix(profileName, inheritedWorkspaceNamePrefix),
			hex.EncodeToString([]byte(key)))
	default:
		return SecretRef(profileName, key)
	}
}
