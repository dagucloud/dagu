// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect

import "time"

// SetClock makes an engine built from cfg read the time from now.
func SetClock(cfg *Config, now func() time.Time) {
	cfg.now = now
}

// SetConsentTimeout makes an engine built from cfg bound the provider
// calls of a callback by d.
func SetConsentTimeout(cfg *Config, d time.Duration) {
	cfg.consentTimeout = d
}
