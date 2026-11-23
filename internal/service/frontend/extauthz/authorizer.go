// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package extauthz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

const maxPDPTokenBytes = 64 << 10

// ErrDenied reports a "false" decision of the PDP.
var ErrDenied = errors.New("denied by the external policy")

// Authorizer verifies identity tokens and asks the PDP for decisions.
type Authorizer struct {
	header   string
	verifier *TokenVerifier
	pdp      *PDPClient
}

// New builds the authorizer from the server configuration. It reads the
// public keys file and the PDP token file once, so that a broken file stops
// the server at start.
func New(cfg config.AuthExternal) (*Authorizer, error) {
	keys, err := LoadPublicKeys(cfg.Token.PublicKeysFile)
	if err != nil {
		return nil, fmt.Errorf("auth.external.token.public_keys_file: %w", err)
	}
	verifier, err := NewTokenVerifier(TokenConfig{
		Issuer:   cfg.Token.Issuer,
		Audience: cfg.Token.Audience,
		MaxAge:   cfg.Token.MaxAge,
	}, keys)
	if err != nil {
		return nil, fmt.Errorf("auth.external.token.public_keys_file: %w", err)
	}

	var token string
	if cfg.AuthZEN.TokenFile != "" {
		data, err := readLimitedFile(cfg.AuthZEN.TokenFile, maxPDPTokenBytes)
		if err != nil {
			return nil, fmt.Errorf("auth.external.authzen.token_file: %w", err)
		}
		token = strings.TrimRight(string(data), "\r\n")
		if token == "" {
			return nil, fmt.Errorf("auth.external.authzen.token_file: %s is empty", cfg.AuthZEN.TokenFile)
		}
	}

	pdp := NewPDPClient(PDPConfig{
		BaseURL:  cfg.AuthZEN.BaseURL,
		Token:    token,
		Timeout:  cfg.AuthZEN.Timeout,
		CacheTTL: cfg.AuthZEN.CacheTTL,
	})
	return NewWith(cfg.Token.Header, verifier, pdp), nil
}

// NewWith returns an authorizer from its parts.
func NewWith(header string, verifier *TokenVerifier, pdp *PDPClient) *Authorizer {
	return &Authorizer{header: http.CanonicalHeaderKey(header), verifier: verifier, pdp: pdp}
}

// Authenticate verifies the identity token of the request.
func (a *Authorizer) Authenticate(r *http.Request) (Subject, error) {
	values := r.Header.Values(a.header)
	if len(values) > 1 {
		return Subject{}, errors.New("identity token header is repeated")
	}
	var raw string
	if len(values) == 1 {
		raw = strings.TrimSpace(values[0])
	}
	sub, err := a.verifier.Verify(raw)
	if err != nil {
		return Subject{}, err
	}
	return Subject{Type: SubjectTypeUser, ID: sub}, nil
}

// Check returns nil when the PDP allows the action. It returns ErrDenied for
// a "false" decision and another error when no valid decision is available.
func (a *Authorizer) Check(ctx context.Context, subject Subject, action Action, resource Resource) error {
	allowed, err := a.pdp.Evaluate(ctx, subject, action, resource)
	if err != nil {
		logger.Warn(ctx, "External authorization failed; denying the request",
			slog.String("subject", subject.ID),
			slog.String("action", string(action)),
			slog.String("resourceType", resource.Type),
			slog.String("resourceID", resource.ID),
			tag.Error(err))
		return err
	}
	if !allowed {
		return ErrDenied
	}
	return nil
}

// Allowed returns one decision per resource. Any PDP error denies all.
func (a *Authorizer) Allowed(ctx context.Context, subject Subject, action Action, resources []Resource) ([]bool, error) {
	if len(resources) == 0 {
		return nil, nil
	}
	decisions, err := a.pdp.EvaluateBatch(ctx, subject, action, resources)
	if err != nil {
		logger.Warn(ctx, "External batch authorization failed; denying the request",
			slog.String("subject", subject.ID),
			slog.String("action", string(action)),
			tag.Error(err))
		return nil, err
	}
	return decisions, nil
}

type subjectKey struct{}

// WithSubject stores the verified subject in the context.
func WithSubject(ctx context.Context, subject Subject) context.Context {
	return context.WithValue(ctx, subjectKey{}, subject)
}

// SubjectFromContext returns the verified subject of the request.
func SubjectFromContext(ctx context.Context) (Subject, bool) {
	subject, ok := ctx.Value(subjectKey{}).(Subject)
	return subject, ok
}

// Middleware verifies the identity token and requires one action on the
// server resource. It protects whole surfaces, such as the event stream and
// the MCP endpoint, that have no per-operation mapping.
func (a *Authorizer) Middleware(action Action) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, err := a.Authenticate(r)
			if err != nil {
				logger.Debug(r.Context(), "External identity token rejected", tag.Error(err))
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := WithSubject(r.Context(), subject)
			if err := a.Check(ctx, subject, action, ServerResource()); err != nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
