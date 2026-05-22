// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"errors"
)

// Common errors for user store operations.
var (
	// ErrUserNotFound is returned when a user cannot be found.
	ErrUserNotFound = errors.New("user not found")
	// ErrUserAlreadyExists is returned when attempting to create a user
	// with a username that already exists.
	ErrUserAlreadyExists = errors.New("user already exists")
	// ErrInvalidUsername is returned when the username is invalid.
	ErrInvalidUsername = errors.New("invalid username")
	// ErrInvalidUserID is returned when the user ID is invalid.
	ErrInvalidUserID = errors.New("invalid user ID")
	// ErrOIDCIdentityNotFound is returned when an OIDC identity cannot be found.
	ErrOIDCIdentityNotFound = errors.New("OIDC identity not found")
	// ErrOIDCIdentityAlreadyExists is returned when attempting to create a user
	// with an OIDC identity that already exists.
	ErrOIDCIdentityAlreadyExists = errors.New("OIDC identity already exists")
)

// Common errors for API key store operations.
var (
	// ErrAPIKeyNotFound is returned when an API key cannot be found.
	ErrAPIKeyNotFound = errors.New("API key not found")
	// ErrAPIKeyAlreadyExists is returned when attempting to create an API key
	// with a name that already exists.
	ErrAPIKeyAlreadyExists = errors.New("API key already exists")
	// ErrInvalidAPIKeyName is returned when the API key name is invalid.
	ErrInvalidAPIKeyName = errors.New("invalid API key name")
	// ErrInvalidAPIKeyID is returned when the API key ID is invalid.
	ErrInvalidAPIKeyID = errors.New("invalid API key ID")
	// ErrInvalidAPIKeyHash is returned when the API key hash is empty.
	ErrInvalidAPIKeyHash = errors.New("invalid API key hash")
	// ErrInvalidAPIKeySurface is returned when the API key surface allowlist is invalid.
	ErrInvalidAPIKeySurface = errors.New("invalid API key surface")
	// ErrInvalidAPIKeyAttribution is returned when API key attribution metadata is invalid.
	ErrInvalidAPIKeyAttribution = errors.New("invalid API key attribution")
	// ErrInvalidRole is returned when the role is not a valid role.
	ErrInvalidRole = errors.New("invalid role")
)

// Common errors for webhook store operations.
var (
	// ErrWebhookNotFound is returned when a webhook cannot be found.
	ErrWebhookNotFound = errors.New("webhook not found")
	// ErrWebhookAlreadyExists is returned when attempting to create a webhook
	// for a DAG that already has one.
	ErrWebhookAlreadyExists = errors.New("webhook already exists for this DAG")
	// ErrInvalidWebhookDAGName is returned when the DAG name is invalid.
	ErrInvalidWebhookDAGName = errors.New("invalid webhook DAG name")
	// ErrInvalidWebhookID is returned when the webhook ID is invalid.
	ErrInvalidWebhookID = errors.New("invalid webhook ID")
	// ErrInvalidWebhookTokenHash is returned when the webhook token hash is empty.
	ErrInvalidWebhookTokenHash = errors.New("invalid webhook token hash")
	// ErrWebhookHMACEncryptorRequired is returned when encrypted HMAC secret
	// storage is required but no encryptor is available.
	ErrWebhookHMACEncryptorRequired = errors.New("webhook HMAC secret requires encryptor")
	// ErrWebhookHMACDecryptFailed is returned when an encrypted HMAC secret
	// cannot be decrypted from storage.
	ErrWebhookHMACDecryptFailed = errors.New("failed to decrypt webhook HMAC secret")
)

// UserStore defines the interface for user persistence operations.
// Implementations must be safe for concurrent use.
type UserStore interface {
	// Create stores a new user.
	// Returns ErrUserAlreadyExists if a user with the same username exists.
	Create(ctx context.Context, user *User) error

	// GetByID retrieves a user by their unique ID.
	// Returns ErrUserNotFound if the user does not exist.
	GetByID(ctx context.Context, id string) (*User, error)

	// GetByUsername retrieves a user by their username.
	// Returns ErrUserNotFound if the user does not exist.
	GetByUsername(ctx context.Context, username string) (*User, error)

	// GetByOIDCIdentity retrieves a user by their OIDC identity (issuer + subject).
	// Returns ErrOIDCIdentityNotFound if no user exists with the given OIDC identity.
	GetByOIDCIdentity(ctx context.Context, issuer, subject string) (*User, error)

	// List returns all users in the store.
	List(ctx context.Context) ([]*User, error)

	// Update modifies an existing user.
	// Returns ErrUserNotFound if the user does not exist.
	Update(ctx context.Context, user *User) error

	// Delete removes a user by their ID.
	// Returns ErrUserNotFound if the user does not exist.
	Delete(ctx context.Context, id string) error

	// Count returns the total number of users.
	Count(ctx context.Context) (int64, error)
}

// APIKeyStore defines the interface for API key persistence operations.
// Implementations must be safe for concurrent use.
type APIKeyStore interface {
	// Create stores a new API key.
	// Returns ErrAPIKeyAlreadyExists if an API key with the same name exists.
	Create(ctx context.Context, key *APIKey) error

	// GetByID retrieves an API key by its unique ID.
	// Returns ErrAPIKeyNotFound if the API key does not exist.
	GetByID(ctx context.Context, id string) (*APIKey, error)

	// List returns all API keys in the store.
	List(ctx context.Context) ([]*APIKey, error)

	// Update modifies an existing API key.
	// Returns ErrAPIKeyNotFound if the API key does not exist.
	Update(ctx context.Context, key *APIKey) error

	// Delete removes an API key by its ID.
	// Returns ErrAPIKeyNotFound if the API key does not exist.
	Delete(ctx context.Context, id string) error

	// UpdateLastUsed updates the LastUsedAt timestamp for an API key.
	// This is called when the API key is used for authentication.
	UpdateLastUsed(ctx context.Context, id string) error
}

// WebhookStore defines the interface for webhook persistence operations.
// Implementations must be safe for concurrent use.
// Each DAG can have at most one webhook (1:1 relationship).
type WebhookStore interface {
	// Create stores a new webhook.
	// Returns ErrWebhookAlreadyExists if a webhook for the DAG already exists.
	Create(ctx context.Context, webhook *Webhook) error

	// GetByID retrieves a webhook by its unique ID.
	// Returns ErrWebhookNotFound if the webhook does not exist.
	GetByID(ctx context.Context, id string) (*Webhook, error)

	// GetByDAGName retrieves the webhook for a specific DAG.
	// Returns ErrWebhookNotFound if no webhook exists for the DAG.
	GetByDAGName(ctx context.Context, dagName string) (*Webhook, error)

	// List returns all webhooks in the store.
	List(ctx context.Context) ([]*Webhook, error)

	// Update modifies an existing webhook.
	// Returns ErrWebhookNotFound if the webhook does not exist.
	Update(ctx context.Context, webhook *Webhook) error

	// Delete removes a webhook by its ID.
	// Returns ErrWebhookNotFound if the webhook does not exist.
	Delete(ctx context.Context, id string) error

	// DeleteByDAGName removes a webhook by its DAG name.
	// Returns ErrWebhookNotFound if no webhook exists for the DAG.
	DeleteByDAGName(ctx context.Context, dagName string) error

	// UpdateLastUsed updates the LastUsedAt timestamp for a webhook.
	// This is called when the webhook is triggered.
	UpdateLastUsed(ctx context.Context, id string) error
}
