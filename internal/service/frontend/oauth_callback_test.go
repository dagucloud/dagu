// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
)

// A refused callback shows a fixed message, never the code or the state
// it was given, and the page may run only its own script.
func TestOAuthCallbackRefusesUnknownState(t *testing.T) {
	t.Parallel()
	stores := persisfile.NewServerStores(&config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}})
	connections, err := stores.ConnectionStore()
	require.NoError(t, err)
	enc, err := stores.Encryptor()
	require.NoError(t, err)
	engine, err := connect.New(connect.Config{Store: connections, Locks: stores.ConnectionLocks(), Encryptor: enc})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=secret-code&state=forged-state", nil)
	oauthCallbackHandler(engine).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "default-src 'none'")
	assert.Contains(t, rec.Body.String(), callbackRefusedMessage)
	assert.Contains(t, rec.Body.String(), closePopupScript)
	assert.NotContains(t, rec.Body.String(), "secret-code")
	assert.NotContains(t, rec.Body.String(), "forged-state")
}
