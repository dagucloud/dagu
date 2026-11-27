// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package google

import "net/http"

// SetTransport makes a provider built from cfg send its requests through
// rt.
func SetTransport(cfg *Config, rt http.RoundTripper) {
	cfg.transport = rt
}
