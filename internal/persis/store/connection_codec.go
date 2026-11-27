// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/connection"
)

// connectionCodec converts connection records to and from their stored
// form, encrypting secrets with encryptor.
type connectionCodec struct {
	encryptor *crypto.Encryptor
}

type appRecord struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Provider        connection.Provider   `json:"provider"`
	Kind            connection.AppKind    `json:"kind"`
	ClientID        string                `json:"client_id"`
	ClientSecretEnc string                `json:"client_secret_enc,omitempty"`
	ClientType      connection.ClientType `json:"client_type"`
	Agent           bool                  `json:"agent,omitempty"`
	Tenant          string                `json:"tenant,omitempty"`
	Endpoints       *endpointsRecord      `json:"endpoints,omitempty"`
	Status          connection.AppStatus  `json:"status"`
	StatusReason    string                `json:"status_reason,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type endpointsRecord struct {
	Authorize string `json:"authorize,omitempty"`
	Token     string `json:"token,omitempty"`
	Revoke    string `json:"revoke,omitempty"`
	Device    string `json:"device,omitempty"`
}

type connectionRecord struct {
	ID               string              `json:"id"`
	Workspace        string              `json:"workspace,omitempty"`
	Name             string              `json:"name"`
	Provider         connection.Provider `json:"provider"`
	App              string              `json:"app"`
	Grant            connection.Grant    `json:"grant"`
	Services         []serviceRecord     `json:"services,omitempty"`
	Scopes           []string            `json:"scopes,omitempty"`
	Subject          string              `json:"subject,omitempty"`
	BoundHosts       []string            `json:"bound_hosts,omitempty"`
	Resource         string              `json:"resource,omitempty"`
	AllowTokenExport bool                `json:"allow_token_export,omitempty"`
	Holder           connection.Holder   `json:"holder"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

type serviceRecord struct {
	Name   string            `json:"name"`
	Access connection.Access `json:"access"`
}

type tokenStateRecord struct {
	ConnectionID string `json:"connection_id"`
	tokensRecord
	Status       connection.TokenStatus `json:"status"`
	StatusReason string                 `json:"status_reason,omitempty"`
	Generation   int64                  `json:"generation"`
	Session      *sessionRecord         `json:"session,omitempty"`
	// ConfirmedSession is a session ID, which is no secret once the
	// session is confirmed.
	ConfirmedSession string    `json:"confirmed_session,omitempty"`
	RefreshedAt      time.Time `json:"refreshed_at,omitzero"`
	// RefreshErrors hold provider errors, which carry no secret.
	RefreshErrors map[string]refreshErrorRecord `json:"refresh_errors,omitempty"`
}

type refreshErrorRecord struct {
	Reason   string `json:"reason,omitempty"`
	Failures int64  `json:"failures"`
}

type tokensRecord struct {
	Account               string                       `json:"account,omitempty"`
	AccountID             string                       `json:"account_id,omitempty"`
	RefreshTokenEnc       string                       `json:"refresh_token_enc,omitempty"`
	RefreshTokenExpiresAt time.Time                    `json:"refresh_token_expires_at,omitzero"`
	AccessTokens          map[string]accessTokenRecord `json:"access_tokens,omitempty"`
	GrantedScopes         []string                     `json:"granted_scopes,omitempty"`
}

type accessTokenRecord struct {
	TokenEnc  string    `json:"token_enc"`
	ExpiresAt time.Time `json:"expires_at"`
	IssuedAt  time.Time `json:"issued_at,omitzero"`
}

type sessionRecord struct {
	ID        string                  `json:"id"`
	User      string                  `json:"user,omitempty"`
	Phase     connection.SessionPhase `json:"phase"`
	ExpiresAt time.Time               `json:"expires_at"`
	ClaimedAt time.Time               `json:"claimed_at,omitzero"`
	Reason    string                  `json:"reason,omitempty"`
	Tokens    tokensRecord            `json:"tokens,omitzero"`
	// Probes hold provider errors, which carry no secret.
	Probes []probeRecord `json:"probes,omitempty"`
}

type probeRecord struct {
	Service string `json:"service"`
	Error   string `json:"error,omitempty"`
}

type catalogRecord struct {
	ConnectionID string       `json:"connection_id"`
	Service      string       `json:"service"`
	ListedAt     time.Time    `json:"listed_at"`
	Tools        []toolRecord `json:"tools"`
}

type toolRecord struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
	Hash        string          `json:"hash"`
}

func (c connectionCodec) appToRecord(app *connection.App) (*appRecord, error) {
	secret, err := c.encrypt(app.ClientSecret)
	if err != nil {
		return nil, err
	}
	rec := &appRecord{
		ID:              app.ID,
		Name:            app.Name,
		Provider:        app.Provider,
		Kind:            app.Kind,
		ClientID:        app.ClientID,
		ClientSecretEnc: secret,
		ClientType:      app.ClientType,
		Agent:           app.Agent,
		Tenant:          app.Tenant,
		Status:          app.Status,
		StatusReason:    app.StatusReason,
		CreatedAt:       app.CreatedAt,
		UpdatedAt:       app.UpdatedAt,
	}
	if e := app.Endpoints; e != nil {
		rec.Endpoints = &endpointsRecord{Authorize: e.Authorize, Token: e.Token, Revoke: e.Revoke, Device: e.Device}
	}
	return rec, nil
}

func (c connectionCodec) appFromRecord(rec *appRecord) (*connection.App, error) {
	secret, err := c.decrypt(rec.ClientSecretEnc)
	if err != nil {
		return nil, err
	}
	app := &connection.App{
		ID:           rec.ID,
		Name:         rec.Name,
		Provider:     rec.Provider,
		Kind:         rec.Kind,
		ClientID:     rec.ClientID,
		ClientSecret: secret,
		ClientType:   rec.ClientType,
		Agent:        rec.Agent,
		Tenant:       rec.Tenant,
		Status:       rec.Status,
		StatusReason: rec.StatusReason,
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
	}
	if e := rec.Endpoints; e != nil {
		app.Endpoints = &connection.Endpoints{Authorize: e.Authorize, Token: e.Token, Revoke: e.Revoke, Device: e.Device}
	}
	return app, nil
}

func connectionToRecord(conn *connection.Connection) *connectionRecord {
	rec := &connectionRecord{
		ID:               conn.ID,
		Workspace:        conn.Workspace,
		Name:             conn.Name,
		Provider:         conn.Provider,
		App:              conn.App,
		Grant:            conn.Grant,
		Scopes:           conn.Scopes,
		Subject:          conn.Subject,
		BoundHosts:       conn.BoundHosts,
		Resource:         conn.Resource,
		AllowTokenExport: conn.AllowTokenExport,
		Holder:           conn.Holder,
		CreatedAt:        conn.CreatedAt,
		UpdatedAt:        conn.UpdatedAt,
	}
	for _, svc := range conn.Services {
		rec.Services = append(rec.Services, serviceRecord(svc))
	}
	return rec
}

func connectionFromRecord(rec *connectionRecord) *connection.Connection {
	conn := &connection.Connection{
		ID:               rec.ID,
		Workspace:        rec.Workspace,
		Name:             rec.Name,
		Provider:         rec.Provider,
		App:              rec.App,
		Grant:            rec.Grant,
		Scopes:           rec.Scopes,
		Subject:          rec.Subject,
		BoundHosts:       rec.BoundHosts,
		Resource:         rec.Resource,
		AllowTokenExport: rec.AllowTokenExport,
		Holder:           rec.Holder,
		CreatedAt:        rec.CreatedAt,
		UpdatedAt:        rec.UpdatedAt,
	}
	for _, svc := range rec.Services {
		conn.Services = append(conn.Services, connection.Service(svc))
	}
	return conn
}

func (c connectionCodec) tokenStateToRecord(state *connection.TokenState) (*tokenStateRecord, error) {
	tokens, err := c.tokensToRecord(state.Tokens)
	if err != nil {
		return nil, err
	}
	rec := &tokenStateRecord{
		ConnectionID:     state.ConnectionID,
		tokensRecord:     tokens,
		Status:           state.Status,
		StatusReason:     state.StatusReason,
		Generation:       state.Generation,
		RefreshedAt:      state.RefreshedAt,
		ConfirmedSession: state.ConfirmedSession,
	}
	for resource, e := range state.RefreshErrors {
		if rec.RefreshErrors == nil {
			rec.RefreshErrors = make(map[string]refreshErrorRecord, len(state.RefreshErrors))
		}
		rec.RefreshErrors[resource] = refreshErrorRecord(e)
	}
	if s := state.Session; s != nil {
		sessionTokens, err := c.tokensToRecord(s.Tokens)
		if err != nil {
			return nil, err
		}
		rec.Session = &sessionRecord{
			ID:        s.ID,
			User:      s.User,
			Phase:     s.Phase,
			ExpiresAt: s.ExpiresAt,
			ClaimedAt: s.ClaimedAt,
			Reason:    s.Reason,
			Tokens:    sessionTokens,
		}
		for _, p := range s.Probes {
			rec.Session.Probes = append(rec.Session.Probes, probeRecord(p))
		}
	}
	return rec, nil
}

func (c connectionCodec) tokenStateFromRecord(rec *tokenStateRecord) (*connection.TokenState, error) {
	tokens, err := c.tokensFromRecord(rec.tokensRecord)
	if err != nil {
		return nil, err
	}
	state := &connection.TokenState{
		ConnectionID:     rec.ConnectionID,
		Tokens:           tokens,
		Status:           rec.Status,
		StatusReason:     rec.StatusReason,
		Generation:       rec.Generation,
		RefreshedAt:      rec.RefreshedAt,
		ConfirmedSession: rec.ConfirmedSession,
	}
	for resource, e := range rec.RefreshErrors {
		if state.RefreshErrors == nil {
			state.RefreshErrors = make(map[string]connection.RefreshError, len(rec.RefreshErrors))
		}
		state.RefreshErrors[resource] = connection.RefreshError(e)
	}
	if s := rec.Session; s != nil {
		sessionTokens, err := c.tokensFromRecord(s.Tokens)
		if err != nil {
			return nil, err
		}
		state.Session = &connection.Session{
			ID:        s.ID,
			User:      s.User,
			Phase:     s.Phase,
			ExpiresAt: s.ExpiresAt,
			ClaimedAt: s.ClaimedAt,
			Reason:    s.Reason,
			Tokens:    sessionTokens,
		}
		for _, p := range s.Probes {
			state.Session.Probes = append(state.Session.Probes, connection.Probe(p))
		}
	}
	return state, nil
}

func (c connectionCodec) tokensToRecord(tokens connection.Tokens) (tokensRecord, error) {
	refresh, err := c.encrypt(tokens.RefreshToken)
	if err != nil {
		return tokensRecord{}, err
	}
	rec := tokensRecord{
		Account:               tokens.Account,
		AccountID:             tokens.AccountID,
		RefreshTokenEnc:       refresh,
		RefreshTokenExpiresAt: tokens.RefreshTokenExpiresAt,
		GrantedScopes:         tokens.GrantedScopes,
	}
	for resource, token := range tokens.AccessTokens {
		enc, err := c.encrypt(token.Token)
		if err != nil {
			return tokensRecord{}, err
		}
		if rec.AccessTokens == nil {
			rec.AccessTokens = make(map[string]accessTokenRecord, len(tokens.AccessTokens))
		}
		rec.AccessTokens[resource] = accessTokenRecord{TokenEnc: enc, ExpiresAt: token.ExpiresAt, IssuedAt: token.IssuedAt}
	}
	return rec, nil
}

func (c connectionCodec) tokensFromRecord(rec tokensRecord) (connection.Tokens, error) {
	refresh, err := c.decrypt(rec.RefreshTokenEnc)
	if err != nil {
		return connection.Tokens{}, err
	}
	tokens := connection.Tokens{
		Account:               rec.Account,
		AccountID:             rec.AccountID,
		RefreshToken:          refresh,
		RefreshTokenExpiresAt: rec.RefreshTokenExpiresAt,
		GrantedScopes:         rec.GrantedScopes,
	}
	for resource, token := range rec.AccessTokens {
		plain, err := c.decrypt(token.TokenEnc)
		if err != nil {
			return connection.Tokens{}, err
		}
		if tokens.AccessTokens == nil {
			tokens.AccessTokens = make(map[string]connection.AccessToken, len(rec.AccessTokens))
		}
		tokens.AccessTokens[resource] = connection.AccessToken{Token: plain, ExpiresAt: token.ExpiresAt, IssuedAt: token.IssuedAt}
	}
	return tokens, nil
}

func catalogToRecord(connectionID string, catalog *connection.Catalog) *catalogRecord {
	rec := &catalogRecord{
		ConnectionID: connectionID,
		Service:      catalog.Service,
		ListedAt:     catalog.ListedAt,
		Tools:        make([]toolRecord, 0, len(catalog.Tools)),
	}
	for _, tool := range catalog.Tools {
		// Schemas are kept canonical, so what Catalog reads back equals
		// what SaveCatalog returns, and the hash covers the stored bytes.
		stored := toolRecord{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: canonicalJSON(tool.InputSchema),
			Annotations: canonicalJSON(tool.Annotations),
		}
		stored.Hash = toolHash(stored)
		rec.Tools = append(rec.Tools, stored)
	}
	return rec
}

func catalogFromRecord(rec *catalogRecord) *connection.Catalog {
	catalog := &connection.Catalog{Service: rec.Service, ListedAt: rec.ListedAt, Tools: make([]connection.Tool, 0, len(rec.Tools))}
	for _, tool := range rec.Tools {
		catalog.Tools = append(catalog.Tools, connection.Tool(tool))
	}
	return catalog
}

// toolHash covers the parts of a tool that change what a caller of it
// agrees to. Its JSON is canonical, so a listing that differs only in
// whitespace or key order hashes the same, and each part is
// length-prefixed, so no two tools share an encoding.
func toolHash(tool toolRecord) string {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(tool.Description), tool.InputSchema, tool.Annotations} {
		_, _ = fmt.Fprintf(h, "%d:", len(part))
		_, _ = h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON re-encodes raw with sorted object keys, keeping numbers as
// written. Input that is not JSON is returned as is.
func canonicalJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return raw
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return canonical
}

// encrypt leaves an empty value empty, so an absent secret stays absent.
func (c connectionCodec) encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	enc, err := c.encryptor.Encrypt(plaintext)
	if err != nil {
		return "", fmt.Errorf("connection store: encrypt: %w", err)
	}
	return enc, nil
}

func (c connectionCodec) decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	plain, err := c.encryptor.Decrypt(encoded)
	if err != nil {
		return "", fmt.Errorf("connection store: decrypt: %w", err)
	}
	return plain, nil
}
