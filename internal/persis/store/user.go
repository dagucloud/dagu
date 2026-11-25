// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/persis"
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
	s := &UserStore{col: col}
	if err := s.rebuildIndexLocked(context.Background()); err != nil {
		return nil, fmt.Errorf("user store: build index: %w", err)
	}
	return s, nil
}

// rebuildIndexLocked replaces the indexes with ones built from the collection
// and keeps the current ones on error. The caller holds mu.
func (s *UserStore) rebuildIndexLocked(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	byUsername := make(map[string]string, len(recs))
	byOIDCIdentity := make(map[string]string)
	byTrustedProxyIdentity := make(map[string]string)
	var count int64
	for _, rec := range recs {
		var stored auth.UserForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			continue
		}
		if err := validateTrustedProxyIdentity(stored.AuthProvider, stored.TrustedProxySource, stored.TrustedProxyUser, stored.OIDCIssuer, stored.OIDCSubject); err != nil {
			return fmt.Errorf("user %q: %w", stored.ID, err)
		}
		if otherID, exists := byUsername[stored.Username]; exists {
			logger.Warn(ctx, "user store: two records share a username",
				slog.String("username", stored.Username),
				slog.String("login_user_id", stored.ID),
				slog.String("other_user_id", otherID))
		}
		byUsername[stored.Username] = stored.ID
		if stored.OIDCIssuer != "" && stored.OIDCSubject != "" {
			byOIDCIdentity[oidcKey(stored.OIDCIssuer, stored.OIDCSubject)] = stored.ID
		}
		if stored.TrustedProxyUser != "" {
			key := trustedProxyKey(stored.TrustedProxySource, stored.TrustedProxyUser)
			if _, exists := byTrustedProxyIdentity[key]; exists {
				return fmt.Errorf("user %q: %w", stored.ID, auth.ErrTrustedProxyIdentityAlreadyExists)
			}
			byTrustedProxyIdentity[key] = stored.ID
		}
		count++
	}
	s.byUsername, s.byOIDCIdentity, s.byTrustedProxyIdentity = byUsername, byOIDCIdentity, byTrustedProxyIdentity
	s.count = count
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
	claimed := userKeys{username: user.Username}
	if user.OIDCIssuer != "" && user.OIDCSubject != "" {
		claimed.oidc = oidcKey(user.OIDCIssuer, user.OIDCSubject)
	}
	if user.TrustedProxyUser != "" {
		claimed.proxy = trustedProxyKey(user.TrustedProxySource, user.TrustedProxyUser)
	}
	if err := s.findStoredConflict(ctx, user.ID, claimed); err != nil {
		return err
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
	var claimed userKeys
	if existingStored.Username != user.Username {
		if id, taken := s.byUsername[user.Username]; taken && id != user.ID {
			return auth.ErrUserAlreadyExists
		}
		claimed.username = user.Username
	}
	if oldOIDCKey != newOIDCKey && user.OIDCIssuer != "" && user.OIDCSubject != "" {
		if id, taken := s.byOIDCIdentity[newOIDCKey]; taken && id != user.ID {
			return auth.ErrOIDCIdentityAlreadyExists
		}
		claimed.oidc = newOIDCKey
	}
	if err := s.findStoredConflict(ctx, user.ID, claimed); err != nil {
		return err
	}
	release := func() {}
	if claimed.username != "" {
		if release, err = s.releaseUsername(ctx, existingStored.Username, user.ID); err != nil {
			return err
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
		release()
		s.byUsername[user.Username] = user.ID
	}
	if oldOIDCKey != newOIDCKey {
		if existingStored.OIDCIssuer != "" && existingStored.OIDCSubject != "" {
			deleteOwned(s.byOIDCIdentity, oldOIDCKey, user.ID)
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
	release := func() {}
	if stored.Username != oldUsername {
		if existingID, taken := s.byUsername[stored.Username]; taken && existingID != id {
			return nil, auth.ErrUserAlreadyExists
		}
		if err := s.findStoredConflict(ctx, id, userKeys{username: stored.Username}); err != nil {
			return nil, err
		}
		if release, err = s.releaseUsername(ctx, oldUsername, id); err != nil {
			return nil, err
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
		release()
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
	projectGrants *[]auth.ProjectGrant,
) (auth.AuthorizationSyncResult, error) {
	if id == "" {
		return auth.AuthorizationSyncResult{}, auth.ErrInvalidUserID
	}
	if !role.Valid() {
		return auth.AuthorizationSyncResult{}, auth.ErrInvalidRole
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return auth.AuthorizationSyncResult{}, auth.ErrUserNotFound
		}
		return auth.AuthorizationSyncResult{}, err
	}
	var stored auth.UserForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return auth.AuthorizationSyncResult{}, fmt.Errorf("user store: decode for authorization sync: %w", err)
	}
	if err := validateTrustedProxyIdentity(stored.AuthProvider, stored.TrustedProxySource, stored.TrustedProxyUser, stored.OIDCIssuer, stored.OIDCSubject); err != nil {
		return auth.AuthorizationSyncResult{}, fmt.Errorf("user store: existing user: %w", err)
	}
	if stored.IsDisabled {
		return auth.AuthorizationSyncResult{}, auth.ErrUserDisabled
	}

	result := auth.AuthorizationSyncResult{
		PreviousRole:            stored.Role,
		PreviousWorkspaceAccess: auth.CloneWorkspaceAccess(stored.WorkspaceAccess),
		PreviousProjectGrants:   slices.Clone(stored.ProjectGrants),
	}
	if stored.Role == role &&
		(workspaceAccess == nil || auth.WorkspaceAccessEqual(stored.WorkspaceAccess, workspaceAccess)) &&
		(projectGrants == nil || projectGrantsEqual(stored.ProjectGrants, *projectGrants)) {
		result.User = stored.ToUser()
		return result, nil
	}

	stored.Role = role
	if workspaceAccess != nil {
		stored.WorkspaceAccess = auth.CloneWorkspaceAccess(workspaceAccess)
	}
	if projectGrants != nil {
		stored.ProjectGrants = slices.Clone(*projectGrants)
	}
	stored.UpdatedAt = time.Now().UTC()
	data, err := persis.Encode(&stored)
	if err != nil {
		return auth.AuthorizationSyncResult{}, err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        id,
		Data:      data,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: stored.UpdatedAt,
	}); err != nil {
		return auth.AuthorizationSyncResult{}, err
	}
	result.User = stored.ToUser()
	result.Changed = true
	return result, nil
}

// projectGrantsEqual reports whether left and right hold the same grants in
// any order.
func projectGrantsEqual(left, right []auth.ProjectGrant) bool {
	compare := func(a, b auth.ProjectGrant) int {
		return cmp.Or(strings.Compare(a.ProjectID, b.ProjectID), strings.Compare(string(a.Role), string(b.Role)))
	}
	return slices.Equal(slices.SortedFunc(slices.Values(left), compare), slices.SortedFunc(slices.Values(right), compare))
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

	release, err := s.releaseUsername(ctx, stored.Username, id)
	if err != nil {
		return err
	}
	if err := s.col.Delete(ctx, id); err != nil {
		return err
	}
	release()
	if stored.OIDCIssuer != "" && stored.OIDCSubject != "" {
		deleteOwned(s.byOIDCIdentity, oidcKey(stored.OIDCIssuer, stored.OIDCSubject), id)
	}
	if stored.TrustedProxyUser != "" {
		deleteOwned(s.byTrustedProxyIdentity, trustedProxyKey(stored.TrustedProxySource, stored.TrustedProxyUser), id)
	}
	s.count--
	return nil
}

// Count returns the total number of users. While the store holds no users,
// Count re-reads the collection, so it counts a user that another process
// sharing the collection created.
func (s *UserStore) Count(ctx context.Context) (int64, error) {
	s.mu.RLock()
	n := s.count
	s.mu.RUnlock()
	if n > 0 {
		return n, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == 0 {
		if err := s.rebuildIndexLocked(ctx); err != nil {
			return 0, err
		}
	}
	return s.count, nil
}

// releaseUsername prepares to drop id's index entry for username. It finds
// another stored record that still has the username, as a restart would, and
// returns a func that moves the entry to it once the caller's write succeeds.
// The caller holds mu.
func (s *UserStore) releaseUsername(ctx context.Context, username, id string) (func(), error) {
	if s.byUsername[username] != id {
		return func() {}, nil
	}
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	var next string
	for _, rec := range recs {
		var stored auth.UserForStorage
		if rec.ID == id || persis.Decode(rec, &stored) != nil {
			continue
		}
		if stored.Username == username {
			next = stored.ID
		}
	}
	return func() {
		if next == "" {
			delete(s.byUsername, username)
			return
		}
		s.byUsername[username] = next
	}, nil
}

// userKeys holds the unique keys a write claims. An empty field is not
// checked.
type userKeys struct {
	username string
	oidc     string
	proxy    string
}

// findStoredConflict reports a key in claimed that a record other than id
// already holds in the collection. The indexes miss records written by
// another process sharing the collection; this read does not.
func (s *UserStore) findStoredConflict(ctx context.Context, id string, claimed userKeys) error {
	if claimed == (userKeys{}) {
		return nil
	}
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if rec.ID == id {
			continue
		}
		var stored auth.UserForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			continue
		}
		switch {
		case claimed.proxy != "" && trustedProxyKey(stored.TrustedProxySource, stored.TrustedProxyUser) == claimed.proxy:
			return auth.ErrTrustedProxyIdentityAlreadyExists
		case claimed.username != "" && stored.Username == claimed.username:
			return auth.ErrUserAlreadyExists
		case claimed.oidc != "" && oidcKey(stored.OIDCIssuer, stored.OIDCSubject) == claimed.oidc:
			return auth.ErrOIDCIdentityAlreadyExists
		}
	}
	return nil
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

// deleteOwned removes key only while it maps to id, so removing one of two
// records that share a key keeps the other one indexed.
func deleteOwned(index map[string]string, key, id string) {
	if index[key] == id {
		delete(index, key)
	}
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
