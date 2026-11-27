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

// A request that came from a loopback peer to localhost or a loopback
// address gives its origin, with the base path, as the usable public URL
// when the server has none. A proxy on the same host that rewrites Host to
// a loopback address marks the request with a forwarding header, or its
// browser names a page of another origin, so neither counts. A configured
// URL wins.
func TestLocalhostPublicURL(t *testing.T) {
	publicURL := func(a *API, host, peer string, headers map[string]string) string {
		var got string
		handler := localhostOrigin("/dagu/")(func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
			got = a.usablePublicURL(ctx)
			return nil, nil
		}, "ListConnectionProviders")
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/dagu/api/v1/connection-providers", nil)
		r.RemoteAddr = peer
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		_, _ = handler(r.Context(), httptest.NewRecorder(), r, nil)
		return got
	}
	const local = "127.0.0.1:50000"
	a := &API{}
	assert.Equal(t, "http://localhost:8080/dagu", publicURL(a, "localhost:8080", local, nil))
	assert.Equal(t, "http://localhost:8080/dagu", publicURL(a, "localhost:8080", "[::1]:50000", map[string]string{"Referer": "http://localhost:8080/dagu/connections"}))
	assert.Equal(t, "http://127.0.0.1:8080/dagu", publicURL(a, "127.0.0.1:8080", local, nil))
	assert.Empty(t, publicURL(a, "dagu.internal:8080", local, nil))
	assert.Empty(t, publicURL(a, "localhost:8080", "192.0.2.10:50000", nil), "a peer on another host")
	assert.Empty(t, publicURL(a, "127.0.0.1:8080", local, map[string]string{"X-Forwarded-For": "192.0.2.10"}), "a proxy that says so")
	assert.Empty(t, publicURL(a, "127.0.0.1:8080", local, map[string]string{"Referer": "https://dagu.example.com/connections"}), "a proxy that rewrote Host")
	assert.Empty(t, publicURL(a, "127.0.0.1:8080", local, map[string]string{"Origin": "https://dagu.example.com"}), "a proxy that rewrote Host")

	a.publicURL = func() string { return "https://dagu.example.com" }
	assert.Equal(t, "https://dagu.example.com", publicURL(a, "localhost:8080", local, nil))
}
