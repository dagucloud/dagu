// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress

import (
	"crypto/tls"
	"time"
)

// Digest is the digest of a credential in a log line.
var Digest = digest

// SetTLSConfigForTest sets the TLS configuration that the proxy uses to reach an
// https upstream. A test uses it to trust the certificate of a test server.
func (p *Proxy) SetTLSConfigForTest(config *tls.Config) {
	p.transport.TLSClientConfig = config
}

// SetTunnelIdleForTest sets the time after which an idle tunnel ends.
func (p *Proxy) SetTunnelIdleForTest(idle time.Duration) {
	p.tunnelIdle = idle
}

// SetKeepAliveIdleForTest sets the time after which a kept connection with no
// request ends.
func (p *Proxy) SetKeepAliveIdleForTest(idle time.Duration) {
	p.keepAliveIdle = idle
}
