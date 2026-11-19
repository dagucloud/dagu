// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubSelfUpgrader struct {
	status   openapiv1.SelfUpgradeStatus
	startErr error
	started  bool
}

func (s *stubSelfUpgrader) Status() openapiv1.SelfUpgradeStatus { return s.status }

func (s *stubSelfUpgrader) Start() error {
	s.started = s.startErr == nil
	return s.startErr
}

func TestStartSelfUpgrade(t *testing.T) {
	t.Parallel()

	t.Run("starts and returns the state", func(t *testing.T) {
		t.Parallel()
		upgrader := &stubSelfUpgrader{status: openapiv1.SelfUpgradeStatus{Available: true, State: openapiv1.SelfUpgradeStateDownloading}}
		a := &API{selfUpgrader: upgrader}

		resp, err := a.StartSelfUpgrade(context.Background(), openapiv1.StartSelfUpgradeRequestObject{})
		require.NoError(t, err)
		assert.True(t, upgrader.started)
		assert.Equal(t, openapiv1.StartSelfUpgrade202JSONResponse(upgrader.status), resp)
	})

	for name, startErr := range map[string]error{
		"already running": ErrSelfUpgradeRunning,
		"unavailable":     ErrSelfUpgradeUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := &API{selfUpgrader: &stubSelfUpgrader{startErr: startErr}}

			_, err := a.StartSelfUpgrade(context.Background(), openapiv1.StartSelfUpgradeRequestObject{})
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
			assert.Equal(t, startErr.Error(), apiErr.Message)
		})
	}

	t.Run("without an upgrader", func(t *testing.T) {
		t.Parallel()
		_, err := (&API{}).StartSelfUpgrade(context.Background(), openapiv1.StartSelfUpgradeRequestObject{})
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
	})

	t.Run("other failures pass through", func(t *testing.T) {
		t.Parallel()
		failure := errors.New("disk full")
		_, err := (&API{selfUpgrader: &stubSelfUpgrader{startErr: failure}}).StartSelfUpgrade(context.Background(), openapiv1.StartSelfUpgradeRequestObject{})
		assert.ErrorIs(t, err, failure)
	})
}

func TestGetSelfUpgrade(t *testing.T) {
	t.Parallel()

	resp, err := (&API{}).GetSelfUpgrade(context.Background(), openapiv1.GetSelfUpgradeRequestObject{})
	require.NoError(t, err)
	assert.Equal(t, openapiv1.GetSelfUpgrade200JSONResponse{State: openapiv1.SelfUpgradeStateIdle}, resp)

	status := openapiv1.SelfUpgradeStatus{Available: true, State: openapiv1.SelfUpgradeStateRestarting}
	resp, err = (&API{selfUpgrader: &stubSelfUpgrader{status: status}}).GetSelfUpgrade(context.Background(), openapiv1.GetSelfUpgradeRequestObject{})
	require.NoError(t, err)
	assert.Equal(t, openapiv1.GetSelfUpgrade200JSONResponse(status), resp)
}
