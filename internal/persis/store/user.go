// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ auth.UserStore = (*UserStore)(nil)
var _ auth.AuthorizationSyncUserStore = (*UserStore)(nil)

// UserStore implements [auth.UserStore].
// Secondary indices are rebuilt from the collection on startup and kept in sync under mu.
type UserStore struct {
	col persis.Collection

	mu                     sync.RWMutex
	byUsername             map[string]string // username → userID
	byOIDCIdentity         map[string]string // oidcKey(issuer,subject) → userID
	byTrustedProxyIdentity map[string]string // proxy source + user → userID
	count                  int64
}

// NewUserStore creates a UserStore backed by col.
func NewUserStore(col persis.Collection) (*UserStore, error) {
	s := &UserStore{
		col:                    col,
		byUsername:             make(map[string]string),
		byOIDCIdentity:         make(map[string]string),
		byTrustedProxyIdentity: make(map[string]string),
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("user store: build index: %w", err)
	}
	return s, nil
}

func (s *UserStore) rebuildIndex(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recs {
		var stored auth.UserForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			continue
		}
		if err := validateTrustedProxyIdentity(stored.AuthProvider, stored.TrustedProxySource, stored.TrustedProxyUser, stored.OIDCIssuer, stored.OIDCSubject); err != nil {
			return fmt.Errorf("user %q: %w", stored.ID, err)
		}
		s.byUsername[stored.Username] = stored.ID
		if stored.OIDCIssuer != "" && stored.OIDCSubject != "" {
			s.byOIDCIdentity[oidcKey(stored.OIDCIssuer, stored.OIDCSubject)] = stored.ID
		}
		if stored.TrustedProxyUser != "" {
			key := trustedProxyKey(stored.TrustedProxySource, stored.TrustedProxyUser)
			if _, exists := s.byTrustedProxyIdentity[key]; exists {
				return fmt.Errorf("user %q: %w", stored.ID, auth.ErrTrustedProxyIdentityAlreadyExists)
			}
			s.byTrustedProxyIdentity[key] = stored.ID
		}
		s.count++
	}
	return nil
}

// Create stores a new user.
// Returns [auth.ErrUserAlreadyExists] if a user with the same username exists.
func (s *UserStore) Create(ctx context.Context, user *auth.User) error {
	if user == nil {
		return errors.New("user store: user cannot be nil")
	}
	if user.ID == "" {
		return auth.ErrInvalidUserID
	}
	if user.Username == "" {
		return auth.ErrInvalidUsername
	}
	if err := validateTrustedProxyIdentity(user.AuthProvider, user.TrustedProxySource, user.TrustedProxyUser, user.OIDCIssuer, user.OIDCSubject); err != nil {
		return err
	}

	data, err := persis.Encode(user.ToStorage())
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if user.TrustedProxyUser != "" {
		if _, exists := s.byTrustedProxyIdentity[trustedProxyKey(user.TrustedProxySource, user.TrustedProxyUser)]; exists {
			return auth.ErrTrustedProxyIdentityAlreadyExists
		}
	}
	if _, exists := s.byUsername[user.Username]; exists {
		return auth.ErrUserAlreadyExists
	}
	if user.OIDCIssuer != "" && user.OIDCSubject != "" {
		if _, exists := s.byOIDCIdentity[oidcKey(user.OIDCIssuer, user.OIDCSubject)]; exists {
			return auth.ErrOIDCIdentityAlreadyExists
		}
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        user.ID,
		Data:      data,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}); err != nil {
		return err
	}
	s.byUsername[user.Username] = user.ID
	if user.OIDCIssuer != "" && user.OIDCSubject != "" {
		s.byOIDCIdentity[oidcKey(user.OIDCIssuer, user.OIDCSubject)] = user.ID
	}
	if user.TrustedProxyUser != "" {
		s.byTrustedProxyIdentity[trustedProxyKey(user.TrustedProxySource, user.TrustedProxyUser)] = user.ID
	}
	s.count++
	return nil
}

// GetByID retrieves a user by their unique ID.
// Returns [auth.ErrUserNotFound] if the user does not exist.
func (s *UserStore) GetByID(ctx context.Context, id string) (*auth.User, error) {
	if id == "" {
		return nil, auth.ErrInvalidUserID
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, auth.ErrUserNotFound
		}
		return nil, err
	}
	return userFromRecord(rec)
}

// GetByUsername retrieves a user by their username.
// Returns [auth.ErrUserNotFound] if the user does not exist.
func (s *UserStore) GetByUsername(ctx context.Context, username string) (*auth.User, error) {
	if username == "" {
		return nil, auth.ErrInvalidUsername
	}
	s.mu.RLock()
	id, ok := s.byUsername[username]
	s.mu.RUnlock()
	if !ok {
		return nil, auth.ErrUserNotFound
	}
	return s.GetByID(ctx, id)
}

// GetByOIDCIdentity retrieves a user by their OIDC identity.
// Returns [auth.ErrOIDCIdentityNotFound] if no user exists with the given identity.
func (s *UserStore) GetByOIDCIdentity(ctx context.Context, issuer, subject string) (*auth.User, error) {
	if issuer == "" || subject == "" {
		return nil, auth.ErrOIDCIdentityNotFound
	}
	s.mu.RLock()
	id, ok := s.byOIDCIdentity[oidcKey(issuer, subject)]
	s.mu.RUnlock()
	if !ok {
		return nil, auth.ErrOIDCIdentityNotFound
	}
	user, err := s.GetByID(ctx, id)
	if errors.Is(err, auth.ErrUserNotFound) {
		return nil, auth.ErrOIDCIdentityNotFound
	}
	return user, err
}

// GetByTrustedProxyIdentity retrieves a user by their proxy identity source and user.
// Returns [auth.ErrTrustedProxyIdentityNotFound] if no user exists with the given identity.
func (s *UserStore) GetByTrustedProxyIdentity(ctx context.Context, source, identity string) (*auth.User, error) {
	if identity == "" {
		return nil, auth.ErrTrustedProxyIdentityNotFound
	}
	s.mu.RLock()
	id, ok := s.byTrustedProxyIdentity[trustedProxyKey(source, identity)]
	s.mu.RUnlock()
	if !ok {
		return nil, auth.ErrTrustedProxyIdentityNotFound
	}
	user, err := s.GetByID(ctx, id)
	if errors.Is(err, auth.ErrUserNotFound) {
		return nil, auth.ErrTrustedProxyIdentityNotFound
	}
	return user, err
}

// List returns all users in the store.
func (s *UserStore) List(ctx context.Context) ([]*auth.User, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*auth.User, 0, len(recs))
	for _, rec := range recs {
		u, err := userFromRecord(rec)
		if err != nil {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// Update modifies an existing user.
// Returns [auth.ErrUserNotFound] if the user does not exist.
func (s *UserStore) Update(ctx context.Context, user *auth.User) error {
	if user == nil {
		return errors.New("user store: user cannot be nil")
	}
	if user.ID == "" {
		return auth.ErrInvalidUserID
	}
	if user.Username == "" {
		return auth.ErrInvalidUsername
	}
	if err := validateTrustedProxyIdentity(user.AuthProvider, user.TrustedProxySource, user.TrustedProxyUser, user.OIDCIssuer, user.OIDCSubject); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existingRec, err := s.col.Get(ctx, user.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.ErrUserNotFound
		}
		return err
	}
	var existingStored auth.UserForStorage
	if err := persis.Decode(existingRec, &existingStored); err != nil {
		return fmt.Errorf("user store: decode existing: %w", err)
	}
	if err := validateTrustedProxyIdentity(existingStored.AuthProvider, existingStored.TrustedProxySource, existingStored.TrustedProxyUser, existingStored.OIDCIssuer, existingStored.OIDCSubject); err != nil {
		return fmt.Errorf("user store: existing user: %w", err)
	}
	if existingStored.TrustedProxySource != user.TrustedProxySource || existingStored.TrustedProxyUser != user.TrustedProxyUser {
		return auth.ErrTrustedProxyIdentityImmutable
	}

	data, err := persis.Encode(user.ToStorage())
	if err != nil {
		return err
	}

	oldOIDCKey := oidcKey(existingStored.OIDCIssuer, existingStored.OIDCSubject)
	newOIDCKey := oidcKey(user.OIDCIssuer, user.OIDCSubject)

	// Conflict checks before writing.
	if existingStored.Username != user.Username {
		if id, taken := s.byUsername[user.Username]; taken && id != user.ID {
			return auth.ErrUserAlreadyExists
		}
	}
	if oldOIDCKey != newOIDCKey && user.OIDCIssuer != "" && user.OIDCSubject != "" {
		if id, taken := s.byOIDCIdentity[newOIDCKey]; taken && id != user.ID {
			return auth.ErrOIDCIdentityAlreadyExists
		}
	}

	if err := s.col.Put(ctx, &persis.Record{
		ID:        user.ID,
		Data:      data,
		CreatedAt: existingRec.CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}

	// Update indices after successful write.
	if existingStored.Username != user.Username {
		delete(s.byUsername, existingStored.Username)
		s.byUsername[user.Username] = user.ID
	}
	if oldOIDCKey != newOIDCKey {
		if existingStored.OIDCIssuer != "" && existingStored.OIDCSubject != "" {
			delete(s.byOIDCIdentity, oldOIDCKey)
		}
		if user.OIDCIssuer != "" && user.OIDCSubject != "" {
			s.byOIDCIdentity[newOIDCKey] = user.ID
		}
	}
	return nil
}

// Patch atomically applies selected account fields.
func (s *UserStore) Patch(ctx context.Context, id string, patch auth.UserPatch) (*auth.User, error) {
	if id == "" {
		return nil, auth.ErrInvalidUserID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, auth.ErrUserNotFound
		}
		return nil, err
	}
	var stored auth.UserForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("user store: decode for patch: %w", err)
	}
	if err := validateTrustedProxyIdentity(stored.AuthProvider, stored.TrustedProxySource, stored.TrustedProxyUser, stored.OIDCIssuer, stored.OIDCSubject); err != nil {
		return nil, fmt.Errorf("user store: existing user: %w", err)
	}

	oldUsername := stored.Username
	if patch.Username != nil {
		if *patch.Username == "" {
			return nil, auth.ErrInvalidUsername
		}
		stored.Username = *patch.Username
	}
	if stored.Username == "" {
		return nil, auth.ErrInvalidUsername
	}
	if stored.Username != oldUsername {
		if existingID, taken := s.byUsername[stored.Username]; taken && existingID != id {
			return nil, auth.ErrUserAlreadyExists
		}
	}

	if patch.Role != nil {
		stored.Role = *patch.Role
	}
	if patch.WorkspaceAccess != nil {
		stored.WorkspaceAccess = auth.CloneWorkspaceAccess(patch.WorkspaceAccess)
	}
	if patch.Role != nil || patch.WorkspaceAccess != nil {
		if err := auth.ValidateWorkspaceAccess(stored.Role, stored.WorkspaceAccess, nil); err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	if patch.PasswordHash != nil {
		stored.PasswordHash = *patch.PasswordHash
		stored.PasswordChangedAt = &now
	}
	if patch.IsDisabled != nil {
		stored.IsDisabled = *patch.IsDisabled
	}
	stored.UpdatedAt = now

	data, err := persis.Encode(&stored)
	if err != nil {
		return nil, err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        id,
		Data:      data,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: now,
	}); err != nil {
		return nil, err
	}
	if stored.Username != oldUsername {
		delete(s.byUsername, oldUsername)
		s.byUsername[stored.Username] = id
	}
	return stored.ToUser(), nil
}

// SyncAuthorization updates only externally managed authorization fields.
func (s *UserStore) SyncAuthorization(
	ctx context.Context,
	id string,
	role auth.Role,
	workspaceAccess *auth.WorkspaceAccess,
) (*auth.User, error) {
	if id == "" {
		return nil, auth.ErrInvalidUserID
	}
	if !role.Valid() {
		return nil, auth.ErrInvalidRole
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, auth.ErrUserNotFound
		}
		return nil, err
	}
	var stored auth.UserForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("user store: decode for authorization sync: %w", err)
	}
	if err := validateTrustedProxyIdentity(stored.AuthProvider, stored.TrustedProxySource, stored.TrustedProxyUser, stored.OIDCIssuer, stored.OIDCSubject); err != nil {
		return nil, fmt.Errorf("user store: existing user: %w", err)
	}
	if stored.IsDisabled {
		return nil, auth.ErrUserDisabled
	}

	stored.Role = role
	if workspaceAccess != nil {
		stored.WorkspaceAccess = auth.CloneWorkspaceAccess(workspaceAccess)
	}
	stored.UpdatedAt = time.Now().UTC()
	data, err := persis.Encode(&stored)
	if err != nil {
		return nil, err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        id,
		Data:      data,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: stored.UpdatedAt,
	}); err != nil {
		return nil, err
	}
	return stored.ToUser(), nil
}

// Delete removes a user by their ID.
// Returns [auth.ErrUserNotFound] if the user does not exist.
func (s *UserStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return auth.ErrInvalidUserID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.ErrUserNotFound
		}
		return err
	}
	var stored auth.UserForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return fmt.Errorf("user store: decode for delete: %w", err)
	}

	if err := s.col.Delete(ctx, id); err != nil {
		return err
	}
	delete(s.byUsername, stored.Username)
	if stored.OIDCIssuer != "" && stored.OIDCSubject != "" {
		delete(s.byOIDCIdentity, oidcKey(stored.OIDCIssuer, stored.OIDCSubject))
	}
	if stored.TrustedProxyUser != "" {
		delete(s.byTrustedProxyIdentity, trustedProxyKey(stored.TrustedProxySource, stored.TrustedProxyUser))
	}
	s.count--
	return nil
}

// Count returns the total number of users.
func (s *UserStore) Count(_ context.Context) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// oidcKey creates a composite key for the OIDC identity index.
// URL-encodes both components to prevent collisions when values contain ":".
func oidcKey(issuer, subject string) string {
	return url.QueryEscape(issuer) + ":" + url.QueryEscape(subject)
}

func trustedProxyKey(source, user string) string {
	return url.QueryEscape(source) + ":" + url.QueryEscape(user)
}

func validateTrustedProxyIdentity(provider, source, user, oidcIssuer, oidcSubject string) error {
	if provider == auth.AuthProviderProxy {
		if user == "" || oidcIssuer != "" || oidcSubject != "" {
			return auth.ErrInvalidTrustedProxyIdentity
		}
		return nil
	}
	if source != "" || user != "" {
		return auth.ErrInvalidTrustedProxyIdentity
	}
	return nil
}

func userFromRecord(rec *persis.Record) (*auth.User, error) {
	var stored auth.UserForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("user store: decode record %q: %w", rec.ID, err)
	}
	return stored.ToUser(), nil
}
