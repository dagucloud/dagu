// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package oidcprovision provides OIDC user provisioning functionality for builtin auth mode.
package oidcprovision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/google/uuid"
)

// Service errors with user-friendly messages.
var (
	// ErrEmailNotAllowed is returned when the user's email domain is not authorized.
	ErrEmailNotAllowed = errors.New("your email domain is not authorized")
	// ErrAutoSignupDisabled is returned when auto-signup is disabled and user doesn't exist.
	ErrAutoSignupDisabled = errors.New("automatic account creation is disabled, contact administrator")
	// ErrEmailRequired is returned when the email claim is not provided by the identity provider.
	ErrEmailRequired = errors.New("email claim is required but not provided by identity provider")
)

// ProvisioningPolicy contains the OIDC settings evaluated for a login.
type ProvisioningPolicy struct {
	AutoSignup     bool
	AllowedDomains []string
	Whitelist      []string
	RoleMapping    RoleMapperConfig
}

// PolicyLoader returns the current OIDC provisioning policy.
type PolicyLoader func(context.Context) (ProvisioningPolicy, error)

// Config holds the configuration for the OIDC provisioning service.
type Config struct {
	// Issuer is the OIDC provider issuer URL.
	Issuer string
	// AutoSignup enables automatic user creation on first login.
	AutoSignup bool
	// DefaultRole is the role assigned to new OIDC users.
	DefaultRole auth.Role
	// AllowedDomains is a list of email domains allowed to sign up.
	// If empty, all domains are allowed (unless Whitelist is set).
	AllowedDomains []string
	// Whitelist is a list of specific email addresses always allowed.
	// Takes precedence over AllowedDomains.
	Whitelist []string
	// RoleMapping holds the role mapping configuration.
	RoleMapping RoleMapperConfig
	// WorkspaceExists checks whether a configured workspace currently exists.
	// Missing workspaces are reported but do not prevent authentication.
	WorkspaceExists func(context.Context, string) (bool, error)
	// LoadPolicy resolves the provisioning policy used for each login.
	// When unset, the static policy in Config is used.
	// A failed load leaves the latest valid policy active.
	LoadPolicy PolicyLoader
}

// OIDCClaims contains the claims extracted from an OIDC ID token.
type OIDCClaims struct {
	// Subject is the unique identifier for the user from the OIDC provider.
	Subject string `json:"sub"`
	// Email is the user's email address.
	Email string `json:"email"`
	// PreferredUsername is the user's preferred username from the OIDC provider.
	PreferredUsername string `json:"preferred_username"`
	// Name is the user's display name.
	Name string `json:"name"`
	// RawClaims contains all claims from the ID token for role mapping.
	RawClaims map[string]any `json:"-"`
}

// Service provides OIDC user provisioning functionality.
type Service struct {
	userStore auth.AuthorizationSyncUserStore
	config    Config
	policy    atomic.Pointer[policySnapshot]
	logger    *slog.Logger
}

type policySnapshot struct {
	ProvisioningPolicy
	roleMapper *RoleMapper
}

// New creates a new OIDC provisioning service.
func New(userStore auth.UserStore, config Config) (*Service, error) {
	authorizationStore, ok := userStore.(auth.AuthorizationSyncUserStore)
	if !ok {
		return nil, errors.New("OIDC provisioner: user store does not support atomic authorization sync")
	}
	if config.RoleMapping.DefaultRole == auth.RoleNone {
		config.RoleMapping.DefaultRole = config.DefaultRole
	}
	staticPolicy := ProvisioningPolicy{
		AutoSignup:     config.AutoSignup,
		AllowedDomains: config.AllowedDomains,
		Whitelist:      config.Whitelist,
		RoleMapping:    config.RoleMapping,
	}
	policy, err := newPolicySnapshot(staticPolicy)
	if err != nil {
		return nil, fmt.Errorf("failed to create role mapper: %w", err)
	}

	service := &Service{
		userStore: authorizationStore,
		config:    config,
		logger:    slog.Default().With(slog.String("service", "oidcprovision")),
	}
	service.policy.Store(policy)
	return service, nil
}

// ProcessLogin handles OIDC authentication with auto-provisioning.
// Returns the user, whether it's a new user, and any error.
func (s *Service) ProcessLogin(ctx context.Context, claims OIDCClaims) (*auth.User, bool, error) {
	// 0. Validate email claim exists (required for access control)
	if claims.Email == "" {
		return nil, false, ErrEmailRequired
	}

	policy := s.loadPolicy(ctx)

	// 1. Check access control (whitelist + allowedDomains)
	if !isEmailAllowed(policy.ProvisioningPolicy, claims.Email) {
		s.logger.Warn("OIDC login rejected: email not allowed",
			slog.String("email_domain", stringutil.ExtractEmailDomain(claims.Email)),
			slog.String("subject", claims.Subject))
		return nil, false, ErrEmailNotAllowed
	}

	// 2. Look up existing user by OIDC identity
	user, err := s.userStore.GetByOIDCIdentity(ctx, s.config.Issuer, claims.Subject)
	if err == nil {
		// User found - check if disabled
		if user.IsDisabled {
			s.logger.Warn("OIDC login rejected: user disabled",
				slog.String("user_id", user.ID),
				slog.String("username", user.Username))
			return nil, false, authservice.ErrUserDisabled
		}

		// Synchronize mapped authorization, or enforce strict matching when synchronization is disabled.
		if policy.RoleMapping.SkipOrgRoleSync {
			if err := validateStrictMapping(policy, claims); err != nil {
				s.logger.Warn("OIDC login rejected: authorization mapping failed",
					slog.String("user_id", user.ID),
					slog.String("error", err.Error()))
				return nil, false, err
			}
		} else {
			if err := s.syncUserAccess(ctx, policy, user, claims); err != nil {
				if policy.roleMapper.WorkspaceAccessPolicyActive() ||
					errors.Is(err, ErrNoRoleFound) ||
					errors.Is(err, auth.ErrUserDisabled) {
					s.logger.Warn("OIDC login rejected: authorization mapping failed",
						slog.String("user_id", user.ID),
						slog.String("error", err.Error()))
					return nil, false, err
				}
				s.logger.Warn("failed to sync OIDC user authorization",
					slog.String("user_id", user.ID),
					slog.String("error", err.Error()))
			}
		}

		s.logger.Debug("OIDC login: existing user",
			slog.String("user_id", user.ID),
			slog.String("username", user.Username))
		return user, false, nil // Existing user
	}

	// 3. User not found - check if it's a not found error
	if !errors.Is(err, auth.ErrOIDCIdentityNotFound) {
		return nil, false, fmt.Errorf("failed to lookup OIDC identity: %w", err)
	}

	// 4. Check if auto-signup is enabled
	if !policy.AutoSignup {
		s.logger.Info("OIDC login rejected: auto-signup disabled",
			slog.String("email_domain", stringutil.ExtractEmailDomain(claims.Email)),
			slog.String("subject", claims.Subject))
		return nil, false, ErrAutoSignupDisabled
	}

	// 5. Determine authorization for the new user.
	role, workspaceAccess, err := s.determineAccess(ctx, policy, claims)
	if err != nil {
		s.logger.Warn("OIDC login rejected: authorization mapping failed",
			slog.String("email_domain", stringutil.ExtractEmailDomain(claims.Email)),
			slog.String("error", err.Error()))
		return nil, false, err
	}

	// 6. Generate unique username and create user with retry for race conditions
	const maxRetries = 3
	var username string
	now := time.Now().UTC()

	for attempt := range maxRetries {
		username = s.generateUniqueUsername(ctx, claims)

		user = &auth.User{
			ID:              uuid.New().String(),
			Username:        username,
			Role:            role,
			WorkspaceAccess: auth.CloneWorkspaceAccess(workspaceAccess),
			AuthProvider:    "oidc",
			OIDCIssuer:      s.config.Issuer,
			OIDCSubject:     claims.Subject,
			CreatedAt:       now,
			UpdatedAt:       now,
		}

		if err := s.userStore.Create(ctx, user); err != nil {
			// Handle race condition: another request created a user with same username
			if errors.Is(err, auth.ErrUserAlreadyExists) && attempt < maxRetries-1 {
				s.logger.Debug("username collision during OIDC signup, retrying",
					slog.String("username", username),
					slog.Int("attempt", attempt+1))
				continue
			}
			return nil, false, fmt.Errorf("failed to create OIDC user: %w", err)
		}
		break
	}

	// Log audit event
	s.logger.Info("OIDC user created",
		slog.String("user_id", user.ID),
		slog.String("username", username),
		slog.String("email_domain", stringutil.ExtractEmailDomain(claims.Email)),
		slog.String("role", string(user.Role)),
		slog.Any("workspace_access", canonicalWorkspaceAccess(user.WorkspaceAccess)))

	return user, true, nil // New user created
}

func (s *Service) loadPolicy(ctx context.Context) *policySnapshot {
	if s.config.LoadPolicy == nil {
		return s.policy.Load()
	}

	loaded, err := s.config.LoadPolicy(ctx)
	if err != nil {
		s.logger.Warn("OIDC authorization policy reload rejected",
			slog.String("error", err.Error()))
		return s.policy.Load()
	}
	policy, err := newPolicySnapshot(loaded)
	if err != nil {
		s.logger.Warn("OIDC authorization policy reload rejected",
			slog.String("error", err.Error()))
		return s.policy.Load()
	}
	s.policy.Store(policy)
	return policy
}

// CurrentPolicy returns the latest successfully loaded provisioning policy.
func (s *Service) CurrentPolicy() ProvisioningPolicy {
	return s.policy.Load().ProvisioningPolicy
}

func newPolicySnapshot(policy ProvisioningPolicy) (*policySnapshot, error) {
	roleMapper, err := NewRoleMapper(policy.RoleMapping)
	if err != nil {
		return nil, err
	}
	return &policySnapshot{
		ProvisioningPolicy: policy,
		roleMapper:         roleMapper,
	}, nil
}

func validateStrictMapping(policy *policySnapshot, claims OIDCClaims) error {
	if !policy.RoleMapping.RoleAttributeStrict || !policy.roleMapper.IsConfigured() {
		return nil
	}
	_, _, err := policy.roleMapper.MapAccess(claims.RawClaims)
	return err
}

// determineAccess determines authorization for a user based on OIDC claims.
func (s *Service) determineAccess(
	ctx context.Context,
	policy *policySnapshot,
	claims OIDCClaims,
) (auth.Role, *auth.WorkspaceAccess, error) {
	if policy.roleMapper.IsConfigured() {
		role, workspaceAccess, err := policy.roleMapper.MapAccess(claims.RawClaims)
		if err != nil {
			return "", nil, err
		}
		s.warnForDormantGrants(ctx, workspaceAccess)
		return role, workspaceAccess, nil
	}

	return policy.RoleMapping.DefaultRole, auth.AllWorkspaceAccess(), nil
}

// syncUserAccess updates mapped authorization without exposing unpersisted values.
func (s *Service) syncUserAccess(
	ctx context.Context,
	policy *policySnapshot,
	user *auth.User,
	claims OIDCClaims,
) error {
	if !policy.roleMapper.IsConfigured() {
		return nil
	}

	accessPolicyActive := policy.roleMapper.WorkspaceAccessPolicyActive()
	var (
		newRole   auth.Role
		newAccess *auth.WorkspaceAccess
		err       error
	)
	if accessPolicyActive {
		newRole, newAccess, err = s.determineAccess(ctx, policy, claims)
	} else {
		newRole, err = policy.roleMapper.MapRole(claims.RawClaims)
	}
	if err != nil {
		return err
	}

	workspaceAccess := newAccess
	if !accessPolicyActive {
		workspaceAccess = nil
	}
	syncResult, err := s.userStore.SyncAuthorization(ctx, user.ID, newRole, workspaceAccess)
	if err != nil {
		return fmt.Errorf("failed to update OIDC user authorization: %w", err)
	}
	updated := syncResult.User
	if updated == nil {
		return errors.New("failed to update OIDC user authorization: store returned no user")
	}

	user.Role = updated.Role
	if accessPolicyActive {
		user.WorkspaceAccess = auth.CloneWorkspaceAccess(updated.WorkspaceAccess)
	}
	user.UpdatedAt = updated.UpdatedAt
	if !syncResult.Changed {
		return nil
	}

	if accessPolicyActive {
		s.logger.Info("OIDC user authorization updated",
			slog.String("user_id", user.ID),
			slog.String("username", user.Username),
			slog.String("old_role", string(syncResult.PreviousRole)),
			slog.String("new_role", string(newRole)),
			slog.Any("old_workspace_access", canonicalWorkspaceAccess(syncResult.PreviousWorkspaceAccess)),
			slog.Any("new_workspace_access", canonicalWorkspaceAccess(updated.WorkspaceAccess)))
	} else {
		s.logger.Info("OIDC user role updated",
			slog.String("user_id", user.ID),
			slog.String("username", user.Username),
			slog.String("old_role", string(syncResult.PreviousRole)),
			slog.String("new_role", string(newRole)))
	}

	return nil
}

func (s *Service) warnForDormantGrants(ctx context.Context, workspaceAccess *auth.WorkspaceAccess) {
	if s.config.WorkspaceExists == nil {
		return
	}

	normalized := canonicalWorkspaceAccess(workspaceAccess)
	if normalized.All {
		return
	}
	for _, grant := range normalized.Grants {
		exists, err := s.config.WorkspaceExists(ctx, grant.Workspace)
		if err != nil {
			s.logger.Warn("failed to check OIDC workspace grant",
				slog.String("workspace", grant.Workspace),
				slog.String("error", err.Error()))
			continue
		}
		if !exists {
			s.logger.Warn("OIDC workspace grant references a nonexistent workspace",
				slog.String("workspace", grant.Workspace))
		}
	}
}

func canonicalWorkspaceAccess(workspaceAccess *auth.WorkspaceAccess) auth.WorkspaceAccess {
	normalized := auth.NormalizeWorkspaceAccess(workspaceAccess)
	slices.SortFunc(normalized.Grants, func(left, right auth.WorkspaceGrant) int {
		if result := strings.Compare(left.Workspace, right.Workspace); result != 0 {
			return result
		}
		return strings.Compare(string(left.Role), string(right.Role))
	})
	return normalized
}

// isEmailAllowed checks if an email is allowed based on whitelist and allowedDomains.
// Logic:
//   - If whitelist is not empty and email is in whitelist: ALLOW
//   - If allowedDomains is not empty and email domain is in allowedDomains: ALLOW
//   - If either whitelist or allowedDomains is configured but email doesn't match: DENY
//   - If both whitelist and allowedDomains are empty: ALLOW (no restrictions)
func isEmailAllowed(policy ProvisioningPolicy, email string) bool {
	email = strings.ToLower(email)
	hasWhitelist := len(policy.Whitelist) > 0
	hasAllowedDomains := len(policy.AllowedDomains) > 0

	// Check whitelist first (takes precedence)
	if hasWhitelist {
		for _, allowed := range policy.Whitelist {
			if strings.EqualFold(email, allowed) {
				return true
			}
		}
	}

	// Check allowed domains
	if hasAllowedDomains {
		domain := stringutil.ExtractEmailDomain(email)
		for _, allowed := range policy.AllowedDomains {
			if strings.EqualFold(domain, allowed) {
				return true
			}
		}
	}

	// If any restriction is configured but email didn't match, deny
	if hasWhitelist || hasAllowedDomains {
		return false
	}

	// No restrictions configured
	return true
}

// generateUniqueUsername creates a username avoiding conflicts with existing users.
func (s *Service) generateUniqueUsername(ctx context.Context, claims OIDCClaims) string {
	candidates := []string{claims.PreferredUsername, s.emailLocalPart(claims.Email)}

	for _, base := range candidates {
		if base == "" {
			continue
		}

		// Sanitize the username (remove special characters, etc.)
		base = s.sanitizeUsername(base)
		if base == "" {
			continue
		}

		// Check if username exists
		existing, err := s.userStore.GetByUsername(ctx, base)
		if errors.Is(err, auth.ErrUserNotFound) {
			// Username available
			return base
		}
		if err != nil {
			// Other error (I/O, etc.) - skip this candidate, try next
			continue
		}

		// If exists but is an OIDC user, try suffix
		if existing.AuthProvider == "oidc" {
			for i := 2; i <= 99; i++ {
				candidate := fmt.Sprintf("%s%d", base, i)
				if _, err := s.userStore.GetByUsername(ctx, candidate); errors.Is(err, auth.ErrUserNotFound) {
					return candidate
				}
			}
		}

		// Conflict with builtin user - use suffix to differentiate
		ssoCandidate := fmt.Sprintf("%s_sso", base)
		if _, err := s.userStore.GetByUsername(ctx, ssoCandidate); errors.Is(err, auth.ErrUserNotFound) {
			return ssoCandidate
		}

		// Try with numbers
		for i := 2; i <= 99; i++ {
			candidate := fmt.Sprintf("%s_sso%d", base, i)
			if _, err := s.userStore.GetByUsername(ctx, candidate); errors.Is(err, auth.ErrUserNotFound) {
				return candidate
			}
		}
	}

	// Fallback: use first 8 chars of subject
	if len(claims.Subject) >= 8 {
		return fmt.Sprintf("user_%s", claims.Subject[:8])
	}
	return fmt.Sprintf("user_%s", claims.Subject)
}

// emailLocalPart extracts the local part (before @) from an email address.
func (s *Service) emailLocalPart(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

// sanitizeUsername removes or replaces characters that are not suitable for usernames.
func (s *Service) sanitizeUsername(username string) string {
	// Convert to lowercase
	username = strings.ToLower(username)

	// Replace common separators with underscores
	replacer := strings.NewReplacer(
		".", "_",
		"-", "_",
		" ", "_",
	)
	username = replacer.Replace(username)

	// Remove any characters that aren't alphanumeric or underscore
	var result strings.Builder
	for _, r := range username {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		}
	}

	// Trim leading/trailing underscores
	return strings.Trim(result.String(), "_")
}
