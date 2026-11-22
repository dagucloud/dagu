// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package netutil

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsPrivateAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{addr: "10.0.0.1", want: true},
		{addr: "127.0.0.1", want: true},
		{addr: "169.254.169.254", want: true},
		{addr: "8.8.8.8", want: false},
		{addr: "::1", want: true},
		{addr: "fd00::1", want: true},
		{addr: "2001:4860:4860::8888", want: false},
		{addr: "::ffff:10.0.0.1", want: true},
		// IPv4-compatible.
		{addr: "::a00:1", want: true},
		{addr: "::808:808", want: false},
		// NAT64 well-known prefix and local-use prefix.
		{addr: "64:ff9b::a9fe:a9fe", want: true},
		{addr: "64:ff9b::808:808", want: false},
		{addr: "64:ff9b:1::808:808", want: true},
		{addr: "64:ff9b::a9fe:a9fe%eth0", want: true},
		// 6to4.
		{addr: "2002:7f00:1::", want: true},
		{addr: "2002:808:808::", want: false},
		// Teredo with a private server, and with a private client (10.0.0.1).
		{addr: "2001:0:a00:1::", want: true},
		{addr: "2001:0:4136:e378:8000:63bf:f5ff:fffe", want: true},
		{addr: "2001:0:4136:e378:8000:63bf:f7f7:f7f7", want: false},
	}
	for _, test := range tests {
		t.Run(test.addr, func(t *testing.T) {
			assert.Equal(t, test.want, IsPrivateAddr(netip.MustParseAddr(test.addr)))
		})
	}
}
