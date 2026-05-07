// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package remotenode

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// AuthType represents the authentication method for a remote node.
type AuthType string

const (
	AuthTypeNone  AuthType = "none"
	AuthTypeBasic AuthType = "basic"
	AuthTypeToken AuthType = "token"
)

// RemoteNode is the domain model for a managed remote node.
type RemoteNode struct {
	ID                string
	Name              string
	Description       string
	APIBaseURL        string
	AuthType          AuthType
	BasicAuthUsername string
	BasicAuthPassword string // plaintext in memory, encrypted at rest
	AuthToken         string // plaintext in memory, encrypted at rest
	SkipTLSVerify     bool
	Timeout           time.Duration // per-node timeout; 0 = use default
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewRemoteNode creates a RemoteNode with a new UUID and current timestamps.
func NewRemoteNode(name, description, apiBaseURL string, authType AuthType) *RemoteNode {
	now := time.Now().UTC()
	return &RemoteNode{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Name:        name,
		Description: description,
		APIBaseURL:  apiBaseURL,
		AuthType:    authType,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ApplyAuth adds authentication headers to the request based on the node's auth configuration.
func (n *RemoteNode) ApplyAuth(req *http.Request) {
	switch n.AuthType {
	case AuthTypeBasic:
		req.SetBasicAuth(n.BasicAuthUsername, n.BasicAuthPassword)
	case AuthTypeToken:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", n.AuthToken))
	case AuthTypeNone:
		// No authentication needed.
	}
}

// RemoteNodeForStorage is used for JSON serialization to persistent storage.
// Credential fields are encrypted at rest.
type RemoteNodeForStorage struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description,omitempty"`
	APIBaseURL           string    `json:"api_base_url"`
	AuthType             AuthType  `json:"auth_type"`
	BasicAuthUsername    string    `json:"basic_auth_username,omitempty"`
	BasicAuthPasswordEnc string    `json:"basic_auth_password_enc,omitempty"`
	AuthTokenEnc         string    `json:"auth_token_enc,omitempty"`
	SkipTLSVerify        bool      `json:"skip_tls_verify,omitempty"`
	TimeoutSeconds       int       `json:"timeout_seconds,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// ToStorage converts a RemoteNode to RemoteNodeForStorage.
// Note: Credential encryption must be handled by the caller before setting
// BasicAuthPasswordEnc and AuthTokenEnc.
func (n *RemoteNode) ToStorage() *RemoteNodeForStorage {
	var timeoutSec int
	if n.Timeout > 0 {
		timeoutSec = int(n.Timeout.Seconds())
	}
	return &RemoteNodeForStorage{
		ID:                n.ID,
		Name:              n.Name,
		Description:       n.Description,
		APIBaseURL:        n.APIBaseURL,
		AuthType:          n.AuthType,
		BasicAuthUsername: n.BasicAuthUsername,
		SkipTLSVerify:     n.SkipTLSVerify,
		TimeoutSeconds:    timeoutSec,
		CreatedAt:         n.CreatedAt,
		UpdatedAt:         n.UpdatedAt,
	}
}

// ToRemoteNode converts RemoteNodeForStorage back to RemoteNode.
// Note: Credential decryption must be handled by the caller to populate
// BasicAuthPassword and AuthToken.
func (s *RemoteNodeForStorage) ToRemoteNode() *RemoteNode {
	timeout := max(time.Duration(s.TimeoutSeconds)*time.Second, 0)
	return &RemoteNode{
		ID:                s.ID,
		Name:              s.Name,
		Description:       s.Description,
		APIBaseURL:        s.APIBaseURL,
		AuthType:          s.AuthType,
		BasicAuthUsername: s.BasicAuthUsername,
		SkipTLSVerify:     s.SkipTLSVerify,
		Timeout:           timeout,
		CreatedAt:         s.CreatedAt,
		UpdatedAt:         s.UpdatedAt,
	}
}
