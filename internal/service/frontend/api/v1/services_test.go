// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	persistestutil "github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSchedulerPauseServer(t *testing.T) test.Server {
	t.Helper()
	return setupWebhookTestServer(t)
}

func schedulerPauseState(t *testing.T, server test.Server, token string) api.SchedulerPauseState {
	t.Helper()
	resp := server.Client().Get("/api/v1/services/scheduler/pause").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)
	var state api.SchedulerPauseState
	resp.Unmarshal(t, &state)
	return state
}

func TestSchedulerPause_DefaultsToRunning(t *testing.T) {
	t.Parallel()
	server := setupSchedulerPauseServer(t)
	adminToken := getWebhookAdminToken(t, server)

	state := schedulerPauseState(t, server, adminToken)

	assert.False(t, state.Paused)
	assert.Nil(t, state.PausedBy)
	assert.Nil(t, state.Reason)
}

func TestSchedulerPause_RoundTripRecordsActorAndReason(t *testing.T) {
	t.Parallel()
	server := setupSchedulerPauseServer(t)
	adminToken := getWebhookAdminToken(t, server)

	reason := "db migration"
	server.Client().Post("/api/v1/services/scheduler/pause", api.UpdateSchedulerPauseStateJSONRequestBody{
		Paused: true,
		Reason: &reason,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	state := schedulerPauseState(t, server, adminToken)
	require.True(t, state.Paused)
	require.NotNil(t, state.PausedBy)
	assert.Equal(t, "admin", *state.PausedBy)
	require.NotNil(t, state.Reason)
	assert.Equal(t, "db migration", *state.Reason)
	assert.NotNil(t, state.PausedAt)

	server.Client().Post("/api/v1/services/scheduler/pause", api.UpdateSchedulerPauseStateJSONRequestBody{
		Paused: false,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	resumed := schedulerPauseState(t, server, adminToken)
	assert.False(t, resumed.Paused)
	assert.Nil(t, resumed.PausedBy)
	assert.Nil(t, resumed.Reason)
}

// Pausing affects every workspace, so it is restricted to admins. Reading is
// deliberately open to every authenticated role, because a paused scheduler
// explains why nothing is running.
func TestSchedulerPause_MutationRequiresAdminButReadDoesNot(t *testing.T) {
	t.Parallel()
	server := setupSchedulerPauseServer(t)
	adminToken := getWebhookAdminToken(t, server)

	for _, u := range []struct {
		username string
		password string
		role     api.UserRole
	}{
		{"manager-user", "manager1", api.UserRoleManager},
		{"developer-user", "developer1", api.UserRoleDeveloper},
		{"operator-user", "operator1", api.UserRoleOperator},
		{"viewer-user", "viewerpass1", api.UserRoleViewer},
	} {
		server.Client().Post("/api/v1/users", api.CreateUserRequest{
			Username: u.username,
			Password: u.password,
			Role:     u.role,
		}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

		resp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
			Username: u.username,
			Password: u.password,
		}).ExpectStatus(http.StatusOK).Send(t)
		var login api.LoginResponse
		resp.Unmarshal(t, &login)

		server.Client().Post("/api/v1/services/scheduler/pause", api.UpdateSchedulerPauseStateJSONRequestBody{
			Paused: true,
		}).WithBearerToken(login.Token).ExpectStatus(http.StatusForbidden).Send(t)

		state := schedulerPauseState(t, server, login.Token)
		assert.False(t, state.Paused, "%s must not have been able to pause", u.role)
	}
}

// The reason is rendered in an app-wide banner, so the schema bounds it and the
// request validator rejects anything longer.
func TestSchedulerPause_RejectsOversizedReason(t *testing.T) {
	t.Parallel()
	server := setupSchedulerPauseServer(t)
	adminToken := getWebhookAdminToken(t, server)

	// Multi-byte input confirms the bound counts characters, matching the
	// maxLength the schema documents, rather than bytes.
	oversized := strings.Repeat("\u3042", 513)
	server.Client().Post("/api/v1/services/scheduler/pause", api.UpdateSchedulerPauseStateJSONRequestBody{
		Paused: true,
		Reason: &oversized,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusBadRequest).Send(t)

	assert.False(t, schedulerPauseState(t, server, adminToken).Paused)

	atLimit := strings.Repeat("\u3042", 512)
	server.Client().Post("/api/v1/services/scheduler/pause", api.UpdateSchedulerPauseStateJSONRequestBody{
		Paused: true,
		Reason: &atLimit,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
}

func TestSchedulerPause_ReadRequiresAuthentication(t *testing.T) {
	t.Parallel()
	server := setupSchedulerPauseServer(t)

	server.Client().Get("/api/v1/services/scheduler/pause").
		ExpectStatus(http.StatusUnauthorized).Send(t)
}

// An operator's pause, from its start to its resume, is a span in which no
// DAG's slots run.
func TestSchedulerPause_RecordsSpan(t *testing.T) {
	t.Parallel()
	server := setupSchedulerPauseServer(t)
	adminToken := getWebhookAdminToken(t, server)

	for _, paused := range []bool{true, true, false} {
		server.Client().Post("/api/v1/services/scheduler/pause", api.UpdateSchedulerPauseStateJSONRequestBody{
			Paused: paused,
		}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	}

	spans, err := store.NewSpanStore(server.Backend.Collection(persis.CollectionSlotSpans)).Ended(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, spans, 1)
	assert.Equal(t, schedulerstate.SpanPaused, spans[0].Reason)
	assert.False(t, spans[0].To.Before(spans[0].From))
}

// flakyPause is a pause store whose Set fails while fail is set.
type flakyPause struct {
	schedulerstate.PauseStore
	fail bool
}

func (s *flakyPause) Set(ctx context.Context, paused bool, actor, reason string) error {
	if s.fail {
		return errors.New("pause store unavailable")
	}
	return s.PauseStore.Set(ctx, paused, actor, reason)
}

// A pause or resume is made only once its span is marked, so neither holds
// without its span, and a retry after a failure leaves the span as one.
func TestSchedulerPause_MarksSpanFirst(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	backend := persistestutil.NewMemoryBackend()
	spans := &flakySpans{SpanStore: store.NewSpanStore(backend.Collection(persis.CollectionSlotSpans))}
	pause := &flakyPause{PauseStore: store.NewSchedulerPauseStore(backend.Collection(persis.CollectionSchedulerState))}
	apiImpl := localapi.New(
		helper.DAGRepository, helper.DAGRunRepository, helper.QueueStore,
		helper.ProcRepository, helper.DAGRunMgr, helper.Config, nil, helper.ServiceRegistry, nil, nil,
		localapi.WithSpanStore(spans), localapi.WithSchedulerPauseStore(pause),
	)
	toggle := func(paused bool) error {
		_, err := apiImpl.UpdateSchedulerPauseState(t.Context(), api.UpdateSchedulerPauseStateRequestObject{
			Body: &api.UpdateSchedulerPauseStateJSONRequestBody{Paused: paused},
		})
		return err
	}
	isPaused := func() bool {
		paused, err := pause.IsPaused(t.Context())
		require.NoError(t, err)
		return paused
	}

	for _, paused := range []bool{true, false} {
		spans.fail = true
		require.Error(t, toggle(paused), "a span that cannot be marked")
		assert.Equal(t, !paused, isPaused(), "the pause stays as it was")

		spans.fail, pause.fail = false, true
		require.Error(t, toggle(paused), "a pause that cannot be written")
		pause.fail = false
		require.NoError(t, toggle(paused), "the retry")
		assert.Equal(t, paused, isPaused())
	}

	ended, err := spans.Ended(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, ended, 1)
	requireNoSpanLasts(t, spans, "", schedulerstate.SpanPaused)
}
