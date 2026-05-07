// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"time"

	"github.com/google/uuid"
)

// User represents a user in the system.
type User struct {
	// ID is the unique identifier for the user (UUID).
	ID string `json:"id"`
	// Username is the unique login name.
	Username string `json:"username"`
	// PasswordHash is the bcrypt hash of the password.
	// Excluded from JSON serialization for security.
	PasswordHash string `json:"-"`
	// Role determines the user's permissions.
	Role Role `json:"role"`
	// WorkspaceAccess restricts access to selected workspaces.
	// Nil is treated as all-workspaces for backward compatibility.
	WorkspaceAccess *WorkspaceAccess `json:"workspace_access,omitempty"`
	// CreatedAt is the timestamp when the user was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the timestamp when the user was last modified.
	UpdatedAt time.Time `json:"updated_at"`

	// AuthProvider indicates how the user was created: "builtin" or "oidc".
	// Empty string is treated as "builtin" for backward compatibility.
	AuthProvider string `json:"auth_provider,omitempty"`
	// OIDCIssuer is the OIDC provider issuer URL (set when AuthProvider is "oidc").
	OIDCIssuer string `json:"oidc_issuer,omitempty"`
	// OIDCSubject is the unique subject identifier from the OIDC provider.
	OIDCSubject string `json:"oidc_subject,omitempty"`
	// IsDisabled indicates if the user account is disabled.
	// Disabled users cannot log in.
	IsDisabled bool `json:"is_disabled,omitempty"`
}

// NewUser creates a User with a new UUID and sets CreatedAt and UpdatedAt to the current UTC time.
// The provided username, passwordHash, and role are assigned to the corresponding fields.
func NewUser(username string, passwordHash string, role Role) *User {
	now := time.Now().UTC()
	return &User{
		ID:              uuid.Must(uuid.NewV7()).String(),
		Username:        username,
		PasswordHash:    passwordHash,
		Role:            role,
		WorkspaceAccess: AllWorkspaceAccess(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// UserForStorage is used for JSON serialization to persistent storage.
// It includes the password hash which is excluded from the regular User JSON.
type UserForStorage struct {
	ID              string           `json:"id"`
	Username        string           `json:"username"`
	PasswordHash    string           `json:"password_hash"`
	Role            Role             `json:"role"`
	WorkspaceAccess *WorkspaceAccess `json:"workspace_access,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	AuthProvider    string           `json:"auth_provider,omitempty"`
	OIDCIssuer      string           `json:"oidc_issuer,omitempty"`
	OIDCSubject     string           `json:"oidc_subject,omitempty"`
	IsDisabled      bool             `json:"is_disabled,omitempty"`
}

// ToStorage converts a User to UserForStorage for persistence.
func (u *User) ToStorage() *UserForStorage {
	return &UserForStorage{
		ID:              u.ID,
		Username:        u.Username,
		PasswordHash:    u.PasswordHash,
		Role:            u.Role,
		WorkspaceAccess: CloneWorkspaceAccess(u.WorkspaceAccess),
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
		AuthProvider:    u.AuthProvider,
		OIDCIssuer:      u.OIDCIssuer,
		OIDCSubject:     u.OIDCSubject,
		IsDisabled:      u.IsDisabled,
	}
}

// ToUser converts UserForStorage back to User.
func (s *UserForStorage) ToUser() *User {
	return &User{
		ID:              s.ID,
		Username:        s.Username,
		PasswordHash:    s.PasswordHash,
		Role:            s.Role,
		WorkspaceAccess: CloneWorkspaceAccess(s.WorkspaceAccess),
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		AuthProvider:    s.AuthProvider,
		OIDCIssuer:      s.OIDCIssuer,
		OIDCSubject:     s.OIDCSubject,
		IsDisabled:      s.IsDisabled,
	}
}

// IsOIDCUser returns true if the user was created via OIDC auto-signup.
func (u *User) IsOIDCUser() bool {
	return u.AuthProvider == "oidc"
}
