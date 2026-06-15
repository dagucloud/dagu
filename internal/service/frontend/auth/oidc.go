// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	oidc "github.com/coreos/go-oidc"
	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/license"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/oidcprovision"
	"golang.org/x/oauth2"
)

// Tunable constants for OIDC auth behaviour.
const (
	oidcProviderInitTimeout     = 10 * time.Second
	oidcProviderMaxRetries      = 10                     // max retries for OIDC provider init (network issues)
	oidcProviderInitialInterval = 500 * time.Millisecond // initial backoff interval
	oidcProviderMaxInterval     = 5 * time.Second        // maximum backoff interval
	stateCookieExpiry           = 120                    // seconds for transient state/nonce/originalURL cookies
)

// Cookie names centralised to avoid copy-paste strings.
const (
	cookieState       = "state"
	cookieNonce       = "nonce"
	cookieOriginalURL = "originalURL"
)

var oidcProviderFactory = func(ctx context.Context, issuer string) (*oidc.Provider, error) {
	return oidc.NewProvider(ctx, issuer)
}

// oidcProviderParams holds the common parameters for OIDC provider initialization.
type oidcProviderParams struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	ClientURL    string
	Scopes       []string
}

// oidcProviderResult holds the initialized OIDC components.
type oidcProviderResult struct {
	Provider     *oidc.Provider
	Verifier     *oidc.IDTokenVerifier
	OAuth2Config *oauth2.Config
}

// initOIDCProviderCore initializes the common OIDC provider components.
// The passed context is used for the OIDC provider initialization and retry loop,
// allowing the initialization to be cancelled (e.g., on SIGINT/SIGTERM).
func initOIDCProviderCore(ctx context.Context, params oidcProviderParams) (*oidcProviderResult, error) {
	// Basic input validation
	if params.Issuer == "" {
		return nil, errors.New("failed to init OIDC provider: issuer is empty")
	}
	if params.ClientID == "" {
		return nil, errors.New("failed to init OIDC provider: client id is empty")
	}
	if params.ClientURL == "" {
		return nil, errors.New("failed to init OIDC provider: client url is empty")
	}

	// Create HTTP client with explicit transport settings to avoid race conditions
	// with gopsutil resource monitoring during startup (both use net/http dial).
	// Force IPv4 to avoid "bad file descriptor" errors on macOS with IPv6.
	dialer := &net.Dialer{
		Timeout:   oidcProviderInitTimeout,
		KeepAlive: 30 * time.Second,
	}
	httpClient := &http.Client{
		Timeout: oidcProviderInitTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp4", addr)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   oidcProviderInitTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	oidcCtx := oidc.ClientContext(ctx, httpClient)

	// Create retry policy with exponential backoff for transient network errors
	policy := backoff.NewExponentialBackoffPolicy(oidcProviderInitialInterval)
	policy.MaxInterval = oidcProviderMaxInterval
	policy.MaxRetries = oidcProviderMaxRetries

	var provider *oidc.Provider
	err := backoff.Retry(oidcCtx, func(retryCtx context.Context) error {
		var providerErr error
		provider, providerErr = oidcProviderFactory(retryCtx, params.Issuer)
		if providerErr != nil {
			// Check for permanent errors that shouldn't be retried
			// (e.g., invalid URL format, unsupported scheme)
			errStr := providerErr.Error()
			if strings.Contains(errStr, "unsupported protocol scheme") ||
				strings.Contains(errStr, "invalid URI") ||
				strings.Contains(errStr, "invalid URL") {
				slog.Error("OIDC provider init failed with permanent error",
					tag.Error(providerErr),
					slog.String("issuer", params.Issuer))
				return backoff.PermanentError(providerErr)
			}

			slog.Warn("OIDC provider init failed, will retry",
				tag.Error(providerErr),
				slog.String("issuer", params.Issuer))
			return providerErr
		}
		return nil
	}, policy, nil)

	if err != nil {
		return nil, fmt.Errorf("failed to init OIDC provider: %w", err)
	}
	if provider == nil {
		return nil, errors.New("failed to init OIDC provider: provider is nil")
	}

	oidcConfig := &oidc.Config{
		ClientID: params.ClientID,
	}
	verifier := provider.Verifier(oidcConfig)
	endpoint := provider.Endpoint()

	oauth2Config := &oauth2.Config{
		ClientID:     params.ClientID,
		ClientSecret: params.ClientSecret,
		Endpoint:     endpoint,
		RedirectURL:  fmt.Sprintf("%s/oidc-callback", strings.TrimSuffix(params.ClientURL, "/")),
		Scopes:       params.Scopes,
	}

	return &oidcProviderResult{
		Provider:     provider,
		Verifier:     verifier,
		OAuth2Config: oauth2Config,
	}, nil
}

func setCookie(w http.ResponseWriter, _ *http.Request, name, value string, expire int) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		MaxAge:   expire,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
		HttpOnly: true,
	}
	http.SetCookie(w, c)
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	setCookie(w, r, name, "", -1)
}

// clearOIDCStateCookies removes all OIDC-related state cookies.
func clearOIDCStateCookies(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, r, cookieState)
	clearCookie(w, r, cookieNonce)
	clearCookie(w, r, cookieOriginalURL)
}

// BuiltinOIDCConfig holds configuration for OIDC under builtin auth mode.
type BuiltinOIDCConfig struct {
	Provider       *oidc.Provider
	Verifier       *oidc.IDTokenVerifier
	OAuth2Config   *oauth2.Config
	Provision      *oidcprovision.Service
	AuthService    *authservice.Service
	LicenseChecker license.Checker
	LoginBasePath  string // Base path for login page redirect
}

// InitBuiltinOIDCConfig initializes OIDC for builtin auth mode.
func InitBuiltinOIDCConfig(ctx context.Context, cfg config.AuthOIDC, authSvc *authservice.Service, provisionSvc *oidcprovision.Service, basePath string) (*BuiltinOIDCConfig, error) {
	result, err := initOIDCProviderCore(ctx, oidcProviderParams{
		Issuer:       cfg.Issuer,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		ClientURL:    cfg.ClientURL,
		Scopes:       cfg.Scopes,
	})
	if err != nil {
		return nil, err
	}

	loginBasePath := basePath
	if loginBasePath == "" {
		loginBasePath = "/"
	}
	if !strings.HasPrefix(loginBasePath, "/") {
		loginBasePath = "/" + loginBasePath
	}

	return &BuiltinOIDCConfig{
		Provider:      result.Provider,
		Verifier:      result.Verifier,
		OAuth2Config:  result.OAuth2Config,
		Provision:     provisionSvc,
		AuthService:   authSvc,
		LoginBasePath: loginBasePath,
	}, nil
}

// BuiltinOIDCLoginHandler returns a handler that initiates the OIDC login flow.
func BuiltinOIDCLoginHandler(cfg *BuiltinOIDCConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.LicenseChecker != nil && !cfg.LicenseChecker.IsFeatureEnabled(license.FeatureSSO) {
			redirectWithError(w, r, cfg.LoginBasePath, "SSO requires an active Dagu license")
			return
		}

		state := stringutil.RandomString(16)
		nonce := stringutil.RandomString(16)

		setCookie(w, r, cookieState, state, stateCookieExpiry)
		setCookie(w, r, cookieNonce, nonce, stateCookieExpiry)
		setCookie(w, r, cookieOriginalURL, cfg.LoginBasePath, stateCookieExpiry)

		http.Redirect(w, r, cfg.OAuth2Config.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
	}
}

// BuiltinOIDCCallbackHandler returns a handler that processes the OIDC callback.
// It uses the provisioning service to create/lookup users and generates JWT tokens.
func BuiltinOIDCCallbackHandler(cfg *BuiltinOIDCConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if cfg.LicenseChecker != nil && !cfg.LicenseChecker.IsFeatureEnabled(license.FeatureSSO) {
			redirectWithError(w, r, cfg.LoginBasePath, "SSO requires an active Dagu license")
			return
		}

		// Verify state
		stateCookie, err := r.Cookie(cookieState)
		if err != nil {
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: state not found")
			return
		}
		if r.URL.Query().Get("state") != stateCookie.Value {
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: state mismatch")
			return
		}

		// Check for error from OIDC provider
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			errDesc := r.URL.Query().Get("error_description")
			if errDesc == "" {
				errDesc = errParam
			}
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: "+errDesc)
			return
		}

		// Exchange code for token
		oauth2Token, err := cfg.OAuth2Config.Exchange(ctx, r.URL.Query().Get("code"))
		if err != nil {
			logger.Error(ctx, "OIDC token exchange failed", tag.Error(err))
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: could not exchange token")
			return
		}

		// Get raw ID token
		rawIDToken, ok := oauth2Token.Extra("id_token").(string)
		if !ok {
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: no ID token")
			return
		}

		// Verify ID token
		idToken, err := cfg.Verifier.Verify(ctx, rawIDToken)
		if err != nil {
			logger.Error(ctx, "OIDC token verification failed", tag.Error(err))
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: invalid token")
			return
		}

		// Verify nonce
		nonceCookie, err := r.Cookie(cookieNonce)
		if err != nil {
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: nonce not found")
			return
		}
		if idToken.Nonce != nonceCookie.Value {
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: nonce mismatch")
			return
		}

		// Extract typed claims
		var claims oidcprovision.OIDCClaims
		if err := idToken.Claims(&claims); err != nil {
			logger.Error(ctx, "Failed to extract OIDC claims", tag.Error(err))
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: could not read user info")
			return
		}

		// Extract raw claims for role mapping
		var rawClaims map[string]any
		if err := idToken.Claims(&rawClaims); err != nil {
			logger.Error(ctx, "Failed to extract raw OIDC claims", tag.Error(err))
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: could not read user info")
			return
		}
		claims.RawClaims = rawClaims

		// Get userinfo for email (may not be in ID token)
		userInfo, err := cfg.Provider.UserInfo(ctx, oauth2.StaticTokenSource(oauth2Token))
		if err != nil {
			logger.Warn(ctx, "Failed to get userinfo, using ID token claims", tag.Error(err))
			// Continue with ID token claims if userinfo fails
		} else {
			// Prefer userinfo email
			if userInfo.Email != "" {
				claims.Email = userInfo.Email
			}
			// Merge userinfo claims into raw claims for role mapping
			var userInfoClaims map[string]any
			if err := userInfo.Claims(&userInfoClaims); err == nil {
				for k, v := range userInfoClaims {
					if _, exists := claims.RawClaims[k]; !exists {
						claims.RawClaims[k] = v
					}
				}
			}
		}

		// Process login (create/lookup user)
		user, isNewUser, err := cfg.Provision.ProcessLogin(ctx, claims)
		if err != nil {
			logger.Warn(ctx, "OIDC provisioning failed",
				slog.String("email_domain", stringutil.ExtractEmailDomain(claims.Email)),
				slog.String("subject", claims.Subject),
				tag.Error(err))
			redirectWithError(w, r, cfg.LoginBasePath, err.Error())
			return
		}

		// Generate JWT token
		tokenResult, err := cfg.AuthService.GenerateToken(user)
		if err != nil {
			logger.Error(ctx, "Failed to generate JWT token", tag.Error(err))
			redirectWithError(w, r, cfg.LoginBasePath, "Authentication failed: could not create session")
			return
		}

		// Clear OIDC cookies
		clearOIDCStateCookies(w, r)

		// Redirect to login page with token in the URL hash fragment.
		// Hash fragments are never sent to the server, so the JWT does not appear
		// in server access logs, reverse-proxy logs, or Referer headers.
		redirectURL := strings.TrimSuffix(cfg.LoginBasePath, "/") + "/login"
		if isNewUser {
			redirectURL += "?welcome=true"
		}
		redirectURL += "#token=" + url.QueryEscape(tokenResult.Token)
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

// redirectWithError redirects to the login page with an error message.
func redirectWithError(w http.ResponseWriter, r *http.Request, basePath, errMsg string) {
	clearOIDCStateCookies(w, r)

	redirectURL := strings.TrimSuffix(basePath, "/") + "/login?error=" + url.QueryEscape(errMsg)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}
