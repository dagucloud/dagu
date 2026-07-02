// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !headless

package frontend_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/service/frontend"
)

func TestEmbeddedWebUIAvailableInDefaultBuild(t *testing.T) {
	t.Parallel()

	require.True(t, frontend.WebUIEmbeddedForTest())

	srv := frontend.NewRouteTestServerForTest(&config.Config{})
	r := chi.NewMux()

	require.NoError(t, frontend.SetupRoutesForTest(context.Background(), srv, r))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}
