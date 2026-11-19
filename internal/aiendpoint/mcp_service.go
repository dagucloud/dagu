// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/secret"
)

// MCPService joins the metadata store of the MCP server entries and the secret
// store. It makes each change of an entry and of its credentials.
type MCPService struct {
	store   MCPStore
	secrets secret.Store

	// mu orders the changes of this process. The stores make each record change
	// atomic.
	mu sync.Mutex
}

// NewMCPService creates a MCPService.
func NewMCPService(store MCPStore, secrets secret.Store) (*MCPService, error) {
	if store == nil {
		return nil, errors.New("MCP server service: store cannot be nil")
	}
	if secrets == nil {
		return nil, errors.New("MCP server service: secret store cannot be nil")
	}
	return &MCPService{store: store, secrets: secrets}, nil
}

// MCPCredentialsInput is a set of credential values: a token, or headers, or none.
type MCPCredentialsInput struct {
	Token   string
	Headers map[string]string
}

// MCPCreateInput holds the values of a new entry.
type MCPCreateInput struct {
	Name        string
	URL         string
	Scope       Scope
	Allow       Allow
	Credentials MCPCredentialsInput
	Actor       string
}

// MCPUpdateInput holds the changes of an entry. A nil field keeps its value. The
// name, the scope and the credentials have no field here.
type MCPUpdateInput struct {
	URL   *string
	Allow *Allow
	Actor string
}

// Create validates and saves a new entry with its credentials. A create that
// fails leaves no entry.
func (s *MCPService) Create(ctx context.Context, in MCPCreateInput) (*MCPServer, error) {
	now := time.Now().UTC()
	server := &MCPServer{
		Name:      in.Name,
		URL:       in.URL,
		Scope:     in.Scope,
		Allow:     Allow{Workspaces: slices.Clone(in.Allow.Workspaces), DAGs: slices.Clone(in.Allow.DAGs)},
		CreatedBy: in.Actor,
		CreatedAt: now,
		UpdatedBy: in.Actor,
		UpdatedAt: now,
	}
	if err := validateMCPServer(server); err != nil {
		return nil, err
	}
	headers, err := checkCredentials(in.Credentials)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// The record claims the name before the credentials are written, as in
	// Service.Create.
	if err := s.store.Create(ctx, server); err != nil {
		return nil, err
	}
	saved, err := s.completeCreate(ctx, server, in.Credentials.Token, headers, in.Actor, now)
	if err != nil {
		_ = s.store.Delete(context.WithoutCancel(ctx), server.Scope, server.Name)
		return nil, err
	}
	return saved, nil
}

// completeCreate saves the credentials of a claimed entry. On an error after
// the writes, it deletes the secrets it wrote.
func (s *MCPService) completeCreate(ctx context.Context, server *MCPServer, token string, headers map[string]string, actor string, now time.Time) (*MCPServer, error) {
	if token == "" && len(headers) == 0 {
		return server.Clone(), nil
	}
	written, err := s.writeCredentials(ctx, server, token, headers, actor, now)
	if err != nil {
		s.deleteSecrets(ctx, secretIDs(written))
		return nil, err
	}
	saved, err := s.store.Update(ctx, server.Scope, server.Name, func(cur *MCPServer) error {
		cur.Token, cur.Headers = server.Token, server.Headers
		return nil
	})
	if err != nil {
		s.deleteSecrets(ctx, secretIDs(written))
		return nil, err
	}
	return saved, nil
}

// Get returns the entry of a scope, or ErrNotFound.
func (s *MCPService) Get(ctx context.Context, scope Scope, name string) (*MCPServer, error) {
	return s.store.Get(ctx, scope, name)
}

// List returns the entries of all scopes. It gives no credential value.
func (s *MCPService) List(ctx context.Context) ([]*MCPServer, error) {
	return s.store.List(ctx)
}

// Update changes the URL and the use rule of an entry.
func (s *MCPService) Update(ctx context.Context, scope Scope, name string, in MCPUpdateInput) (*MCPServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.store.Update(ctx, scope, name, func(server *MCPServer) error {
		if in.URL != nil {
			server.URL = *in.URL
		}
		if in.Allow != nil {
			server.Allow = Allow{Workspaces: slices.Clone(in.Allow.Workspaces), DAGs: slices.Clone(in.Allow.DAGs)}
		}
		if err := validateMCPServer(server); err != nil {
			return err
		}
		server.UpdatedBy = in.Actor
		server.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// SetCredentials replaces the whole set of credentials of an entry. The old values
// stay in the secret store as encrypted old versions. A credential that the new
// set does not have is deleted with its secret. A replacement that fails puts the
// old values back, so the entry keeps its whole old set rather than a mix of
// both sets.
func (s *MCPService) SetCredentials(ctx context.Context, scope Scope, name string, in MCPCredentialsInput, actor string) (*MCPServer, error) {
	headers, err := checkCredentials(in)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	server, err := s.store.Get(ctx, scope, name)
	if err != nil {
		return nil, err
	}
	before := credentialIDs(server)
	previous, err := s.currentValues(ctx, before, in.Token, headers)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	written, err := s.writeCredentials(ctx, server, in.Token, headers, actor, now)
	if err != nil {
		s.undoWrites(ctx, before, previous, written, actor, now)
		return nil, err
	}
	var old map[string]string
	saved, err := s.store.Update(ctx, scope, name, func(cur *MCPServer) error {
		old = credentialIDs(cur)
		cur.Token, cur.Headers = server.Token, server.Headers
		cur.UpdatedBy, cur.UpdatedAt = actor, now
		return nil
	})
	if err != nil {
		s.undoWrites(ctx, before, previous, written, actor, now)
		return nil, err
	}
	// A secret that the new set no longer uses has no record.
	kept := credentialIDs(saved)
	var obsolete []string
	for header, id := range old {
		if kept[header] != id {
			obsolete = append(obsolete, id)
		}
	}
	s.deleteSecrets(ctx, obsolete)
	return saved, nil
}

// Delete removes an entry and its credentials.
func (s *MCPService) Delete(ctx context.Context, scope Scope, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	server, err := s.store.Get(ctx, scope, name)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, scope, name); err != nil {
		return err
	}
	var failed []string
	for _, id := range credentialIDs(server) {
		if err := s.secrets.Delete(ctx, id); err != nil && !errors.Is(err, secret.ErrNotFound) {
			failed = append(failed, id)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("MCP server %q was deleted but %d of its credentials were not", name, len(failed))
	}
	return nil
}

// ResolveMCPServer finds the entry that a step names, for a DAG, and checks the use
// rule. A DAG with no workspace passes an empty workspace. It returns an *Error
// with the reason mcp_server_unknown or mcp_server_forbidden, or with the reason
// endpoint_unavailable when the store fails. The result has no credential value.
// The coordinator reads the values with ReadCredentials.
func (s *MCPService) ResolveMCPServer(ctx context.Context, name, dagName, workspace string) (MCPServer, error) {
	unavailable := func(err error) error {
		return &Error{Reason: ReasonUnavailable, Name: name, Kind: "MCP server", Detail: "the MCP server store failed", Err: err}
	}
	if err := ValidateMCPName(name); err != nil {
		return MCPServer{}, &Error{Reason: ReasonMCPUnknown, Name: name, Kind: "MCP server", Detail: "the name is not valid"}
	}
	found, err := visibleIn(workspace, func(scope Scope) (*MCPServer, error) {
		server, err := s.store.Get(ctx, scope, name)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, unavailable(err)
		}
		return server, nil
	})
	if err != nil {
		return MCPServer{}, err
	}
	if found == nil {
		return MCPServer{}, &Error{Reason: ReasonMCPUnknown, Name: name, Kind: "MCP server",
			Detail: "no MCP server entry of this name is visible to the DAG"}
	}
	if err := found.CheckUse(dagName, workspace); err != nil {
		return MCPServer{}, err
	}
	return *found, nil
}

// ReadCredentials returns the credential values of an entry in clear text. It is
// for the relay of the coordinator only. No route of the API calls it, and no log
// or error holds a value. A secret that the process cannot read gives the reason
// endpoint_unavailable.
func (s *MCPService) ReadCredentials(ctx context.Context, server MCPServer) (MCPCredentials, error) {
	read := func(c Credential) (string, error) {
		value, _, err := s.secrets.ReadValue(ctx, c.SecretID)
		if err != nil {
			// The cause can name a path, never a value.
			return "", &Error{Reason: ReasonUnavailable, Name: server.Name, Kind: "MCP server",
				Detail: "the process cannot read a credential", Err: err}
		}
		return value, nil
	}
	var out MCPCredentials
	if server.Token != nil {
		token, err := read(*server.Token)
		if err != nil {
			return MCPCredentials{}, err
		}
		out.Token = token
	}
	if len(server.Headers) > 0 {
		out.Headers = make(map[string]string, len(server.Headers))
	}
	for _, header := range server.Headers {
		value, err := read(header)
		if err != nil {
			return MCPCredentials{}, err
		}
		out.Headers[header.Header] = value
	}
	return out, nil
}

// Choices returns the entries that a DAG may use, sorted by name. An entry of the
// workspace hides the global entry of the same name for the DAGs of its
// workspace, even when the DAG may not use the entry of the workspace.
func (s *MCPService) Choices(ctx context.Context, dagName, workspace string) ([]MCPServer, error) {
	all, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	hidden := map[string]bool{}
	for _, server := range all {
		if !server.Scope.IsGlobal() && server.Scope.Workspace == workspace {
			hidden[server.Name] = true
		}
	}
	var out []MCPServer
	for _, server := range all {
		if server.Scope.IsGlobal() && hidden[server.Name] {
			continue
		}
		if !server.Scope.IsGlobal() && server.Scope.Workspace != workspace {
			continue
		}
		if server.CheckUse(dagName, workspace) == nil {
			out = append(out, *server)
		}
	}
	slices.SortFunc(out, func(a, b MCPServer) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// checkCredentials validates a set of credentials and returns the headers with
// their names in lower case.
func checkCredentials(in MCPCredentialsInput) (map[string]string, error) {
	if in.Token != "" {
		if err := validateCredentialValue("token", in.Token); err != nil {
			return nil, err
		}
	}
	return normalizeMCPHeaders(in.Token, in.Headers)
}

// credentialIDs maps the header of each credential of an entry to the ID of its
// secret. The token has the header authorization.
func credentialIDs(server *MCPServer) map[string]string {
	ids := map[string]string{}
	if server.Token != nil {
		ids[MCPTokenHeader] = server.Token.SecretID
	}
	for _, header := range server.Headers {
		ids[header.Header] = header.SecretID
	}
	return ids
}

// currentValues reads the values that a new set overwrites: the value of each
// credential of the set that the entry already has, by header. A secret that no
// longer exists has no value to keep.
func (s *MCPService) currentValues(ctx context.Context, ids map[string]string, token string, headers map[string]string) (map[string]string, error) {
	names := slices.Collect(maps.Keys(headers))
	if token != "" {
		names = append(names, MCPTokenHeader)
	}
	values := map[string]string{}
	for _, header := range names {
		id, ok := ids[header]
		if !ok {
			continue
		}
		value, _, err := s.secrets.ReadValue(ctx, id)
		switch {
		case errors.Is(err, secret.ErrNotFound):
			continue
		case err != nil:
			return nil, err
		}
		values[header] = value
	}
	return values, nil
}

// undoWrites reverts the writes of a replacement that failed. A secret that the
// entry already had gets its old value back as a new version. A secret that the
// replacement made, which no record uses, is deleted.
func (s *MCPService) undoWrites(ctx context.Context, before, previous map[string]string, written []Credential, actor string, now time.Time) {
	ctx = context.WithoutCancel(ctx)
	for _, c := range written {
		value, ok := previous[c.Header]
		if !ok || c.SecretID != before[c.Header] {
			_ = s.secrets.Delete(ctx, c.SecretID)
			continue
		}
		_, _ = s.secrets.WriteValue(ctx, c.SecretID, secret.WriteValueInput{Value: value, CreatedBy: actor, CreatedAt: now})
	}
}

// secretIDs returns the IDs of the secrets of the credentials.
func secretIDs(credentials []Credential) []string {
	ids := make([]string, 0, len(credentials))
	for _, c := range credentials {
		ids = append(ids, c.SecretID)
	}
	return ids
}

// writeCredentials writes each value of the new set as a version of a managed
// secret, and puts the credentials of the set in the entry. It reuses the secret
// of a credential that the entry already has. The record of the entry is not
// saved here. It returns the credentials it wrote, also those written before an
// error, so the caller can revert them.
func (s *MCPService) writeCredentials(ctx context.Context, server *MCPServer, token string, headers map[string]string, actor string, now time.Time) ([]Credential, error) {
	old := credentialIDs(server)
	write := func(header, value string) (Credential, error) {
		id, err := writeManagedSecret(ctx, s.secrets, managedSecret{
			workspace:   server.Scope.SecretWorkspace(),
			ref:         MCPSecretRef(server.Name, header),
			description: fmt.Sprintf("Credential %s of the MCP server %s", header, server.Name),
		}, old[header], value, actor, now)
		if err != nil {
			return Credential{}, err
		}
		prefix, fingerprint := KeyDisplay(value)
		return Credential{Header: header, SecretID: id, Prefix: prefix, Fingerprint: fingerprint, UpdatedAt: now}, nil
	}

	var (
		written  []Credential
		newToken *Credential
	)
	if token != "" {
		credential, err := write(MCPTokenHeader, token)
		if err != nil {
			return written, err
		}
		written = append(written, credential)
		newToken = &credential
	}
	names := slices.Sorted(maps.Keys(headers))
	newHeaders := make([]Credential, 0, len(names))
	for _, name := range names {
		credential, err := write(name, headers[name])
		if err != nil {
			return written, err
		}
		written = append(written, credential)
		newHeaders = append(newHeaders, credential)
	}
	server.Token = newToken
	server.Headers = newHeaders
	return written, nil
}

// deleteSecrets removes secrets that no record uses. A failure leaves a secret
// with no record. The next write of the same credential reuses it.
func (s *MCPService) deleteSecrets(ctx context.Context, ids []string) {
	ctx = context.WithoutCancel(ctx)
	for _, id := range ids {
		_ = s.secrets.Delete(ctx, id)
	}
}
