// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/license"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/trustedproxyprovision"
)

// TrustedProxyProvisioner resolves proxy identities into Dagu users.
type TrustedProxyProvisioner interface {
	ProcessLogin(context.Context, string, []string) (*authmodel.User, bool, error)
}

// TrustedProxyTokenService creates Dagu sessions for provisioned users.
type TrustedProxyTokenService interface {
	GenerateToken(*authmodel.User) (*authservice.TokenResult, error)
}

// TrustedProxyLoginConfig configures the proxy authentication browser login endpoint.
type TrustedProxyLoginConfig struct {
	Enabled              bool
	UserHeader           string
	GroupsHeader         string
	GroupsRequired       bool
	Provision            TrustedProxyProvisioner
	AuthService          TrustedProxyTokenService
	LicenseChecker       license.Checker
	InitialSetupComplete func(context.Context) (bool, error)
	LoginBasePath        string
}

// TrustedProxyLoginHandler exchanges a proxy-provided identity for a Dagu session.
func TrustedProxyLoginHandler(cfg *TrustedProxyLoginConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setTrustedProxyResponseHeaders(w)
		if cfg == nil || !cfg.Enabled {
			writeTrustedProxyError(w, http.StatusNotFound, "not found")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeTrustedProxyError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			writeTrustedProxyError(w, http.StatusBadRequest, "invalid request")
			return
		}
		if !allowTrustedProxyAfterInitialSetup(w, r, cfg) {
			return
		}
		if cfg.LicenseChecker != nil && !cfg.LicenseChecker.IsFeatureEnabled(license.FeatureSSO) {
			logger.Warn(r.Context(), "Proxy login denied", slog.String("reason", "license_unavailable"))
			writeTrustedProxyError(w, http.StatusForbidden, "access denied")
			return
		}
		if cfg.Provision == nil || cfg.AuthService == nil {
			logger.Error(r.Context(), "Proxy login services are not configured")
			writeTrustedProxyError(w, http.StatusInternalServerError, "authentication failed")
			return
		}

		identity, err := parseTrustedProxyIdentity(r.Header, cfg.UserHeader, cfg.GroupsHeader, cfg.GroupsRequired)
		if err != nil {
			switch {
			case errors.Is(err, errTrustedProxyIdentityUnavailable):
				logger.Warn(r.Context(), "Proxy login denied", slog.String("reason", "identity_unavailable"))
				writeTrustedProxyError(w, http.StatusUnauthorized, "proxy identity unavailable")
			default:
				logger.Warn(r.Context(), "Proxy login denied", slog.String("reason", "invalid_identity"))
				writeTrustedProxyError(w, http.StatusBadRequest, "invalid proxy identity")
			}
			return
		}

		user, isNew, err := cfg.Provision.ProcessLogin(r.Context(), identity.user, identity.groups)
		if err != nil {
			switch {
			case errors.Is(err, trustedproxyprovision.ErrInitialSetupRequired):
				redirectTrustedProxySetup(w, r, cfg.LoginBasePath)
			case errors.Is(err, trustedproxyprovision.ErrInvalidIdentity):
				logger.Warn(r.Context(), "Proxy login denied", slog.String("reason", "invalid_identity"))
				writeTrustedProxyError(w, http.StatusBadRequest, "invalid proxy identity")
			case errors.Is(err, trustedproxyprovision.ErrAutoSignupDisabled):
				logger.Warn(r.Context(), "Proxy login denied", slog.String("reason", "auto_signup_disabled"))
				writeTrustedProxyError(w, http.StatusForbidden, "access denied")
			case errors.Is(err, trustedproxyprovision.ErrAuthorizationMapping):
				logger.Warn(r.Context(), "Proxy login denied", slog.String("reason", "authorization_mapping"))
				writeTrustedProxyError(w, http.StatusForbidden, "access denied")
			case errors.Is(err, authservice.ErrUserDisabled):
				logger.Warn(r.Context(), "Proxy login denied", slog.String("reason", "user_disabled"))
				writeTrustedProxyError(w, http.StatusForbidden, "access denied")
			default:
				logger.Error(r.Context(), "Proxy authentication provisioning failed", tag.Error(err))
				writeTrustedProxyError(w, http.StatusInternalServerError, "authentication failed")
			}
			return
		}
		if user == nil {
			logger.Error(r.Context(), "Proxy authentication provisioning returned no user")
			writeTrustedProxyError(w, http.StatusInternalServerError, "authentication failed")
			return
		}

		tokenResult, err := cfg.AuthService.GenerateToken(user)
		if err != nil || tokenResult == nil || tokenResult.Token == "" {
			logger.Error(r.Context(), "Proxy session creation failed", tag.Error(err))
			writeTrustedProxyError(w, http.StatusInternalServerError, "authentication failed")
			return
		}

		access := authmodel.NormalizeWorkspaceAccess(user.WorkspaceAccess)
		logger.Info(r.Context(), "Proxy login accepted",
			slog.String("user_id", user.ID),
			slog.Bool("new_user", isNew),
			slog.Int("group_count", len(identity.groups)),
			slog.String("role", string(user.Role)),
			slog.Bool("all_workspaces", access.All),
			slog.Int("workspace_grant_count", len(access.Grants)))

		redirectURL := strings.TrimSuffix(cfg.LoginBasePath, "/") + "/login"
		if isNew {
			redirectURL += "?welcome=true"
		}
		redirectURL += "#token=" + url.QueryEscape(tokenResult.Token)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Redirect(w, r, redirectURL, http.StatusFound)
	})
}

func allowTrustedProxyAfterInitialSetup(w http.ResponseWriter, r *http.Request, cfg *TrustedProxyLoginConfig) bool {
	if cfg.InitialSetupComplete == nil {
		logger.Error(r.Context(), "Initial setup check is not configured for proxy login")
		writeTrustedProxyError(w, http.StatusInternalServerError, "authentication failed")
		return false
	}
	complete, err := cfg.InitialSetupComplete(r.Context())
	if err != nil {
		logger.Error(r.Context(), "Failed to check initial setup before proxy login", tag.Error(err))
		writeTrustedProxyError(w, http.StatusInternalServerError, "authentication failed")
		return false
	}
	if !complete {
		redirectTrustedProxySetup(w, r, cfg.LoginBasePath)
		return false
	}
	return true
}

func redirectTrustedProxySetup(w http.ResponseWriter, r *http.Request, basePath string) {
	http.Redirect(w, r, strings.TrimSuffix(basePath, "/")+"/setup", http.StatusFound)
}

func setTrustedProxyResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func writeTrustedProxyError(w http.ResponseWriter, status int, message string) {
	http.Error(w, message, status)
}
