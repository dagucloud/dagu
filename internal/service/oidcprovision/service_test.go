// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oidcprovision

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/internal/auth"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockUserStore implements auth.UserStore for testing.
type mockUserStore struct {
	users          map[string]*auth.User
	byUsername     map[string]*auth.User
	byOIDCIdentity map[string]*auth.User

	// Error injection for testing error paths
	getByOIDCIdentityErr error
	createErr            error
	updateErr            error
	updateCalls          int
}

func newMockUserStore() *mockUserStore {
	return &mockUserStore{
		users:          make(map[string]*auth.User),
		byUsername:     make(map[string]*auth.User),
		byOIDCIdentity: make(map[string]*auth.User),
	}
}

func (m *mockUserStore) Create(_ context.Context, user *auth.User) error {
	if m.createErr != nil {
		return m.createErr
	}
	if _, exists := m.users[user.ID]; exists {
		return auth.ErrUserAlreadyExists
	}
	if _, exists := m.byUsername[user.Username]; exists {
		return auth.ErrUserAlreadyExists
	}
	m.users[user.ID] = user
	m.byUsername[user.Username] = user
	if user.OIDCIssuer != "" && user.OIDCSubject != "" {
		key := user.OIDCIssuer + ":" + user.OIDCSubject
		m.byOIDCIdentity[key] = user
	}
	return nil
}

func (m *mockUserStore) GetByID(_ context.Context, id string) (*auth.User, error) {
	user, exists := m.users[id]
	if !exists {
		return nil, auth.ErrUserNotFound
	}
	return user, nil
}

func (m *mockUserStore) GetByUsername(_ context.Context, username string) (*auth.User, error) {
	user, exists := m.byUsername[username]
	if !exists {
		return nil, auth.ErrUserNotFound
	}
	return user, nil
}

func (m *mockUserStore) GetByOIDCIdentity(_ context.Context, issuer, subject string) (*auth.User, error) {
	if m.getByOIDCIdentityErr != nil {
		return nil, m.getByOIDCIdentityErr
	}
	key := issuer + ":" + subject
	user, exists := m.byOIDCIdentity[key]
	if !exists {
		return nil, auth.ErrOIDCIdentityNotFound
	}
	return user, nil
}

func (m *mockUserStore) List(_ context.Context) ([]*auth.User, error) {
	users := make([]*auth.User, 0, len(m.users))
	for _, u := range m.users {
		users = append(users, u)
	}
	return users, nil
}

func (m *mockUserStore) Update(_ context.Context, user *auth.User) error {
	m.updateCalls++
	if m.updateErr != nil {
		return m.updateErr
	}
	if _, exists := m.users[user.ID]; !exists {
		return auth.ErrUserNotFound
	}
	m.users[user.ID] = user
	m.byUsername[user.Username] = user
	if user.OIDCIssuer != "" && user.OIDCSubject != "" {
		key := user.OIDCIssuer + ":" + user.OIDCSubject
		m.byOIDCIdentity[key] = user
	}
	return nil
}

func (m *mockUserStore) Delete(_ context.Context, id string) error {
	user, exists := m.users[id]
	if !exists {
		return auth.ErrUserNotFound
	}
	delete(m.users, id)
	delete(m.byUsername, user.Username)
	if user.OIDCIssuer != "" && user.OIDCSubject != "" {
		key := user.OIDCIssuer + ":" + user.OIDCSubject
		delete(m.byOIDCIdentity, key)
	}
	return nil
}

func (m *mockUserStore) Count(_ context.Context) (int64, error) {
	return int64(len(m.users)), nil
}

func TestProcessLogin_NewUser(t *testing.T) {
	store := newMockUserStore()
	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	claims := OIDCClaims{
		Subject:           "sub123",
		Email:             "user@example.com",
		PreferredUsername: "testuser",
	}

	user, isNew, err := svc.ProcessLogin(context.Background(), claims)
	require.NoError(t, err)
	assert.True(t, isNew)
	assert.NotNil(t, user)
	assert.Equal(t, "testuser", user.Username)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, "oidc", user.AuthProvider)
	assert.Equal(t, "https://issuer.example.com", user.OIDCIssuer)
	assert.Equal(t, "sub123", user.OIDCSubject)
}

func TestProcessLogin_ExistingUser(t *testing.T) {
	store := newMockUserStore()
	existingUser := &auth.User{
		ID:           "existing-id",
		Username:     "existinguser",
		Role:         auth.RoleOperator,
		AuthProvider: "oidc",
		OIDCIssuer:   "https://issuer.example.com",
		OIDCSubject:  "sub123",
	}
	_ = store.Create(context.Background(), existingUser)

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	claims := OIDCClaims{
		Subject:           "sub123",
		Email:             "user@example.com",
		PreferredUsername: "testuser",
	}

	user, isNew, err := svc.ProcessLogin(context.Background(), claims)
	require.NoError(t, err)
	assert.False(t, isNew)
	assert.Equal(t, "existing-id", user.ID)
	assert.Equal(t, "existinguser", user.Username)
	// Role should not change on subsequent logins
	assert.Equal(t, auth.RoleOperator, user.Role)
}

func TestProcessLogin_DisabledUser(t *testing.T) {
	store := newMockUserStore()
	existingUser := &auth.User{
		ID:           "disabled-id",
		Username:     "disableduser",
		Role:         auth.RoleViewer,
		AuthProvider: "oidc",
		OIDCIssuer:   "https://issuer.example.com",
		OIDCSubject:  "sub123",
		IsDisabled:   true,
	}
	_ = store.Create(context.Background(), existingUser)

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	claims := OIDCClaims{
		Subject:           "sub123",
		Email:             "user@example.com",
		PreferredUsername: "testuser",
	}

	user, isNew, err := svc.ProcessLogin(context.Background(), claims)
	assert.ErrorIs(t, err, authservice.ErrUserDisabled)
	assert.Nil(t, user)
	assert.False(t, isNew)
}

func TestProcessLogin_ErrorCases(t *testing.T) {
	tests := []struct {
		name          string
		config        Config
		claims        OIDCClaims
		expectedError error
	}{
		{
			name: "auto_signup_disabled",
			config: Config{
				Issuer:      "https://issuer.example.com",
				AutoSignup:  false,
				DefaultRole: auth.RoleViewer,
			},
			claims: OIDCClaims{
				Subject:           "sub123",
				Email:             "user@example.com",
				PreferredUsername: "testuser",
			},
			expectedError: ErrAutoSignupDisabled,
		},
		{
			name: "email_required",
			config: Config{
				Issuer:      "https://issuer.example.com",
				AutoSignup:  true,
				DefaultRole: auth.RoleViewer,
			},
			claims: OIDCClaims{
				Subject:           "sub123",
				Email:             "",
				PreferredUsername: "testuser",
			},
			expectedError: ErrEmailRequired,
		},
		{
			name: "email_not_allowed_domain",
			config: Config{
				Issuer:         "https://issuer.example.com",
				AutoSignup:     true,
				DefaultRole:    auth.RoleViewer,
				AllowedDomains: []string{"company.com", "partner.org"},
			},
			claims: OIDCClaims{
				Subject:           "sub123",
				Email:             "user@unauthorized.com",
				PreferredUsername: "testuser",
			},
			expectedError: ErrEmailNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMockUserStore()
			svc, err := New(store, tc.config)
			require.NoError(t, err)

			user, isNew, err := svc.ProcessLogin(context.Background(), tc.claims)
			assert.ErrorIs(t, err, tc.expectedError)
			assert.Nil(t, user)
			assert.False(t, isNew)
		})
	}
}

func TestGenerateUniqueUsername(t *testing.T) {
	tests := []struct {
		name           string
		existingUsers  []*auth.User
		claims         OIDCClaims
		expectedResult string
	}{
		{
			name:          "no_conflict",
			existingUsers: nil,
			claims: OIDCClaims{
				Subject:           "sub123",
				Email:             "john.doe@example.com",
				PreferredUsername: "johndoe",
			},
			expectedResult: "johndoe",
		},
		{
			name: "conflict_with_oidc_user",
			existingUsers: []*auth.User{
				{
					ID:           "existing-id",
					Username:     "johndoe",
					AuthProvider: "oidc",
					OIDCIssuer:   "https://other-issuer.example.com",
					OIDCSubject:  "other-sub",
				},
			},
			claims: OIDCClaims{
				Subject:           "sub123",
				Email:             "john.doe@example.com",
				PreferredUsername: "johndoe",
			},
			expectedResult: "johndoe2",
		},
		{
			name: "conflict_with_builtin_user",
			existingUsers: []*auth.User{
				{
					ID:           "existing-id",
					Username:     "johndoe",
					AuthProvider: "builtin",
				},
			},
			claims: OIDCClaims{
				Subject:           "sub123",
				Email:             "john.doe@example.com",
				PreferredUsername: "johndoe",
			},
			expectedResult: "johndoe_sso",
		},
		{
			name:          "fallback_to_email",
			existingUsers: nil,
			claims: OIDCClaims{
				Subject:           "sub123",
				Email:             "john.doe@example.com",
				PreferredUsername: "",
			},
			expectedResult: "john_doe",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMockUserStore()
			for _, user := range tc.existingUsers {
				_ = store.Create(context.Background(), user)
			}

			svc, err := New(store, Config{
				Issuer:      "https://issuer.example.com",
				AutoSignup:  true,
				DefaultRole: auth.RoleViewer,
			})
			require.NoError(t, err)

			username := svc.generateUniqueUsername(context.Background(), tc.claims)
			assert.Equal(t, tc.expectedResult, username)
		})
	}
}

func TestGenerateUniqueUsername_FallbackToSubject(t *testing.T) {
	store := newMockUserStore()

	// Create users to exhaust all candidates including _sso variants
	// First, block the base and _sso versions
	for _, name := range []string{"john_doe", "john_doe_sso"} {
		_ = store.Create(context.Background(), &auth.User{
			ID:           name + "-id",
			Username:     name,
			AuthProvider: "builtin", // Use builtin to trigger _sso logic
		})
	}
	// Block all numbered OIDC variants (john_doe2 through john_doe99)
	for i := 2; i <= 99; i++ {
		name := fmt.Sprintf("john_doe%d", i)
		_ = store.Create(context.Background(), &auth.User{
			ID:           name + "-id",
			Username:     name,
			AuthProvider: "oidc",
		})
	}
	// Block all numbered _sso variants (john_doe_sso2 through john_doe_sso99)
	for i := 2; i <= 99; i++ {
		name := fmt.Sprintf("john_doe_sso%d", i)
		_ = store.Create(context.Background(), &auth.User{
			ID:           name + "-id",
			Username:     name,
			AuthProvider: "oidc",
		})
	}

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	claims := OIDCClaims{
		Subject:           "abcd1234efgh5678",
		Email:             "john.doe@example.com",
		PreferredUsername: "", // No preferred username
	}

	username := svc.generateUniqueUsername(context.Background(), claims)
	assert.Equal(t, "user_abcd1234", username) // First 8 chars of subject
}

func TestSanitizeUsername(t *testing.T) {
	svc := &Service{}

	tests := []struct {
		input    string
		expected string
	}{
		{"johndoe", "johndoe"},
		{"John.Doe", "john_doe"},
		{"john-doe", "john_doe"},
		{"john doe", "john_doe"},
		{"john@doe", "johndoe"},
		{"UPPERCASE", "uppercase"},
		{"user123", "user123"},
		{"___test___", "test"},
		{"john.doe.smith", "john_doe_smith"},
		{"日本語", ""}, // Non-ASCII removed
		{"test!@#$%", "test"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := svc.sanitizeUsername(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsEmailAllowed(t *testing.T) {
	tests := []struct {
		name           string
		allowedDomains []string
		whitelist      []string
		email          string
		expected       bool
	}{
		{
			name:     "no restrictions - all allowed",
			email:    "user@any.com",
			expected: true,
		},
		{
			name:           "domain allowed",
			allowedDomains: []string{"company.com"},
			email:          "user@company.com",
			expected:       true,
		},
		{
			name:           "domain not allowed",
			allowedDomains: []string{"company.com"},
			email:          "user@other.com",
			expected:       false,
		},
		{
			name:           "whitelist takes precedence",
			allowedDomains: []string{"company.com"},
			whitelist:      []string{"special@other.com"},
			email:          "special@other.com",
			expected:       true,
		},
		{
			name:           "case insensitive domain",
			allowedDomains: []string{"Company.COM"},
			email:          "user@company.com",
			expected:       true,
		},
		{
			name:      "case insensitive whitelist",
			whitelist: []string{"User@Company.COM"},
			email:     "user@company.com",
			expected:  true,
		},
		{
			name:           "multiple domains",
			allowedDomains: []string{"company.com", "partner.org"},
			email:          "user@partner.org",
			expected:       true,
		},
		{
			name:      "whitelist only - email not in whitelist - denied",
			whitelist: []string{"allowed@example.com"},
			email:     "other@example.com",
			expected:  false,
		},
		{
			name:      "whitelist only - email in whitelist - allowed",
			whitelist: []string{"allowed@example.com"},
			email:     "allowed@example.com",
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{
				config: Config{
					AllowedDomains: tt.allowedDomains,
					Whitelist:      tt.whitelist,
				},
			}
			result := svc.isEmailAllowed(tt.email)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Tests for error paths and edge cases

func TestProcessLogin_OIDCIdentityLookupError(t *testing.T) {
	store := newMockUserStore()
	store.getByOIDCIdentityErr = errors.New("database connection failed")

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	claims := OIDCClaims{
		Subject:           "sub123",
		Email:             "user@example.com",
		PreferredUsername: "testuser",
	}

	user, isNew, err := svc.ProcessLogin(context.Background(), claims)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to lookup OIDC identity")
	assert.Nil(t, user)
	assert.False(t, isNew)
}

func TestProcessLogin_UserCreationError(t *testing.T) {
	store := newMockUserStore()
	store.createErr = errors.New("database write failed")

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	claims := OIDCClaims{
		Subject:           "sub123",
		Email:             "user@example.com",
		PreferredUsername: "testuser",
	}

	user, isNew, err := svc.ProcessLogin(context.Background(), claims)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create OIDC user")
	assert.Nil(t, user)
	assert.False(t, isNew)
}

func TestEmailLocalPart(t *testing.T) {
	svc := &Service{}

	tests := []struct {
		email    string
		expected string
	}{
		{"user@example.com", "user"},
		{"john.doe@company.org", "john.doe"},
		{"invalid-email", ""},
		{"multiple@at@signs.com", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			result := svc.emailLocalPart(tt.email)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateUniqueUsername_SanitizedToEmpty(t *testing.T) {
	store := newMockUserStore()
	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	// Both preferred_username and email local part sanitize to empty
	claims := OIDCClaims{
		Subject:           "abcd1234efgh",
		Email:             "!!!@example.com", // Sanitizes to empty
		PreferredUsername: "日本語",             // Non-ASCII, sanitizes to empty
	}

	username := svc.generateUniqueUsername(context.Background(), claims)
	assert.Equal(t, "user_abcd1234", username) // Falls back to subject
}

func TestGenerateUniqueUsername_ShortSubject(t *testing.T) {
	store := newMockUserStore()
	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	// Subject is less than 8 characters
	claims := OIDCClaims{
		Subject:           "abc",
		Email:             "!!!@example.com", // Sanitizes to empty
		PreferredUsername: "",
	}

	username := svc.generateUniqueUsername(context.Background(), claims)
	assert.Equal(t, "user_abc", username) // Uses full short subject
}

func TestGenerateUniqueUsername_SSONumberedFallback(t *testing.T) {
	store := newMockUserStore()

	// Block base username with builtin user, and also block _sso variant
	_ = store.Create(context.Background(), &auth.User{
		ID:           "builtin-id",
		Username:     "johndoe",
		AuthProvider: "builtin",
	})
	_ = store.Create(context.Background(), &auth.User{
		ID:           "sso-id",
		Username:     "johndoe_sso",
		AuthProvider: "oidc",
	})

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	claims := OIDCClaims{
		Subject:           "sub123",
		Email:             "john.doe@example.com",
		PreferredUsername: "johndoe",
	}

	username := svc.generateUniqueUsername(context.Background(), claims)
	assert.Equal(t, "johndoe_sso2", username) // Falls back to numbered _sso
}

func TestProcessLogin_NewUserWithWorkspaceMapping(t *testing.T) {
	store := newMockUserStore()
	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleManager,
		RoleMapping: RoleMapperConfig{
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"payments-team": {
					{Workspace: "payments", Role: "developer"},
				},
			},
			DefaultRole: auth.RoleManager,
		},
	})
	require.NoError(t, err)

	user, isNew, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:           "new-user",
		Email:             "new@example.com",
		PreferredUsername: "newuser",
		RawClaims:         map[string]any{"groups": []any{"payments-team"}},
	})
	require.NoError(t, err)
	assert.True(t, isNew)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
		{Workspace: "payments", Role: auth.RoleDeveloper},
	}}, user.WorkspaceAccess)
}

func TestProcessLogin_DefaultNoneWithoutMappings(t *testing.T) {
	store := newMockUserStore()
	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleManager,
		RoleMapping: RoleMapperConfig{
			DefaultRole:            auth.RoleManager,
			DefaultWorkspaceAccess: "none",
		},
	})
	require.NoError(t, err)

	user, isNew, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:           "unmapped-user",
		Email:             "unmapped@example.com",
		PreferredUsername: "unmapped",
	})
	require.NoError(t, err)
	assert.True(t, isNew)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, user.WorkspaceAccess)
}

func TestProcessLogin_SyncsAuthorizationOnce(t *testing.T) {
	store := newMockUserStore()
	existing := &auth.User{
		ID:              "existing-id",
		Username:        "existing",
		Role:            auth.RoleManager,
		WorkspaceAccess: auth.AllWorkspaceAccess(),
		AuthProvider:    "oidc",
		OIDCIssuer:      "https://issuer.example.com",
		OIDCSubject:     "subject",
	}
	require.NoError(t, store.Create(context.Background(), existing))

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {
					{Workspace: "payments", Role: "operator"},
				},
			},
			DefaultRole: auth.RoleViewer,
		},
	})
	require.NoError(t, err)
	claims := OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"team"}},
	}

	user, isNew, err := svc.ProcessLogin(context.Background(), claims)
	require.NoError(t, err)
	assert.False(t, isNew)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
		{Workspace: "payments", Role: auth.RoleOperator},
	}}, user.WorkspaceAccess)
	assert.Equal(t, 1, store.updateCalls)

	_, _, err = svc.ProcessLogin(context.Background(), claims)
	require.NoError(t, err)
	assert.Equal(t, 1, store.updateCalls)
}

func TestProcessLogin_SyncCanonicalComparisonAvoidsWrite(t *testing.T) {
	store := newMockUserStore()
	existing := &auth.User{
		ID:       "existing-id",
		Username: "existing",
		Role:     auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "payments", Role: auth.RoleOperator},
			{Workspace: "infra", Role: auth.RoleDeveloper},
		}},
		AuthProvider: "oidc",
		OIDCIssuer:   "https://issuer.example.com",
		OIDCSubject:  "subject",
	}
	require.NoError(t, store.Create(context.Background(), existing))

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {
					{Workspace: "infra", Role: "developer"},
					{Workspace: "payments", Role: "operator"},
				},
			},
			DefaultRole: auth.RoleViewer,
		},
	})
	require.NoError(t, err)

	_, _, err = svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"team"}},
	})
	require.NoError(t, err)
	assert.Zero(t, store.updateCalls)
}

func TestProcessLogin_SyncRevokesAllWorkspaceGrants(t *testing.T) {
	store := newMockUserStore()
	existing := &auth.User{
		ID:       "existing-id",
		Username: "existing",
		Role:     auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "payments", Role: auth.RoleDeveloper},
		}},
		AuthProvider: "oidc",
		OIDCIssuer:   "https://issuer.example.com",
		OIDCSubject:  "subject",
	}
	require.NoError(t, store.Create(context.Background(), existing))

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleManager,
		RoleMapping: RoleMapperConfig{
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "payments", Role: "developer"}},
			},
			DefaultWorkspaceAccess: "none",
			DefaultRole:            auth.RoleManager,
		},
	})
	require.NoError(t, err)

	user, _, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"revoked"}},
	})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, user.WorkspaceAccess)
	assert.Equal(t, 1, store.updateCalls)
}

func TestProcessLogin_GlobalMappingReplacesScopedAccess(t *testing.T) {
	store := newMockUserStore()
	existing := &auth.User{
		ID:       "existing-id",
		Username: "existing",
		Role:     auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "payments", Role: auth.RoleDeveloper},
		}},
		AuthProvider: "oidc",
		OIDCIssuer:   "https://issuer.example.com",
		OIDCSubject:  "subject",
	}
	require.NoError(t, store.Create(context.Background(), existing))

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			GroupMappings: map[string]string{"staff": "developer"},
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "payments", Role: "operator"}},
			},
			DefaultRole: auth.RoleViewer,
		},
	})
	require.NoError(t, err)

	user, _, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"staff", "team"}},
	})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleDeveloper, user.Role)
	assert.True(t, user.WorkspaceAccess.All)
	assert.Empty(t, user.WorkspaceAccess.Grants)
	assert.Equal(t, 1, store.updateCalls)
}

func TestProcessLogin_RoleOnlySyncPreservesWorkspaceAccess(t *testing.T) {
	store := newMockUserStore()
	existingAccess := &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
		{Workspace: "payments", Role: auth.RoleOperator},
	}}
	existing := &auth.User{
		ID:              "existing-id",
		Username:        "existing",
		Role:            auth.RoleViewer,
		WorkspaceAccess: existingAccess,
		AuthProvider:    "oidc",
		OIDCIssuer:      "https://issuer.example.com",
		OIDCSubject:     "subject",
	}
	require.NoError(t, store.Create(context.Background(), existing))

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			GroupMappings: map[string]string{"staff": "operator"},
			DefaultRole:   auth.RoleViewer,
		},
	})
	require.NoError(t, err)

	user, _, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"staff"}},
	})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleOperator, user.Role)
	assert.Same(t, existingAccess, user.WorkspaceAccess)
	assert.Equal(t, existingAccess, store.users[existing.ID].WorkspaceAccess)
	assert.Equal(t, 1, store.updateCalls)
}

func TestProcessLogin_SkipSyncFreezesRoleAndAccess(t *testing.T) {
	store := newMockUserStore()
	existing := &auth.User{
		ID:              "existing-id",
		Username:        "existing",
		Role:            auth.RoleManager,
		WorkspaceAccess: auth.AllWorkspaceAccess(),
		AuthProvider:    "oidc",
		OIDCIssuer:      "https://issuer.example.com",
		OIDCSubject:     "subject",
	}
	require.NoError(t, store.Create(context.Background(), existing))

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "payments", Role: "operator"}},
			},
			SkipOrgRoleSync: true,
			DefaultRole:     auth.RoleViewer,
		},
	})
	require.NoError(t, err)

	user, _, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"team"}},
	})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleManager, user.Role)
	assert.Equal(t, auth.AllWorkspaceAccess(), user.WorkspaceAccess)
	assert.Zero(t, store.updateCalls)
}

func TestProcessLogin_StrictMissRejectsExistingUser(t *testing.T) {
	store := newMockUserStore()
	existing := &auth.User{
		ID:              "existing-id",
		Username:        "existing",
		Role:            auth.RoleManager,
		WorkspaceAccess: auth.AllWorkspaceAccess(),
		AuthProvider:    "oidc",
		OIDCIssuer:      "https://issuer.example.com",
		OIDCSubject:     "subject",
	}
	require.NoError(t, store.Create(context.Background(), existing))

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "payments", Role: "operator"}},
			},
			RoleAttributeStrict: true,
			DefaultRole:         auth.RoleViewer,
		},
	})
	require.NoError(t, err)

	user, isNew, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"other"}},
	})
	assert.ErrorIs(t, err, ErrNoRoleFound)
	assert.Nil(t, user)
	assert.False(t, isNew)
	assert.Zero(t, store.updateCalls)
	assert.Equal(t, auth.RoleManager, existing.Role)
	assert.Equal(t, auth.AllWorkspaceAccess(), existing.WorkspaceAccess)
}

func TestProcessLogin_UpdateFailureReturnsStoredAuthorization(t *testing.T) {
	store := newMockUserStore()
	existing := &auth.User{
		ID:              "existing-id",
		Username:        "existing",
		Role:            auth.RoleManager,
		WorkspaceAccess: auth.AllWorkspaceAccess(),
		AuthProvider:    "oidc",
		OIDCIssuer:      "https://issuer.example.com",
		OIDCSubject:     "subject",
	}
	require.NoError(t, store.Create(context.Background(), existing))
	store.updateErr = errors.New("storage unavailable")

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "payments", Role: "developer"}},
			},
			DefaultRole: auth.RoleViewer,
		},
	})
	require.NoError(t, err)

	user, isNew, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"team"}},
	})
	require.NoError(t, err)
	assert.False(t, isNew)
	assert.Equal(t, auth.RoleManager, user.Role)
	assert.Equal(t, auth.AllWorkspaceAccess(), user.WorkspaceAccess)
	assert.Equal(t, auth.RoleManager, store.users[existing.ID].Role)
	assert.Equal(t, auth.AllWorkspaceAccess(), store.users[existing.ID].WorkspaceAccess)
	assert.Equal(t, 1, store.updateCalls)
}

func TestProcessLogin_WorkspaceLookupNeverBlocksAndDeduplicatesMergedGrants(t *testing.T) {
	store := newMockUserStore()
	var lookedUp []string
	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"operators": {
					{Workspace: "payments", Role: "operator"},
					{Workspace: "infra", Role: "viewer"},
				},
				"developers": {
					{Workspace: "payments", Role: "developer"},
				},
			},
			DefaultRole: auth.RoleViewer,
		},
		WorkspaceExists: func(_ context.Context, workspaceName string) (bool, error) {
			lookedUp = append(lookedUp, workspaceName)
			if workspaceName == "infra" {
				return false, errors.New("lookup failed")
			}
			return false, nil
		},
	})
	require.NoError(t, err)

	user, isNew, err := svc.ProcessLogin(context.Background(), OIDCClaims{
		Subject:           "new-user",
		Email:             "new@example.com",
		PreferredUsername: "newuser",
		RawClaims:         map[string]any{"groups": []any{"operators", "developers"}},
	})
	require.NoError(t, err)
	assert.True(t, isNew)
	assert.Equal(t, []string{"infra", "payments"}, lookedUp)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
		{Workspace: "infra", Role: auth.RoleViewer},
		{Workspace: "payments", Role: auth.RoleDeveloper},
	}}, user.WorkspaceAccess)
}
