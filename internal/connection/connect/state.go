// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/connection"
)

// statePurpose tells a state parameter apart from other text that the
// server's key encrypts, such as a stored secret.
const statePurpose = "connect-state"

// errBadState reports a state parameter that fails to decrypt or decode.
var errBadState = errors.New("the state parameter is not valid")

// authState is what the state parameter of an authorization request
// carries, so that any frontend replica completes its callback.
type authState struct {
	Purpose      string `json:"p"`
	Workspace    string `json:"w,omitempty"`
	Name         string `json:"n"`
	ConnectionID string `json:"c"`
	SessionID    string `json:"s"`
	// Verifier is the PKCE verifier.
	Verifier    string `json:"v"`
	RedirectURI string `json:"r"`
	// ExpectedAccount is the email the starter expects to sign in.
	ExpectedAccount string    `json:"e,omitempty"`
	ExpiresAt       time.Time `json:"x"`
}

func (s authState) key() connection.Key {
	return connection.Key{Workspace: s.Workspace, Name: s.Name}
}

// sealState encrypts s into a URL-safe state parameter.
func sealState(enc *crypto.Encryptor, s authState) (string, error) {
	s.Purpose = statePurpose
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	sealed, err := enc.Encrypt(string(data))
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// openState decrypts a state parameter that sealState made. The
// authenticated encryption fails for any other text.
func openState(enc *crypto.Encryptor, param string) (authState, error) {
	raw, err := base64.RawURLEncoding.DecodeString(param)
	if err != nil || len(raw) == 0 {
		return authState{}, errBadState
	}
	data, err := enc.Decrypt(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		return authState{}, errBadState
	}
	var s authState
	if err := json.Unmarshal([]byte(data), &s); err != nil || s.Purpose != statePurpose {
		return authState{}, errBadState
	}
	if s.SessionID == "" || s.ConnectionID == "" {
		return authState{}, fmt.Errorf("%w: it names no session", errBadState)
	}
	return s, nil
}
