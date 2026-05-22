// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// APIKeySurface is an interface where an API key may be accepted.
type APIKeySurface string

const (
	// APIKeySurfaceREST allows use against the REST API and related HTTP surfaces.
	APIKeySurfaceREST APIKeySurface = "rest_api"
	// APIKeySurfaceMCP allows use against the MCP endpoint.
	APIKeySurfaceMCP APIKeySurface = "mcp"
)

// APIKeyAttributionClass describes the identity quality of an API key.
type APIKeyAttributionClass string

const (
	// APIKeyAttributionUserOwned means the key is attributable to a user owner.
	APIKeyAttributionUserOwned APIKeyAttributionClass = "user_owned"
	// APIKeyAttributionServiceAccount means the key is attributable to a service identity.
	APIKeyAttributionServiceAccount APIKeyAttributionClass = "service_account"
)

// APIKey represents a standalone API key in the system.
// API keys are independent entities with their own role assignment,
// enabling programmatic access with fine-grained permissions.
type APIKey struct {
	// ID is the unique identifier for the API key (UUID).
	ID string `json:"id"`
	// Name is a human-readable name for the API key (required).
	Name string `json:"name"`
	// Description is an optional description of the API key's purpose.
	Description string `json:"description,omitempty"`
	// Role determines the API key's permissions.
	Role Role `json:"role"`
	// WorkspaceAccess restricts access to selected workspaces.
	// Nil is treated as all-workspaces for backward compatibility.
	WorkspaceAccess *WorkspaceAccess `json:"workspace_access,omitempty"`
	// AllowedSurfaces controls which public interfaces accept this key.
	// Empty is treated as REST API and MCP for backward compatibility.
	AllowedSurfaces []APIKeySurface `json:"allowed_surfaces,omitempty"`
	// AttributionClass determines whether the key is user-owned or a service account.
	AttributionClass APIKeyAttributionClass `json:"attribution_class,omitempty"`
	// OwnerUserID is populated when AttributionClass is user_owned.
	OwnerUserID string `json:"owner_user_id,omitempty"`
	// OwnerUsername is populated when AttributionClass is user_owned.
	OwnerUsername string `json:"owner_username,omitempty"`
	// ServiceAccountID is populated when AttributionClass is service_account.
	ServiceAccountID string `json:"service_account_id,omitempty"`
	// ServiceAccountName is populated when AttributionClass is service_account.
	ServiceAccountName string `json:"service_account_name,omitempty"`
	// MigratedAsServiceAccount is true when missing legacy attribution defaults were applied.
	MigratedAsServiceAccount bool `json:"migrated_as_service_account,omitempty"`
	// KeyHash is the bcrypt hash of the API key secret.
	// Excluded from JSON serialization for security.
	KeyHash string `json:"-"`
	// KeyPrefix stores the first 8 characters of the key for identification.
	KeyPrefix string `json:"key_prefix"`
	// CreatedAt is the timestamp when the API key was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the timestamp when the API key was last modified.
	UpdatedAt time.Time `json:"updated_at"`
	// CreatedBy is the user ID of the admin who created the API key.
	CreatedBy string `json:"created_by"`
	// LastUsedAt is the timestamp when the API key was last used for authentication.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// NewAPIKey creates an APIKey with a new UUID and sets CreatedAt and UpdatedAt to the current UTC time.
// It validates that required fields are not empty and the role is valid.
// Returns an error if validation fails.
func NewAPIKey(name, description string, role Role, keyHash, keyPrefix, createdBy string) (*APIKey, error) {
	if name == "" {
		return nil, ErrInvalidAPIKeyName
	}
	if keyHash == "" {
		return nil, ErrInvalidAPIKeyHash
	}
	if !role.Valid() {
		return nil, ErrInvalidRole
	}
	now := time.Now().UTC()
	return &APIKey{
		ID:               uuid.New().String(),
		Name:             name,
		Description:      description,
		Role:             role,
		WorkspaceAccess:  AllWorkspaceAccess(),
		AllowedSurfaces:  DefaultAPIKeySurfaces(),
		AttributionClass: APIKeyAttributionServiceAccount,
		KeyHash:          keyHash,
		KeyPrefix:        keyPrefix,
		CreatedAt:        now,
		UpdatedAt:        now,
		CreatedBy:        createdBy,
	}, nil
}

// APIKeyForStorage is used for JSON serialization to persistent storage.
// It includes the key hash which is excluded from the regular APIKey JSON.
type APIKeyForStorage struct {
	ID                       string                 `json:"id"`
	Name                     string                 `json:"name"`
	Description              string                 `json:"description,omitempty"`
	Role                     Role                   `json:"role"`
	WorkspaceAccess          *WorkspaceAccess       `json:"workspace_access,omitempty"`
	AllowedSurfaces          []APIKeySurface        `json:"allowed_surfaces,omitempty"`
	AttributionClass         APIKeyAttributionClass `json:"attribution_class,omitempty"`
	OwnerUserID              string                 `json:"owner_user_id,omitempty"`
	OwnerUsername            string                 `json:"owner_username,omitempty"`
	ServiceAccountID         string                 `json:"service_account_id,omitempty"`
	ServiceAccountName       string                 `json:"service_account_name,omitempty"`
	MigratedAsServiceAccount bool                   `json:"migrated_as_service_account,omitempty"`
	KeyHash                  string                 `json:"key_hash"`
	KeyPrefix                string                 `json:"key_prefix"`
	CreatedAt                time.Time              `json:"created_at"`
	UpdatedAt                time.Time              `json:"updated_at"`
	CreatedBy                string                 `json:"created_by"`
	LastUsedAt               *time.Time             `json:"last_used_at,omitempty"`
}

// ToStorage converts an APIKey to APIKeyForStorage for persistence.
// NOTE: When adding new fields to APIKey or APIKeyForStorage, ensure both
// ToStorage and ToAPIKey are updated to maintain field synchronization.
func (k *APIKey) ToStorage() *APIKeyForStorage {
	normalized := NormalizeAPIKeyMetadata(k)
	return &APIKeyForStorage{
		ID:                       normalized.ID,
		Name:                     normalized.Name,
		Description:              normalized.Description,
		Role:                     normalized.Role,
		WorkspaceAccess:          CloneWorkspaceAccess(normalized.WorkspaceAccess),
		AllowedSurfaces:          CloneAPIKeySurfaces(normalized.AllowedSurfaces),
		AttributionClass:         normalized.AttributionClass,
		OwnerUserID:              normalized.OwnerUserID,
		OwnerUsername:            normalized.OwnerUsername,
		ServiceAccountID:         normalized.ServiceAccountID,
		ServiceAccountName:       normalized.ServiceAccountName,
		MigratedAsServiceAccount: normalized.MigratedAsServiceAccount,
		KeyHash:                  normalized.KeyHash,
		KeyPrefix:                normalized.KeyPrefix,
		CreatedAt:                normalized.CreatedAt,
		UpdatedAt:                normalized.UpdatedAt,
		CreatedBy:                normalized.CreatedBy,
		LastUsedAt:               normalized.LastUsedAt,
	}
}

// ToAPIKey converts APIKeyForStorage back to APIKey.
// NOTE: When adding new fields to APIKey or APIKeyForStorage, ensure both
// ToStorage and ToAPIKey are updated to maintain field synchronization.
func (s *APIKeyForStorage) ToAPIKey() *APIKey {
	return NormalizeAPIKeyMetadata(&APIKey{
		ID:                       s.ID,
		Name:                     s.Name,
		Description:              s.Description,
		Role:                     s.Role,
		WorkspaceAccess:          CloneWorkspaceAccess(s.WorkspaceAccess),
		AllowedSurfaces:          CloneAPIKeySurfaces(s.AllowedSurfaces),
		AttributionClass:         s.AttributionClass,
		OwnerUserID:              s.OwnerUserID,
		OwnerUsername:            s.OwnerUsername,
		ServiceAccountID:         s.ServiceAccountID,
		ServiceAccountName:       s.ServiceAccountName,
		MigratedAsServiceAccount: s.MigratedAsServiceAccount,
		KeyHash:                  s.KeyHash,
		KeyPrefix:                s.KeyPrefix,
		CreatedAt:                s.CreatedAt,
		UpdatedAt:                s.UpdatedAt,
		CreatedBy:                s.CreatedBy,
		LastUsedAt:               s.LastUsedAt,
	})
}

// DefaultAPIKeySurfaces returns the legacy-compatible surface allowlist.
func DefaultAPIKeySurfaces() []APIKeySurface {
	return []APIKeySurface{APIKeySurfaceREST, APIKeySurfaceMCP}
}

// CloneAPIKeySurfaces returns a normalized copy of API key surfaces.
func CloneAPIKeySurfaces(surfaces []APIKeySurface) []APIKeySurface {
	normalized := NormalizeAPIKeySurfaces(surfaces)
	out := make([]APIKeySurface, len(normalized))
	copy(out, normalized)
	return out
}

// NormalizeAPIKeySurfaces returns a stable allowlist, defaulting legacy keys to both surfaces.
func NormalizeAPIKeySurfaces(surfaces []APIKeySurface) []APIKeySurface {
	if len(surfaces) == 0 {
		return DefaultAPIKeySurfaces()
	}
	seen := make(map[APIKeySurface]struct{}, len(surfaces))
	out := make([]APIKeySurface, 0, len(surfaces))
	for _, surface := range surfaces {
		switch surface {
		case APIKeySurfaceREST, APIKeySurfaceMCP:
			if _, ok := seen[surface]; ok {
				continue
			}
			seen[surface] = struct{}{}
			out = append(out, surface)
		}
	}
	if len(out) == 0 {
		return DefaultAPIKeySurfaces()
	}
	return out
}

// ValidAPIKeySurface reports whether surface is a known API-key surface.
func ValidAPIKeySurface(surface APIKeySurface) bool {
	switch surface {
	case APIKeySurfaceREST, APIKeySurfaceMCP:
		return true
	default:
		return false
	}
}

// HasAPIKeySurface reports whether a normalized surface allowlist contains a surface.
func HasAPIKeySurface(surfaces []APIKeySurface, surface APIKeySurface) bool {
	return slices.Contains(NormalizeAPIKeySurfaces(surfaces), surface)
}

// NormalizeAPIKeyMetadata returns a copy with legacy attribution and surface defaults applied.
func NormalizeAPIKeyMetadata(key *APIKey) *APIKey {
	if key == nil {
		return nil
	}
	normalized := *key
	normalized.WorkspaceAccess = CloneWorkspaceAccess(key.WorkspaceAccess)
	normalized.AllowedSurfaces = CloneAPIKeySurfaces(key.AllowedSurfaces)

	if normalized.AttributionClass == "" {
		normalized.AttributionClass = APIKeyAttributionServiceAccount
		normalized.MigratedAsServiceAccount = true
	}
	if normalized.AttributionClass == APIKeyAttributionServiceAccount {
		if strings.TrimSpace(normalized.ServiceAccountID) == "" {
			identifier := normalized.ID
			if identifier == "" {
				identifier = normalized.Name
			}
			normalized.ServiceAccountID = "api_key:" + identifier
		}
		if strings.TrimSpace(normalized.ServiceAccountName) == "" {
			normalized.ServiceAccountName = normalized.Name
		}
	}
	return &normalized
}
