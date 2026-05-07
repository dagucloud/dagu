// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type WebhookAuthMode string

const (
	WebhookAuthModeTokenOnly    WebhookAuthMode = "token_only"
	WebhookAuthModeTokenAndHMAC WebhookAuthMode = "token_and_hmac"
	WebhookAuthModeHMACOnly     WebhookAuthMode = "hmac_only"
)

type WebhookHMACEnforcementMode string

const (
	WebhookHMACEnforcementModeStrict  WebhookHMACEnforcementMode = "strict"
	WebhookHMACEnforcementModeObserve WebhookHMACEnforcementMode = "observe"
)

const (
	WebhookHMACAlgorithm         = "HMAC-SHA256"
	WebhookHMACHeaderName        = "X-Dagu-Signature"
	WebhookHMACHeaderValueFormat = "sha256=<hex>"
)

func (m WebhookAuthMode) OrDefault() WebhookAuthMode {
	if m == "" {
		return WebhookAuthModeTokenOnly
	}
	return m
}

// Webhook represents a webhook configuration for triggering a specific DAG.
// Each DAG can have at most one webhook. The token is stored as a bcrypt hash.
type Webhook struct {
	// ID is the unique identifier for the webhook (UUID).
	ID string `json:"id"`
	// DAGName is the file name of the DAG this webhook triggers.
	// This serves as a unique constraint - one webhook per DAG.
	DAGName string `json:"dagName"`
	// TokenHash is the bcrypt hash of the webhook token secret.
	// Excluded from JSON serialization for security.
	TokenHash string `json:"-"`
	// TokenPrefix stores the first 8 characters of the token for identification.
	TokenPrefix string `json:"tokenPrefix"`
	// Enabled indicates whether the webhook is active.
	Enabled bool `json:"enabled"`
	// CreatedAt is the timestamp when the webhook was created.
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt is the timestamp when the webhook was last modified.
	UpdatedAt time.Time `json:"updatedAt"`
	// CreatedBy is the user ID of the admin who created the webhook.
	CreatedBy string `json:"createdBy"`
	// LastUsedAt is the timestamp when the webhook was last triggered.
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	// AuthMode controls which request authentication mode the webhook uses.
	AuthMode WebhookAuthMode `json:"-"`
	// HMACEnforcementMode controls whether HMAC failures block requests when HMAC is enabled.
	HMACEnforcementMode WebhookHMACEnforcementMode `json:"-"`
	// HMACSecret is stored decrypted in memory and encrypted at rest.
	HMACSecret string `json:"-"`
	// HMACSecretGeneratedAt records when the HMAC secret was last generated.
	HMACSecretGeneratedAt *time.Time `json:"-"`
}

// NewWebhook creates a Webhook with a new UUID and sets CreatedAt and UpdatedAt to the current UTC time.
// It validates that required fields are not empty.
// Returns an error if validation fails.
func NewWebhook(dagName, tokenHash, tokenPrefix, createdBy string) (*Webhook, error) {
	if dagName == "" {
		return nil, ErrInvalidWebhookDAGName
	}
	if tokenHash == "" {
		return nil, ErrInvalidWebhookTokenHash
	}
	now := time.Now().UTC()
	return &Webhook{
		ID:          uuid.Must(uuid.NewV7()).String(),
		DAGName:     dagName,
		TokenHash:   tokenHash,
		TokenPrefix: tokenPrefix,
		Enabled:     true, // Enabled by default on creation
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   createdBy,
		AuthMode:    WebhookAuthModeTokenOnly,
	}, nil
}

// WebhookForStorage is used for JSON serialization to persistent storage.
// It includes the token hash which is excluded from the regular Webhook JSON.
type WebhookForStorage struct {
	ID                    string                     `json:"id"`
	DAGName               string                     `json:"dagName"`
	TokenHash             string                     `json:"tokenHash"`
	TokenPrefix           string                     `json:"tokenPrefix"`
	Enabled               bool                       `json:"enabled"`
	CreatedAt             time.Time                  `json:"createdAt"`
	UpdatedAt             time.Time                  `json:"updatedAt"`
	CreatedBy             string                     `json:"createdBy"`
	LastUsedAt            *time.Time                 `json:"lastUsedAt,omitempty"`
	AuthMode              WebhookAuthMode            `json:"authMode,omitempty"`
	HMACEnforcementMode   WebhookHMACEnforcementMode `json:"hmacEnforcementMode,omitempty"`
	HMACSecretEnc         string                     `json:"hmacSecretEnc,omitempty"`
	HMACSecretGeneratedAt *time.Time                 `json:"hmacSecretGeneratedAt,omitempty"`
}

// ToStorage converts a Webhook to WebhookForStorage for persistence.
// NOTE: When adding new fields to Webhook or WebhookForStorage, ensure both
// ToStorage and ToWebhook are updated to maintain field synchronization.
func (w *Webhook) ToStorage() *WebhookForStorage {
	return &WebhookForStorage{
		ID:                    w.ID,
		DAGName:               w.DAGName,
		TokenHash:             w.TokenHash,
		TokenPrefix:           w.TokenPrefix,
		Enabled:               w.Enabled,
		CreatedAt:             w.CreatedAt,
		UpdatedAt:             w.UpdatedAt,
		CreatedBy:             w.CreatedBy,
		LastUsedAt:            w.LastUsedAt,
		AuthMode:              w.AuthMode,
		HMACEnforcementMode:   w.HMACEnforcementMode,
		HMACSecretGeneratedAt: w.HMACSecretGeneratedAt,
	}
}

// ToWebhook converts WebhookForStorage back to Webhook.
// NOTE: When adding new fields to Webhook or WebhookForStorage, ensure both
// ToStorage and ToWebhook are updated to maintain field synchronization.
func (s *WebhookForStorage) ToWebhook() *Webhook {
	return &Webhook{
		ID:                    s.ID,
		DAGName:               s.DAGName,
		TokenHash:             s.TokenHash,
		TokenPrefix:           s.TokenPrefix,
		Enabled:               s.Enabled,
		CreatedAt:             s.CreatedAt,
		UpdatedAt:             s.UpdatedAt,
		CreatedBy:             s.CreatedBy,
		LastUsedAt:            s.LastUsedAt,
		AuthMode:              s.AuthMode,
		HMACEnforcementMode:   s.HMACEnforcementMode,
		HMACSecretGeneratedAt: s.HMACSecretGeneratedAt,
	}
}

func (w *Webhook) EffectiveAuthMode() WebhookAuthMode {
	if w == nil {
		return WebhookAuthModeTokenOnly
	}
	return w.AuthMode.OrDefault()
}

func (w *Webhook) HMACEnabled() bool {
	return w.EffectiveAuthMode() != WebhookAuthModeTokenOnly
}

func (w *Webhook) HMACSecretConfigured() bool {
	return w != nil && w.HMACSecret != ""
}

type webhookHMACDetails struct {
	Enabled          bool                       `json:"enabled"`
	EnforcementMode  WebhookHMACEnforcementMode `json:"enforcementMode,omitempty"`
	Algorithm        string                     `json:"algorithm,omitempty"`
	HeaderName       string                     `json:"headerName,omitempty"`
	Format           string                     `json:"format,omitempty"`
	SecretConfigured bool                       `json:"secretConfigured"`
	UpdatedAt        *time.Time                 `json:"updatedAt,omitempty"`
}

// MarshalJSON exposes defaulted auth mode and public HMAC status while keeping
// secret material and token hash out of the JSON payload.
func (w *Webhook) MarshalJSON() ([]byte, error) {
	type alias Webhook
	enforcementMode := w.HMACEnforcementMode
	if enforcementMode == "" && w.HMACEnabled() {
		enforcementMode = WebhookHMACEnforcementModeStrict
	}

	return json.Marshal(struct {
		*alias
		AuthMode WebhookAuthMode    `json:"authMode"`
		HMAC     webhookHMACDetails `json:"hmac"`
	}{
		alias:    (*alias)(w),
		AuthMode: w.EffectiveAuthMode(),
		HMAC: webhookHMACDetails{
			Enabled:          w.HMACEnabled(),
			EnforcementMode:  enforcementMode,
			Algorithm:        conditionalString(w.HMACEnabled(), WebhookHMACAlgorithm),
			HeaderName:       conditionalString(w.HMACEnabled(), WebhookHMACHeaderName),
			Format:           conditionalString(w.HMACEnabled(), WebhookHMACHeaderValueFormat),
			SecretConfigured: w.HMACSecretConfigured(),
			UpdatedAt:        w.HMACSecretGeneratedAt,
		},
	})
}

func conditionalString(cond bool, value string) string {
	if !cond {
		return ""
	}
	return value
}
