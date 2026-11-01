// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplicitPACAccepts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		explicit string
		want     string
	}{
		{"proxy.example:3128", "PROXY proxy.example:3128"},
		{"proxy.example", "PROXY proxy.example:80"},
		{"http://proxy.example:8080/", "PROXY proxy.example:8080"},
		{"HTTP://Proxy.Example:8080", "PROXY Proxy.Example:8080"},
		{"https://proxy.example", "HTTPS proxy.example:443"},
		{"https://proxy.example:8443", "HTTPS proxy.example:8443"},
		{"socks5://10.0.0.1", "SOCKS5 10.0.0.1:1080"},
		{"socks://socks.example:9050", "SOCKS5 socks.example:9050"},
		{"[2001:db8::1]:3128", "PROXY [2001:db8::1]:3128"},
	}
	for _, tt := range tests {
		t.Run(tt.explicit, func(t *testing.T) {
			t.Parallel()
			script, err := explicitPAC(tt.explicit)
			require.NoError(t, err)
			assertRoutes(t, script, []routeCase{
				{"https://www.example.com/", tt.want},
				{"http://intranet/", tt.want},
				{"http://localhost:5173/", "DIRECT"},
				{"http://127.0.0.1:8080/", "DIRECT"},
				{"http://[::1]:8080/", "DIRECT"},
			})
		})
	}
}

func TestExplicitPACRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		explicit string
		reason   string
	}{
		{"http=proxy:80;https=proxy:443", "proxy rules such as http=a;https=b are not supported"},
		{"proxy-a:80;proxy-b:80", "proxy rules such as http=a;https=b are not supported"},
		{"proxy-a:80 proxy-b:80", "proxy lists are not supported"},
		{"proxy-a:80,proxy-b:80", "proxy lists are not supported"},
		{"direct://", "direct:// is not supported"},
		{"user:secret@proxy.example:8080", "user name or password"},
		{"http://user@proxy.example", "user name or password"},
		{"ftp://proxy.example", `proxy scheme "ftp" is not supported`},
		{"socks4://proxy.example", `proxy scheme "socks4" is not supported`},
		{"proxy.example:http", "invalid port"},
		{"proxy.example:70000", "not a port number"},
		{"http://proxy.example/path", "without a path"},
		{"http://", "has no host"},
		{"pro_xy!.example", "not a host name"},
	}
	for _, tt := range tests {
		t.Run(tt.explicit, func(t *testing.T) {
			t.Parallel()
			_, err := explicitPAC(tt.explicit)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.reason)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}
