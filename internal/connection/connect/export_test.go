// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect

import (
	"context"
	"time"
)

// SetClock makes an engine built from cfg read the time from now.
func SetClock(cfg *Config, now func() time.Time) {
	cfg.now = now
}

// SetCallContext makes an engine built from cfg run the provider calls of
// a callback on the context that fn derives.
func SetCallContext(cfg *Config, fn func(context.Context) (context.Context, context.CancelFunc)) {
	cfg.callContext = fn
}
