// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package trustedproxyprovision provisions users through proxy authentication.
package trustedproxyprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/authmapping"
	"github.com/google/uuid"
)

const (
	maxUsernameLength = 64
	usernameHashBytes = 6
	maxUsernameTries  = 8
)

var (
	// ErrAutoSignupDisabled is returned when an unknown identity cannot create an account.
	ErrAutoSignupDisabled = errors.New("automatic account creation is disabled")
	// ErrAuthorizationMapping is returned when strict mapping finds no authorized group.
	ErrAuthorizationMapping = errors.New("no authorization mapping matched")
	// ErrInitialSetupRequired is returned when proxy login would create the first account.
	ErrInitialSetupRequired = errors.New("initial administrator setup is required")
	// ErrInvalidIdentity is returned when provisioning receives an empty identity.
	ErrInvalidIdentity = errors.New("proxy identity is required")
)

// Config defines proxy authentication provisioning behavior.
type Config struct {
	UsersDir        string
	Source          string
	AutoSignup      bool
	RoleMapping     authmapping.Config
	WorkspaceExists func(context.Context, string) (bool, error)
}

// Service provisions and synchronizes proxy users.
type Service struct {
	userStore       auth.AuthorizationSyncUserStore
	usersDir        string
	source          string
	autoSignup      bool
	mapper          *authmapping.Mapper
	workspaceExists func(context.Context, string) (bool, error)
	logger          *slog.Logger
}

// New creates a proxy authentication provisioning service.
func New(userStore auth.UserStore, config Config) (*Service, error) {
	if userStore == nil {
		return nil, errors.New("proxy authentication provisioner: user store is required")
	}
	authorizationStore, ok := userStore.(auth.AuthorizationSyncUserStore)
	if !ok {
		return nil, errors.New("proxy authentication provisioner: user store does not support atomic authorization sync")
	}
	if strings.TrimSpace(config.UsersDir) == "" {
		return nil, errors.New("proxy authentication provisioner: users directory is required")
	}
	if !config.RoleMapping.DefaultRole.Valid() {
		return nil, fmt.Errorf("proxy authentication provisioner: invalid default role %q", config.RoleMapping.DefaultRole)
	}
	mapper, err := authmapping.New(config.RoleMapping)
	if err != nil {
		return nil, fmt.Errorf("proxy authentication provisioner: compile authorization mapping: %w", err)
	}
	return &Service{
		userStore:       authorizationStore,
		usersDir:        config.UsersDir,
		source:          config.Source,
		autoSignup:      config.AutoSignup,
		mapper:          mapper,
		workspaceExists: config.WorkspaceExists,
		logger:          slog.Default().With(slog.String("service", "trustedproxyprovision")),
	}, nil
}

// ProcessLogin resolves a proxy identity and synchronizes its authorization.
func (s *Service) ProcessLogin(ctx context.Context, identity string, groups []string) (*auth.User, bool, error) {
	if identity == "" {
		return nil, false, ErrInvalidIdentity
	}

	user, err := s.userStore.GetByTrustedProxyIdentity(ctx, s.source, identity)
	if err == nil {
		return s.processExisting(ctx, user, groups)
	}
	if !errors.Is(err, auth.ErrTrustedProxyIdentityNotFound) {
		return nil, false, fmt.Errorf("lookup proxy identity: %w", err)
	}
	if !s.autoSignup {
		return nil, false, ErrAutoSignupDisabled
	}

	mapping, err := s.mapAccess(ctx, groups)
	if err != nil {
		return nil, false, err
	}

	lock := dirlock.New(s.usersDir, &dirlock.LockOptions{
		StaleThreshold: 30 * time.Second,
		RetryInterval:  50 * time.Millisecond,
	})
	if err := lock.Lock(ctx); err != nil {
		return nil, false, fmt.Errorf("acquire user provisioning lock: %w", err)
	}
	defer func() {
		if err := lock.Unlock(); err != nil {
			s.logger.Error("failed to release user provisioning lock", slog.String("error", err.Error()))
		}
	}()

	user, err = s.userStore.GetByTrustedProxyIdentity(ctx, s.source, identity)
	if err == nil {
		return s.processExisting(ctx, user, groups)
	}
	if !errors.Is(err, auth.ErrTrustedProxyIdentityNotFound) {
		return nil, false, fmt.Errorf("recheck proxy identity: %w", err)
	}

	count, err := s.userStore.Count(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("count users before proxy signup: %w", err)
	}
	if count == 0 {
		return nil, false, ErrInitialSetupRequired
	}

	return s.createUser(ctx, identity, groups, mapping)
}

func (s *Service) processExisting(ctx context.Context, user *auth.User, groups []string) (*auth.User, bool, error) {
	if user.IsDisabled {
		return nil, false, authservice.ErrUserDisabled
	}

	mapping, err := s.mapAccess(ctx, groups)
	if err != nil {
		return nil, false, err
	}
	if user.Role == mapping.Role && workspaceAccessEqual(user.WorkspaceAccess, mapping.WorkspaceAccess) {
		s.logger.Debug("proxy login resolved existing user",
			slog.String("user_id", user.ID),
			slog.Int("group_count", len(groups)),
			slog.Int("mapping_match_count", mapping.MatchCount))
		return user, false, nil
	}

	oldRole := user.Role
	oldAccess := canonicalWorkspaceAccess(user.WorkspaceAccess)
	updated, err := s.userStore.SyncAuthorization(ctx, user.ID, mapping.Role, mapping.WorkspaceAccess)
	if errors.Is(err, auth.ErrUserDisabled) {
		return nil, false, authservice.ErrUserDisabled
	}
	if err != nil {
		return nil, false, fmt.Errorf("update proxy authorization: %w", err)
	}
	if updated == nil {
		return nil, false, errors.New("update proxy authorization: store returned no user")
	}

	s.logger.Info("proxy user authorization updated",
		slog.String("user_id", updated.ID),
		slog.String("old_role", string(oldRole)),
		slog.String("new_role", string(updated.Role)),
		slog.Any("old_workspace_access", oldAccess),
		slog.Any("new_workspace_access", canonicalWorkspaceAccess(updated.WorkspaceAccess)),
		slog.Int("group_count", len(groups)),
		slog.Int("mapping_match_count", mapping.MatchCount))
	return updated, false, nil
}

func (s *Service) createUser(
	ctx context.Context,
	identity string,
	groups []string,
	mapping authmapping.Result,
) (*auth.User, bool, error) {
	for _, username := range usernameCandidates(s.source, identity) {
		now := time.Now().UTC()
		user := &auth.User{
			ID:                 uuid.New().String(),
			Username:           username,
			PasswordHash:       "",
			Role:               mapping.Role,
			WorkspaceAccess:    auth.CloneWorkspaceAccess(mapping.WorkspaceAccess),
			CreatedAt:          now,
			UpdatedAt:          now,
			AuthProvider:       auth.AuthProviderProxy,
			TrustedProxySource: s.source,
			TrustedProxyUser:   identity,
		}
		if err := s.userStore.Create(ctx, user); err == nil {
			s.logger.Info("proxy user created",
				slog.String("user_id", user.ID),
				slog.String("role", string(user.Role)),
				slog.Any("workspace_access", canonicalWorkspaceAccess(user.WorkspaceAccess)),
				slog.Int("group_count", len(groups)),
				slog.Int("mapping_match_count", mapping.MatchCount))
			return user, true, nil
		} else if !errors.Is(err, auth.ErrUserAlreadyExists) &&
			!errors.Is(err, auth.ErrTrustedProxyIdentityAlreadyExists) {
			return nil, false, fmt.Errorf("create proxy user: %w", err)
		}

		existing, err := s.userStore.GetByTrustedProxyIdentity(ctx, s.source, identity)
		if err == nil {
			return s.processExisting(ctx, existing, groups)
		}
		if !errors.Is(err, auth.ErrTrustedProxyIdentityNotFound) {
			return nil, false, fmt.Errorf("resolve concurrent proxy signup: %w", err)
		}
	}
	return nil, false, errors.New("create proxy user: username candidates exhausted")
}

func (s *Service) mapAccess(ctx context.Context, groups []string) (authmapping.Result, error) {
	result, err := s.mapper.Map(groups)
	if errors.Is(err, authmapping.ErrNoMatch) {
		return authmapping.Result{}, ErrAuthorizationMapping
	}
	if err != nil {
		return authmapping.Result{}, fmt.Errorf("map proxy authorization: %w", err)
	}
	s.warnForDormantGrants(ctx, result.WorkspaceAccess)
	return result, nil
}

func (s *Service) warnForDormantGrants(ctx context.Context, workspaceAccess *auth.WorkspaceAccess) {
	if s.workspaceExists == nil {
		return
	}
	access := canonicalWorkspaceAccess(workspaceAccess)
	if access.All {
		return
	}
	for _, grant := range access.Grants {
		exists, err := s.workspaceExists(ctx, grant.Workspace)
		if err != nil {
			s.logger.Warn("failed to check proxy workspace grant",
				slog.String("workspace", grant.Workspace),
				slog.String("error", err.Error()))
			continue
		}
		if !exists {
			s.logger.Warn("proxy workspace grant references a nonexistent workspace",
				slog.String("workspace", grant.Workspace))
		}
	}
}

func usernameCandidates(source, identity string) []string {
	base := sanitizeUsername(identity)
	candidates := make([]string, 0, maxUsernameTries)
	if base != "" {
		candidates = append(candidates, truncateASCII(base, maxUsernameLength))
	}
	for attempt := 0; len(candidates) < maxUsernameTries; attempt++ {
		seed := "trusted_proxy\x00" + identity + "\x00" + strconv.Itoa(attempt)
		if source != "" {
			seed = "trusted_proxy\x00" + source + "\x00" + identity + "\x00" + strconv.Itoa(attempt)
		}
		digest := sha256.Sum256([]byte(seed))
		suffix := hex.EncodeToString(digest[:usernameHashBytes])
		if base == "" {
			candidates = append(candidates, "user_"+suffix)
			continue
		}
		prefix := truncateASCII(base, maxUsernameLength-len(suffix)-1)
		candidates = append(candidates, prefix+"_"+suffix)
	}
	return candidates
}

func sanitizeUsername(identity string) string {
	var result strings.Builder
	separatorPending := false
	lastWasUnderscore := false
	for _, r := range identity {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			if separatorPending && result.Len() > 0 && !lastWasUnderscore {
				result.WriteByte('_')
			}
			separatorPending = false
			result.WriteRune(r)
			lastWasUnderscore = r == '_'
			continue
		}
		separatorPending = result.Len() > 0
	}
	return strings.Trim(result.String(), "_")
}

func truncateASCII(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	return strings.TrimRight(value[:maxLength], "_")
}

func workspaceAccessEqual(left, right *auth.WorkspaceAccess) bool {
	leftCanonical := canonicalWorkspaceAccess(left)
	rightCanonical := canonicalWorkspaceAccess(right)
	return leftCanonical.All == rightCanonical.All && slices.Equal(leftCanonical.Grants, rightCanonical.Grants)
}

func canonicalWorkspaceAccess(access *auth.WorkspaceAccess) auth.WorkspaceAccess {
	normalized := auth.NormalizeWorkspaceAccess(access)
	slices.SortFunc(normalized.Grants, func(left, right auth.WorkspaceGrant) int {
		if result := strings.Compare(left.Workspace, right.Workspace); result != 0 {
			return result
		}
		return strings.Compare(string(left.Role), string(right.Role))
	})
	return normalized
}
