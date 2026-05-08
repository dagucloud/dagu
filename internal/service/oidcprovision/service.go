// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package oidcprovision provides OIDC user provisioning functionality for builtin auth mode.
package oidcprovision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	userStore  auth.UserStore
	config     Config
	roleMapper *RoleMapper
	logger     *slog.Logger
}

// New creates a new OIDC provisioning service.
func New(userStore auth.UserStore, config Config) (*Service, error) {
	roleMapper, err := NewRoleMapper(config.RoleMapping)
	if err != nil {
		return nil, fmt.Errorf("failed to create role mapper: %w", err)
	}

	return &Service{
		userStore:  userStore,
		config:     config,
		roleMapper: roleMapper,
		logger:     slog.Default().With(slog.String("service", "oidcprovision")),
	}, nil
}

// ProcessLogin handles OIDC authentication with auto-provisioning.
// Returns the user, whether it's a new user, and any error.
func (s *Service) ProcessLogin(ctx context.Context, claims OIDCClaims) (*auth.User, bool, error) {
	// 0. Validate email claim exists (required for access control)
	if claims.Email == "" {
		return nil, false, ErrEmailRequired
	}

	// 1. Check access control (whitelist + allowedDomains)
	if !s.isEmailAllowed(claims.Email) {
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

		// Sync roles on re-login (unless skipOrgRoleSync is true)
		if !s.config.RoleMapping.SkipOrgRoleSync {
			if err := s.syncUserRole(ctx, user, claims); err != nil {
				s.logger.Warn("failed to sync user role",
					slog.String("user_id", user.ID),
					slog.String("error", err.Error()))
				// Continue with login even if role sync fails (non-fatal)
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
	if !s.config.AutoSignup {
		s.logger.Info("OIDC login rejected: auto-signup disabled",
			slog.String("email_domain", stringutil.ExtractEmailDomain(claims.Email)),
			slog.String("subject", claims.Subject))
		return nil, false, ErrAutoSignupDisabled
	}

	// 5. Determine role for new user
	role, err := s.determineRole(claims)
	if err != nil {
		s.logger.Warn("OIDC login rejected: role mapping failed",
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
		id, err := uuid.NewV7()
		if err != nil {
			return nil, false, fmt.Errorf("failed to generate user ID: %w", err)
		}

		user = &auth.User{
			ID:              id.String(),
			Username:        username,
			Role:            role,
			WorkspaceAccess: auth.AllWorkspaceAccess(),
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
		slog.String("role", string(user.Role)))

	return user, true, nil // New user created
}

// determineRole determines the role for a user based on their OIDC claims.
func (s *Service) determineRole(claims OIDCClaims) (auth.Role, error) {
	// Use role mapper if configured
	if s.roleMapper.IsConfigured() {
		return s.roleMapper.MapRole(claims.RawClaims)
	}

	// Fall back to default role
	return s.config.DefaultRole, nil
}

// syncUserRole updates the user's role based on current OIDC claims.
func (s *Service) syncUserRole(ctx context.Context, user *auth.User, claims OIDCClaims) error {
	// Only sync if role mapper is configured
	if !s.roleMapper.IsConfigured() {
		return nil
	}

	newRole, err := s.roleMapper.MapRole(claims.RawClaims)
	if err != nil {
		// In strict mode, this is an error; otherwise, keep current role
		if errors.Is(err, ErrNoRoleFound) {
			return nil // Keep current role
		}
		return err
	}

	// Check if role changed
	if user.Role == newRole {
		return nil
	}

	oldRole := user.Role
	user.Role = newRole
	user.UpdatedAt = time.Now().UTC()

	if err := s.userStore.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user role: %w", err)
	}

	s.logger.Info("OIDC user role updated",
		slog.String("user_id", user.ID),
		slog.String("username", user.Username),
		slog.String("old_role", string(oldRole)),
		slog.String("new_role", string(newRole)))

	return nil
}

// isEmailAllowed checks if an email is allowed based on whitelist and allowedDomains.
// Logic:
//   - If whitelist is not empty and email is in whitelist: ALLOW
//   - If allowedDomains is not empty and email domain is in allowedDomains: ALLOW
//   - If either whitelist or allowedDomains is configured but email doesn't match: DENY
//   - If both whitelist and allowedDomains are empty: ALLOW (no restrictions)
func (s *Service) isEmailAllowed(email string) bool {
	email = strings.ToLower(email)
	hasWhitelist := len(s.config.Whitelist) > 0
	hasAllowedDomains := len(s.config.AllowedDomains) > 0

	// Check whitelist first (takes precedence)
	if hasWhitelist {
		for _, allowed := range s.config.Whitelist {
			if strings.EqualFold(email, allowed) {
				return true
			}
		}
	}

	// Check allowed domains
	if hasAllowedDomains {
		domain := stringutil.ExtractEmailDomain(email)
		for _, allowed := range s.config.AllowedDomains {
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
