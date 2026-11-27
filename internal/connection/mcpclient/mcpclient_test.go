// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/mcpclient"
)

// fakeSource hands out at-1, and at-2 after a report.
type fakeSource struct {
	mu       sync.Mutex
	rejected []string
}

func (s *fakeSource) Token(context.Context, *connection.Connection, string, time.Duration) (string, time.Duration, error) {
	return "at-1", time.Hour, nil
}

func (s *fakeSource) Rejected(_ context.Context, _ *connection.Connection, _, token string, _ time.Duration) (string, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected = append(s.rejected, token)
	return "at-2", time.Hour, nil
}

func (s *fakeSource) reports() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rejected)
}

// A GET that resumes a stream, which go-sdk never passes to the OAuth
// handler, reports a refused token once and is sent again with the new
// token and its Last-Event-ID.
func TestBearerResumesRefusedStream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		accept     string
		wantStatus int
	}{
		{name: "Refreshed", accept: "Bearer at-2", wantStatus: http.StatusOK},
		{name: "RefusedAgain", wantStatus: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var eventIDs []string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				eventIDs = append(eventIDs, r.Header.Get("Last-Event-ID"))
				mu.Unlock()
				if r.Header.Get("Authorization") != tt.accept {
					w.WriteHeader(http.StatusUnauthorized)
				}
			}))
			t.Cleanup(srv.Close)
			u, err := url.Parse(srv.URL)
			require.NoError(t, err)
			conn := &connection.Connection{Name: "work", Provider: connection.ProviderOAuth2, BoundHosts: []string{u.Host}}
			source := &fakeSource{}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/mcp", nil)
			require.NoError(t, err)
			req.Header.Set("Last-Event-ID", "7")
			resp, err := mcpclient.NewBearer(source, conn, srv.Client().Transport, nil).RoundTrip(req)
			require.NoError(t, err)
			_ = resp.Body.Close()

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Equal(t, []string{"at-1"}, source.reports())
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, []string{"7", "7"}, eventIDs)
		})
	}
}
