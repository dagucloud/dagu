// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/runtime"
	apiV1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionTestSetup contains common test infrastructure for agent session API tests.
type sessionTestSetup struct {
	api          *apiV1.API
	sessionStore *mockSessionStore
	configStore  *mockAgentConfigStore
}

func newSessionTestSetup(t *testing.T) *sessionTestSetup {
	t.Helper()

	ss := &mockSessionStore{}
	cs := &mockAgentConfigStore{config: agent.DefaultConfig()}

	agentAPI := agent.NewAPI(agent.APIConfig{
		SessionStore: ss,
	})

	a := apiV1.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		apiV1.WithAgentAPI(agentAPI),
		apiV1.WithAgentConfigStore(cs),
	)

	return &sessionTestSetup{
		api:          a,
		sessionStore: ss,
		configStore:  cs,
	}
}

// mockSessionStore implements agent.SessionStore for integration tests.
type mockSessionStore struct {
	sessions []*agent.Session
}

func (m *mockSessionStore) CreateSession(_ context.Context, sess *agent.Session) error {
	if sess.ID == "" {
		return agent.ErrInvalidSessionID
	}
	if sess.UserID == "" {
		return agent.ErrInvalidUserID
	}
	m.sessions = append(m.sessions, sess)
	return nil
}

func (m *mockSessionStore) GetSession(_ context.Context, id string) (*agent.Session, error) {
	for _, s := range m.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, agent.ErrSessionNotFound
}

func (m *mockSessionStore) ListSessions(_ context.Context, userID string) ([]*agent.Session, error) {
	if userID == "" {
		return nil, agent.ErrInvalidUserID
	}
	var result []*agent.Session
	for _, s := range m.sessions {
		if s.UserID == userID {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSessionStore) UpdateSession(_ context.Context, sess *agent.Session) error {
	for i, s := range m.sessions {
		if s.ID == sess.ID {
			m.sessions[i] = sess
			return nil
		}
	}
	return agent.ErrSessionNotFound
}

func (m *mockSessionStore) DeleteSession(_ context.Context, id string) error {
	for i, s := range m.sessions {
		if s.ID == id {
			m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
			return nil
		}
	}
	return agent.ErrSessionNotFound
}

func (m *mockSessionStore) AddMessage(_ context.Context, _ string, _ *agent.Message) error {
	return nil
}

func (m *mockSessionStore) GetMessages(_ context.Context, _ string) ([]agent.Message, error) {
	return nil, nil
}

func (m *mockSessionStore) GetLatestSequenceID(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (m *mockSessionStore) ListSubSessions(_ context.Context, _ string) ([]*agent.Session, error) {
	return nil, nil
}

var _ agent.SessionStore = (*mockSessionStore)(nil)

func sessionAdminCtx() context.Context {
	return auth.WithUser(context.Background(), &auth.User{
		ID:       "admin-1",
		Username: "admin",
		Role:     auth.RoleAdmin,
	})
}

//go:fix inline
func ptrInt(v int) *int { return new(v) }

func TestListAgentSessions(t *testing.T) {
	t.Parallel()

	t.Run("returns empty list when no sessions", func(t *testing.T) {
		t.Parallel()

		setup := newSessionTestSetup(t)

		resp, err := setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		assert.Empty(t, listResp.Sessions)
		assert.Equal(t, 0, listResp.Pagination.TotalRecords)
		assert.Equal(t, 1, listResp.Pagination.CurrentPage)
		assert.Equal(t, 1, listResp.Pagination.TotalPages)
	})

	t.Run("returns sessions with pagination metadata", func(t *testing.T) {
		t.Parallel()

		setup := newSessionTestSetup(t)
		now := time.Now()

		for i := range 5 {
			setup.sessionStore.sessions = append(setup.sessionStore.sessions, &agent.Session{
				ID:        fmt.Sprintf("sess-%d", i+1),
				UserID:    "admin-1",
				CreatedAt: now.Add(time.Duration(-i) * time.Hour),
				UpdatedAt: now.Add(time.Duration(-i) * time.Hour),
			})
		}

		resp, err := setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{
				Page:    new(1),
				PerPage: new(3),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		assert.Len(t, listResp.Sessions, 3)
		assert.Equal(t, 5, listResp.Pagination.TotalRecords)
		assert.Equal(t, 1, listResp.Pagination.CurrentPage)
		assert.Equal(t, 2, listResp.Pagination.TotalPages)
		assert.Equal(t, 2, listResp.Pagination.NextPage)
		assert.Equal(t, 1, listResp.Pagination.PrevPage)
	})

	t.Run("returns second page correctly", func(t *testing.T) {
		t.Parallel()

		setup := newSessionTestSetup(t)
		now := time.Now()

		for i := range 5 {
			setup.sessionStore.sessions = append(setup.sessionStore.sessions, &agent.Session{
				ID:        fmt.Sprintf("sess-%d", i+1),
				UserID:    "admin-1",
				CreatedAt: now.Add(time.Duration(-i) * time.Hour),
				UpdatedAt: now.Add(time.Duration(-i) * time.Hour),
			})
		}

		resp, err := setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{
				Page:    new(2),
				PerPage: new(3),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		assert.Len(t, listResp.Sessions, 2)
		assert.Equal(t, 5, listResp.Pagination.TotalRecords)
		assert.Equal(t, 2, listResp.Pagination.CurrentPage)
		assert.Equal(t, 2, listResp.Pagination.TotalPages)
		assert.Equal(t, 2, listResp.Pagination.NextPage)
		assert.Equal(t, 1, listResp.Pagination.PrevPage)
	})

	t.Run("returns cursor page with next cursor", func(t *testing.T) {
		t.Parallel()

		setup := newSessionTestSetup(t)
		now := time.Now()

		for i := range 3 {
			setup.sessionStore.sessions = append(setup.sessionStore.sessions, &agent.Session{
				ID:        fmt.Sprintf("sess-%d", i+1),
				UserID:    "admin-1",
				CreatedAt: now.Add(time.Duration(-i) * time.Hour),
				UpdatedAt: now.Add(time.Duration(-i) * time.Hour),
			})
		}

		mode := apigen.ListAgentSessionsParamsPaginationModeCursor
		resp, err := setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{
				PaginationMode: &mode,
				PerPage:        new(2),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		require.Len(t, listResp.Sessions, 2)
		assert.Equal(t, "sess-1", listResp.Sessions[0].SessionId)
		assert.Equal(t, "sess-2", listResp.Sessions[1].SessionId)
		require.NotNil(t, listResp.NextCursor)

		resp, err = setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{
				Cursor:  listResp.NextCursor,
				PerPage: new(2),
			},
		})
		require.NoError(t, err)

		listResp, ok = resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		require.Len(t, listResp.Sessions, 1)
		assert.Equal(t, "sess-3", listResp.Sessions[0].SessionId)
		assert.Nil(t, listResp.NextCursor)
	})

	t.Run("uses default pagination when no params", func(t *testing.T) {
		t.Parallel()

		setup := newSessionTestSetup(t)
		now := time.Now()

		for i := range 3 {
			setup.sessionStore.sessions = append(setup.sessionStore.sessions, &agent.Session{
				ID:        fmt.Sprintf("sess-%d", i+1),
				UserID:    "admin-1",
				CreatedAt: now.Add(time.Duration(-i) * time.Hour),
				UpdatedAt: now.Add(time.Duration(-i) * time.Hour),
			})
		}

		resp, err := setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		assert.Len(t, listResp.Sessions, 3)
		assert.Equal(t, 3, listResp.Pagination.TotalRecords)
		assert.Equal(t, 1, listResp.Pagination.CurrentPage)
	})

	t.Run("excludes sub-sessions from results", func(t *testing.T) {
		t.Parallel()

		setup := newSessionTestSetup(t)
		now := time.Now()

		setup.sessionStore.sessions = append(setup.sessionStore.sessions,
			&agent.Session{
				ID:        "parent-1",
				UserID:    "admin-1",
				CreatedAt: now,
				UpdatedAt: now,
			},
			&agent.Session{
				ID:              "child-1",
				UserID:          "admin-1",
				ParentSessionID: "parent-1",
				CreatedAt:       now,
				UpdatedAt:       now,
			},
			&agent.Session{
				ID:        "parent-2",
				UserID:    "admin-1",
				CreatedAt: now.Add(-time.Hour),
				UpdatedAt: now.Add(-time.Hour),
			},
		)

		resp, err := setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		// Only parent sessions should appear
		assert.Len(t, listResp.Sessions, 2)
		assert.Equal(t, 2, listResp.Pagination.TotalRecords)
		for _, s := range listResp.Sessions {
			assert.Empty(t, deref(s.Session.ParentSessionId), "sub-sessions should not appear")
		}
	})

	t.Run("session fields are correctly mapped", func(t *testing.T) {
		t.Parallel()

		setup := newSessionTestSetup(t)
		now := time.Now().Truncate(time.Second)

		setup.sessionStore.sessions = append(setup.sessionStore.sessions, &agent.Session{
			ID:        "sess-1",
			UserID:    "admin-1",
			Title:     "Test Session",
			DAGName:   "my-dag",
			CreatedAt: now,
			UpdatedAt: now.Add(time.Minute),
		})

		resp, err := setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{
				Page:    new(1),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		require.Len(t, listResp.Sessions, 1)

		s := listResp.Sessions[0]
		assert.Equal(t, "sess-1", s.SessionId)
		assert.Equal(t, "sess-1", s.Session.Id)
		assert.Equal(t, "Test Session", deref(s.Session.Title))
		assert.Equal(t, "my-dag", deref(s.Session.DagName))
		assert.Equal(t, "admin-1", deref(s.Session.UserId))
		assert.False(t, s.Working)
		assert.Equal(t, float64(0), s.TotalCost)
	})

	t.Run("returns error when agent not available", func(t *testing.T) {
		t.Parallel()

		// Create API without agent
		a := apiV1.New(
			nil, nil, nil, nil, runtime.Manager{},
			&config.Config{}, nil, nil,
			prometheus.NewRegistry(),
			nil,
		)

		_, err := a.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{},
		})
		require.Error(t, err)
	})

	t.Run("page beyond range returns empty items", func(t *testing.T) {
		t.Parallel()

		setup := newSessionTestSetup(t)

		setup.sessionStore.sessions = append(setup.sessionStore.sessions, &agent.Session{
			ID:        "sess-1",
			UserID:    "admin-1",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})

		resp, err := setup.api.ListAgentSessions(sessionAdminCtx(), apigen.ListAgentSessionsRequestObject{
			Params: apigen.ListAgentSessionsParams{
				Page:    new(99),
				PerPage: new(10),
			},
		})
		require.NoError(t, err)

		listResp, ok := resp.(apigen.ListAgentSessions200JSONResponse)
		require.True(t, ok)
		assert.Empty(t, listResp.Sessions)
		assert.Equal(t, 1, listResp.Pagination.TotalRecords)
		assert.Equal(t, 99, listResp.Pagination.CurrentPage)
	})
}

// deref safely dereferences a pointer, returning the zero value if nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
