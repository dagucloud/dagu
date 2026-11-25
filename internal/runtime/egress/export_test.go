// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress

import "time"

// SetTunnelIdleForTest sets the time after which an idle tunnel ends.
func (p *Proxy) SetTunnelIdleForTest(idle time.Duration) {
	p.tunnelIdle = idle
}

// SetKeepAliveIdleForTest sets the time after which a kept connection with no
// request ends.
func (p *Proxy) SetKeepAliveIdleForTest(idle time.Duration) {
	p.keepAliveIdle = idle
}
