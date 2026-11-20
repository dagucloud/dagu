// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"net/http"

	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
)

// SetCredential removes each credential header of the client from h and sets the
// header of the form of the endpoint. An empty key sets no header. The gateway
// and the models list use it, so both call the provider with the same header.
func SetCredential(h http.Header, auth Auth, key string) {
	for _, name := range []string{"X-Api-Key", "Authorization", "Proxy-Authorization"} {
		h.Del(name)
	}
	if key == "" {
		return
	}
	switch auth {
	case AuthXAPIKey:
		h.Set("X-Api-Key", key)
	case AuthBearer:
		h.Set("Authorization", "Bearer "+key)
	case AuthAnthropicOAuth:
		h.Set("Authorization", "Bearer "+key)
		h.Set("Anthropic-Beta", egress.WithBeta(h.Values("Anthropic-Beta"), egress.AnthropicOAuthBeta))
	}
}
