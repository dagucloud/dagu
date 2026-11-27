// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A request that reached the server at localhost or a loopback address
// gives its origin, with the base path, as the usable public URL when the
// server has none. Another host gives none, and a configured URL wins.
func TestLocalhostPublicURL(t *testing.T) {
	publicURL := func(a *API, host string) string {
		var got string
		handler := localhostOrigin("/dagu/")(func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
			got = a.usablePublicURL(ctx)
			return nil, nil
		}, "ListConnectionProviders")
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/dagu/api/v1/connection-providers", nil)
		_, _ = handler(r.Context(), httptest.NewRecorder(), r, nil)
		return got
	}
	a := &API{}
	assert.Equal(t, "http://localhost:8080/dagu", publicURL(a, "localhost:8080"))
	assert.Equal(t, "http://127.0.0.1:8080/dagu", publicURL(a, "127.0.0.1:8080"))
	assert.Empty(t, publicURL(a, "dagu.internal:8080"))
	assert.Empty(t, publicURL(a, "192.168.1.10:8080"))

	a.publicURL = func() string { return "https://dagu.example.com" }
	assert.Equal(t, "https://dagu.example.com", publicURL(a, "localhost:8080"))
}
