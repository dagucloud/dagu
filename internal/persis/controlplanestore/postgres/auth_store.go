// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

var (
	_ auth.UserStore    = (*userStore)(nil)
	_ auth.APIKeyStore  = (*apiKeyStore)(nil)
	_ auth.WebhookStore = (*webhookStore)(nil)
)

type userStore struct{ store *Store }
type apiKeyStore struct{ store *Store }
type webhookStore struct{ store *Store }

// Users returns the user store.
func (s *Store) Users() auth.UserStore {
	return &userStore{store: s}
}

// APIKeys returns the API key store.
func (s *Store) APIKeys() auth.APIKeyStore {
	return &apiKeyStore{store: s}
}

// Webhooks returns the webhook store.
func (s *Store) Webhooks() auth.WebhookStore {
	return &webhookStore{store: s}
}

func (s *userStore) Create(ctx context.Context, user *auth.User) error {
	if user == nil {
		return errors.New("postgres user store: user cannot be nil")
	}
	if user.Username == "" {
		return auth.ErrInvalidUsername
	}
	if !user.Role.Valid() {
		return auth.ErrInvalidRole
	}
	id, err := ensureUserID(user)
	if err != nil {
		return err
	}
	if (user.OIDCIssuer == "") != (user.OIDCSubject == "") {
		return auth.ErrOIDCIdentityNotFound
	}
	workspaceAccess, err := marshalWorkspaceAccess(user.WorkspaceAccess)
	if err != nil {
		return err
	}
	data, err := json.Marshal(user.ToStorage())
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}
	err = s.store.queries.CreateUser(ctx, db.CreateUserParams{
		ID:              id,
		Username:        user.Username,
		PasswordHash:    user.PasswordHash,
		Role:            string(user.Role),
		AuthProvider:    user.AuthProvider,
		OidcIssuer:      user.OIDCIssuer,
		OidcSubject:     user.OIDCSubject,
		IsDisabled:      user.IsDisabled,
		WorkspaceAccess: workspaceAccess,
		Data:            data,
		CreatedAt:       timestamptz(user.CreatedAt),
		UpdatedAt:       timestamptz(user.UpdatedAt),
	})
	if err != nil {
		return mapUserCreateError(err)
	}
	return nil
}

func (s *userStore) GetByID(ctx context.Context, id string) (*auth.User, error) {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return nil, auth.ErrInvalidUserID
	}
	row, err := s.store.queries.GetUserByID(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return userFromRow(row)
}

func (s *userStore) GetByUsername(ctx context.Context, username string) (*auth.User, error) {
	if username == "" {
		return nil, auth.ErrInvalidUsername
	}
	row, err := s.store.queries.GetUserByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return userFromRow(row)
}

func (s *userStore) GetByOIDCIdentity(ctx context.Context, issuer, subject string) (*auth.User, error) {
	if issuer == "" || subject == "" {
		return nil, auth.ErrOIDCIdentityNotFound
	}
	row, err := s.store.queries.GetUserByOIDCIdentity(ctx, db.GetUserByOIDCIdentityParams{
		OidcIssuer:  pgtype.Text{String: issuer, Valid: true},
		OidcSubject: pgtype.Text{String: subject, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrOIDCIdentityNotFound
	}
	if err != nil {
		return nil, err
	}
	return userFromRow(row)
}

func (s *userStore) List(ctx context.Context) ([]*auth.User, error) {
	rows, err := s.store.queries.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]*auth.User, 0, len(rows))
	for _, row := range rows {
		user, err := userFromRow(row)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func (s *userStore) Update(ctx context.Context, user *auth.User) error {
	if user == nil {
		return errors.New("postgres user store: user cannot be nil")
	}
	id, err := parseUUIDv7(user.ID)
	if err != nil {
		return auth.ErrInvalidUserID
	}
	if user.Username == "" {
		return auth.ErrInvalidUsername
	}
	if !user.Role.Valid() {
		return auth.ErrInvalidRole
	}
	workspaceAccess, err := marshalWorkspaceAccess(user.WorkspaceAccess)
	if err != nil {
		return err
	}
	data, err := json.Marshal(user.ToStorage())
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}
	rows, err := s.store.queries.UpdateUser(ctx, db.UpdateUserParams{
		Username:        user.Username,
		PasswordHash:    user.PasswordHash,
		Role:            string(user.Role),
		AuthProvider:    user.AuthProvider,
		OidcIssuer:      user.OIDCIssuer,
		OidcSubject:     user.OIDCSubject,
		IsDisabled:      user.IsDisabled,
		WorkspaceAccess: workspaceAccess,
		Data:            data,
		UpdatedAt:       timestamptz(user.UpdatedAt),
		ID:              id,
	})
	if err != nil {
		return mapUserCreateError(err)
	}
	if rows == 0 {
		return auth.ErrUserNotFound
	}
	return nil
}

func (s *userStore) Delete(ctx context.Context, id string) error {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return auth.ErrInvalidUserID
	}
	rows, err := s.store.queries.DeleteUser(ctx, uid)
	if err != nil {
		return err
	}
	if rows == 0 {
		return auth.ErrUserNotFound
	}
	return nil
}

func (s *userStore) Count(ctx context.Context) (int64, error) {
	return s.store.queries.CountUsers(ctx)
}

func (s *apiKeyStore) Create(ctx context.Context, key *auth.APIKey) error {
	if key == nil {
		return errors.New("postgres api key store: API key cannot be nil")
	}
	if key.Name == "" {
		return auth.ErrInvalidAPIKeyName
	}
	if key.KeyHash == "" {
		return auth.ErrInvalidAPIKeyHash
	}
	if !key.Role.Valid() {
		return auth.ErrInvalidRole
	}
	id, err := ensureAPIKeyID(key)
	if err != nil {
		return err
	}
	params, err := apiKeyParams(id, key)
	if err != nil {
		return err
	}
	if err := s.store.queries.CreateAPIKey(ctx, params); err != nil {
		if isUniqueViolation(err) {
			return auth.ErrAPIKeyAlreadyExists
		}
		return err
	}
	return nil
}

func (s *apiKeyStore) GetByID(ctx context.Context, id string) (*auth.APIKey, error) {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return nil, auth.ErrInvalidAPIKeyID
	}
	row, err := s.store.queries.GetAPIKeyByID(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrAPIKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return apiKeyFromRow(row)
}

func (s *apiKeyStore) List(ctx context.Context) ([]*auth.APIKey, error) {
	rows, err := s.store.queries.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]*auth.APIKey, 0, len(rows))
	for _, row := range rows {
		key, err := apiKeyFromRow(row)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (s *apiKeyStore) Update(ctx context.Context, key *auth.APIKey) error {
	if key == nil {
		return errors.New("postgres api key store: API key cannot be nil")
	}
	id, err := parseUUIDv7(key.ID)
	if err != nil {
		return auth.ErrInvalidAPIKeyID
	}
	params, err := updateAPIKeyParams(id, key)
	if err != nil {
		return err
	}
	rows, err := s.store.queries.UpdateAPIKey(ctx, params)
	if err != nil {
		if isUniqueViolation(err) {
			return auth.ErrAPIKeyAlreadyExists
		}
		return err
	}
	if rows == 0 {
		return auth.ErrAPIKeyNotFound
	}
	return nil
}

func (s *apiKeyStore) Delete(ctx context.Context, id string) error {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return auth.ErrInvalidAPIKeyID
	}
	rows, err := s.store.queries.DeleteAPIKey(ctx, uid)
	if err != nil {
		return err
	}
	if rows == 0 {
		return auth.ErrAPIKeyNotFound
	}
	return nil
}

func (s *apiKeyStore) UpdateLastUsed(ctx context.Context, id string) error {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return auth.ErrInvalidAPIKeyID
	}
	rows, err := s.store.queries.UpdateAPIKeyLastUsed(ctx, db.UpdateAPIKeyLastUsedParams{
		LastUsedAt: timestamptz(time.Now().UTC()),
		ID:         uid,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return auth.ErrAPIKeyNotFound
	}
	return nil
}

func (s *webhookStore) Create(ctx context.Context, webhook *auth.Webhook) error {
	if webhook == nil {
		return errors.New("postgres webhook store: webhook cannot be nil")
	}
	if webhook.DAGName == "" {
		return auth.ErrInvalidWebhookDAGName
	}
	if err := core.ValidateDAGName(webhook.DAGName); err != nil {
		return auth.ErrInvalidWebhookDAGName
	}
	if webhook.TokenHash == "" {
		return auth.ErrInvalidWebhookTokenHash
	}
	id, err := ensureWebhookID(webhook)
	if err != nil {
		return err
	}
	params, err := s.webhookParams(id, webhook)
	if err != nil {
		return err
	}
	if err := s.store.queries.CreateWebhook(ctx, params); err != nil {
		if isUniqueViolation(err) {
			return auth.ErrWebhookAlreadyExists
		}
		return err
	}
	return nil
}

func (s *webhookStore) GetByID(ctx context.Context, id string) (*auth.Webhook, error) {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return nil, auth.ErrInvalidWebhookID
	}
	row, err := s.store.queries.GetWebhookByID(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrWebhookNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.webhookFromRow(row)
}

func (s *webhookStore) GetByDAGName(ctx context.Context, dagName string) (*auth.Webhook, error) {
	if err := core.ValidateDAGName(dagName); err != nil {
		return nil, auth.ErrInvalidWebhookDAGName
	}
	row, err := s.store.queries.GetWebhookByDAGName(ctx, dagName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrWebhookNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.webhookFromRow(row)
}

func (s *webhookStore) List(ctx context.Context) ([]*auth.Webhook, error) {
	rows, err := s.store.queries.ListWebhooks(ctx)
	if err != nil {
		return nil, err
	}
	webhooks := make([]*auth.Webhook, 0, len(rows))
	for _, row := range rows {
		webhook, err := s.webhookFromRow(row)
		if err != nil {
			return nil, err
		}
		webhooks = append(webhooks, webhook)
	}
	return webhooks, nil
}

func (s *webhookStore) Update(ctx context.Context, webhook *auth.Webhook) error {
	if webhook == nil {
		return errors.New("postgres webhook store: webhook cannot be nil")
	}
	id, err := parseUUIDv7(webhook.ID)
	if err != nil {
		return auth.ErrInvalidWebhookID
	}
	params, err := s.updateWebhookParams(id, webhook)
	if err != nil {
		return err
	}
	rows, err := s.store.queries.UpdateWebhook(ctx, params)
	if err != nil {
		if isUniqueViolation(err) {
			return auth.ErrWebhookAlreadyExists
		}
		return err
	}
	if rows == 0 {
		return auth.ErrWebhookNotFound
	}
	return nil
}

func (s *webhookStore) Delete(ctx context.Context, id string) error {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return auth.ErrInvalidWebhookID
	}
	rows, err := s.store.queries.DeleteWebhook(ctx, uid)
	if err != nil {
		return err
	}
	if rows == 0 {
		return auth.ErrWebhookNotFound
	}
	return nil
}

func (s *webhookStore) DeleteByDAGName(ctx context.Context, dagName string) error {
	if err := core.ValidateDAGName(dagName); err != nil {
		return auth.ErrInvalidWebhookDAGName
	}
	rows, err := s.store.queries.DeleteWebhookByDAGName(ctx, dagName)
	if err != nil {
		return err
	}
	if rows == 0 {
		return auth.ErrWebhookNotFound
	}
	return nil
}

func (s *webhookStore) UpdateLastUsed(ctx context.Context, id string) error {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return auth.ErrInvalidWebhookID
	}
	rows, err := s.store.queries.UpdateWebhookLastUsed(ctx, db.UpdateWebhookLastUsedParams{
		LastUsedAt: timestamptz(time.Now().UTC()),
		ID:         uid,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return auth.ErrWebhookNotFound
	}
	return nil
}

func ensureUserID(user *auth.User) (uuid.UUID, error) {
	idString, id, err := ensureUUIDv7String(user.ID)
	if err != nil {
		return uuid.Nil, auth.ErrInvalidUserID
	}
	user.ID = idString
	return id, nil
}

func ensureAPIKeyID(key *auth.APIKey) (uuid.UUID, error) {
	idString, id, err := ensureUUIDv7String(key.ID)
	if err != nil {
		return uuid.Nil, auth.ErrInvalidAPIKeyID
	}
	key.ID = idString
	return id, nil
}

func ensureWebhookID(webhook *auth.Webhook) (uuid.UUID, error) {
	idString, id, err := ensureUUIDv7String(webhook.ID)
	if err != nil {
		return uuid.Nil, auth.ErrInvalidWebhookID
	}
	webhook.ID = idString
	return id, nil
}

func marshalWorkspaceAccess(access *auth.WorkspaceAccess) ([]byte, error) {
	if access == nil {
		return nil, nil
	}
	data, err := json.Marshal(auth.CloneWorkspaceAccess(access))
	if err != nil {
		return nil, fmt.Errorf("marshal workspace access: %w", err)
	}
	return data, nil
}

func userFromRow(row db.DaguUser) (*auth.User, error) {
	var stored auth.UserForStorage
	if err := json.Unmarshal(row.Data, &stored); err != nil {
		return nil, fmt.Errorf("unmarshal user: %w", err)
	}
	user := stored.ToUser()
	user.ID = row.ID.String()
	user.Username = row.Username
	user.PasswordHash = row.PasswordHash
	user.Role = auth.Role(row.Role)
	user.AuthProvider = row.AuthProvider.String
	user.OIDCIssuer = row.OidcIssuer.String
	user.OIDCSubject = row.OidcSubject.String
	user.IsDisabled = row.IsDisabled
	user.CreatedAt = timeFromTimestamptz(row.CreatedAt)
	user.UpdatedAt = timeFromTimestamptz(row.UpdatedAt)
	return user, nil
}

func apiKeyParams(id uuid.UUID, key *auth.APIKey) (db.CreateAPIKeyParams, error) {
	workspaceAccess, data, err := apiKeyStorageData(key)
	if err != nil {
		return db.CreateAPIKeyParams{}, err
	}
	return db.CreateAPIKeyParams{
		ID:              id,
		Name:            key.Name,
		Role:            string(key.Role),
		KeyHash:         key.KeyHash,
		KeyPrefix:       key.KeyPrefix,
		CreatedBy:       key.CreatedBy,
		WorkspaceAccess: workspaceAccess,
		LastUsedAt:      timePtrTimestamptz(key.LastUsedAt),
		Data:            data,
		CreatedAt:       timestamptz(key.CreatedAt),
		UpdatedAt:       timestamptz(key.UpdatedAt),
	}, nil
}

func updateAPIKeyParams(id uuid.UUID, key *auth.APIKey) (db.UpdateAPIKeyParams, error) {
	workspaceAccess, data, err := apiKeyStorageData(key)
	if err != nil {
		return db.UpdateAPIKeyParams{}, err
	}
	return db.UpdateAPIKeyParams{
		ID:              id,
		Name:            key.Name,
		Role:            string(key.Role),
		KeyHash:         key.KeyHash,
		KeyPrefix:       key.KeyPrefix,
		CreatedBy:       key.CreatedBy,
		WorkspaceAccess: workspaceAccess,
		LastUsedAt:      timePtrTimestamptz(key.LastUsedAt),
		Data:            data,
		UpdatedAt:       timestamptz(key.UpdatedAt),
	}, nil
}

func apiKeyStorageData(key *auth.APIKey) ([]byte, []byte, error) {
	if key.Name == "" {
		return nil, nil, auth.ErrInvalidAPIKeyName
	}
	if key.KeyHash == "" {
		return nil, nil, auth.ErrInvalidAPIKeyHash
	}
	if !key.Role.Valid() {
		return nil, nil, auth.ErrInvalidRole
	}
	workspaceAccess, err := marshalWorkspaceAccess(key.WorkspaceAccess)
	if err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(key.ToStorage())
	if err != nil {
		return nil, nil, fmt.Errorf("marshal API key: %w", err)
	}
	return workspaceAccess, data, nil
}

func apiKeyFromRow(row db.DaguApiKey) (*auth.APIKey, error) {
	var stored auth.APIKeyForStorage
	if err := json.Unmarshal(row.Data, &stored); err != nil {
		return nil, fmt.Errorf("unmarshal API key: %w", err)
	}
	key := stored.ToAPIKey()
	key.ID = row.ID.String()
	key.Name = row.Name
	key.Role = auth.Role(row.Role)
	key.KeyHash = row.KeyHash
	key.KeyPrefix = row.KeyPrefix
	key.CreatedBy = row.CreatedBy
	key.CreatedAt = timeFromTimestamptz(row.CreatedAt)
	key.UpdatedAt = timeFromTimestamptz(row.UpdatedAt)
	if row.LastUsedAt.Valid {
		t := timeFromTimestamptz(row.LastUsedAt)
		key.LastUsedAt = &t
	}
	return key, nil
}

func (s *webhookStore) webhookParams(id uuid.UUID, webhook *auth.Webhook) (db.CreateWebhookParams, error) {
	data, err := s.webhookStorageData(webhook)
	if err != nil {
		return db.CreateWebhookParams{}, err
	}
	return db.CreateWebhookParams{
		ID:                  id,
		DagName:             webhook.DAGName,
		TokenHash:           webhook.TokenHash,
		TokenPrefix:         webhook.TokenPrefix,
		Enabled:             webhook.Enabled,
		AuthMode:            string(webhook.AuthMode),
		HmacEnforcementMode: string(webhook.HMACEnforcementMode),
		CreatedBy:           webhook.CreatedBy,
		LastUsedAt:          timePtrTimestamptz(webhook.LastUsedAt),
		Data:                data,
		CreatedAt:           timestamptz(webhook.CreatedAt),
		UpdatedAt:           timestamptz(webhook.UpdatedAt),
	}, nil
}

func (s *webhookStore) updateWebhookParams(id uuid.UUID, webhook *auth.Webhook) (db.UpdateWebhookParams, error) {
	data, err := s.webhookStorageData(webhook)
	if err != nil {
		return db.UpdateWebhookParams{}, err
	}
	return db.UpdateWebhookParams{
		ID:                  id,
		DagName:             webhook.DAGName,
		TokenHash:           webhook.TokenHash,
		TokenPrefix:         webhook.TokenPrefix,
		Enabled:             webhook.Enabled,
		AuthMode:            string(webhook.AuthMode),
		HmacEnforcementMode: string(webhook.HMACEnforcementMode),
		CreatedBy:           webhook.CreatedBy,
		LastUsedAt:          timePtrTimestamptz(webhook.LastUsedAt),
		Data:                data,
		UpdatedAt:           timestamptz(webhook.UpdatedAt),
	}, nil
}

func (s *webhookStore) webhookStorageData(webhook *auth.Webhook) ([]byte, error) {
	stored := webhook.ToStorage()
	if webhook.HMACSecret != "" {
		if s.store.webhookEncryptor == nil {
			return nil, auth.ErrWebhookHMACEncryptorRequired
		}
		enc, err := s.store.webhookEncryptor.Encrypt(webhook.HMACSecret)
		if err != nil {
			return nil, fmt.Errorf("postgres webhook store: encrypt HMAC secret: %w", err)
		}
		stored.HMACSecretEnc = enc
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal webhook: %w", err)
	}
	return data, nil
}

func (s *webhookStore) webhookFromRow(row db.DaguWebhook) (*auth.Webhook, error) {
	var stored auth.WebhookForStorage
	if err := json.Unmarshal(row.Data, &stored); err != nil {
		return nil, fmt.Errorf("unmarshal webhook: %w", err)
	}
	webhook := stored.ToWebhook()
	webhook.ID = row.ID.String()
	webhook.DAGName = row.DagName
	webhook.TokenHash = row.TokenHash
	webhook.TokenPrefix = row.TokenPrefix
	webhook.Enabled = row.Enabled
	webhook.AuthMode = auth.WebhookAuthMode(row.AuthMode.String)
	webhook.HMACEnforcementMode = auth.WebhookHMACEnforcementMode(row.HmacEnforcementMode.String)
	webhook.CreatedBy = row.CreatedBy
	webhook.CreatedAt = timeFromTimestamptz(row.CreatedAt)
	webhook.UpdatedAt = timeFromTimestamptz(row.UpdatedAt)
	if row.LastUsedAt.Valid {
		t := timeFromTimestamptz(row.LastUsedAt)
		webhook.LastUsedAt = &t
	}
	if stored.HMACSecretEnc != "" {
		if s.store.webhookEncryptor == nil {
			return nil, auth.ErrWebhookHMACEncryptorRequired
		}
		secret, err := s.store.webhookEncryptor.Decrypt(stored.HMACSecretEnc)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", auth.ErrWebhookHMACDecryptFailed, err)
		}
		webhook.HMACSecret = secret
	}
	return webhook, nil
}

func mapUserCreateError(err error) error {
	if !isUniqueViolation(err) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "dagu_users_oidc_identity_uidx" {
		return auth.ErrOIDCIdentityAlreadyExists
	}
	return auth.ErrUserAlreadyExists
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func timePtrTimestamptz(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return timestamptz(*value)
}
