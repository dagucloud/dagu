// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/service/audit"
	frontendauth "github.com/dagucloud/dagu/internal/service/frontend/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type restAuditStore struct {
	entries []*audit.Entry
}

func (s *restAuditStore) Append(_ context.Context, entry *audit.Entry) error {
	s.entries = append(s.entries, entry)
	return nil
}

func (s *restAuditStore) Query(_ context.Context, _ audit.QueryFilter) (*audit.QueryResult, error) {
	return &audit.QueryResult{Entries: s.entries, Total: len(s.entries)}, nil
}

func TestLogRESTAuthDeniedLogsUnresolvedAPIKeyAttempts(t *testing.T) {
	store := &restAuditStore{}
	api := &API{auditService: audit.New(store)}
	req := newRequestWithRESTAuditSource("Bearer dagu_invalid")

	api.logRESTAuthDenied(req, frontendauth.DenialReasonAuthFailed, nil)

	require.Len(t, store.entries, 1)
	entry := store.entries[0]
	assert.Equal(t, audit.CategoryAPIKey, entry.Category)
	assert.Equal(t, "api_key_request_denied", entry.Action)
	assert.Equal(t, "rest", entry.Source)
	assert.Equal(t, string(auth.APIKeySurfaceREST), entry.Surface)
	assert.Equal(t, "denied", entry.Result)
	assert.Equal(t, "rest_request", entry.ResourceType)
	assert.Equal(t, "/api/v1/dags", entry.ResourceID)
	assert.Equal(t, "api_key", entry.CredentialType)
	assert.Contains(t, entry.Details, `"denial_reason":"auth_failed"`)
}

func TestLogRESTAuthDeniedIgnoresNonAPIKeyAuthFailures(t *testing.T) {
	store := &restAuditStore{}
	api := &API{auditService: audit.New(store)}
	req := newRequestWithRESTAuditSource("Bearer jwt-invalid")

	api.logRESTAuthDenied(req, frontendauth.DenialReasonAuthFailed, nil)

	assert.Empty(t, store.entries)
}

func newRequestWithRESTAuditSource(authHeader string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dags", nil)
	req.Header.Set("Authorization", authHeader)
	source := &audit.SourceContext{
		Source:        "rest",
		Surface:       string(auth.APIKeySurfaceREST),
		RequestID:     "request-id",
		CorrelationID: "correlation-id",
		Transport:     "http",
	}
	return req.WithContext(audit.WithSourceContext(req.Context(), source))
}
