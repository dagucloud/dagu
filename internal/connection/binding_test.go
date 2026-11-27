// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection_test

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/connection"
)

func TestBindsURL(t *testing.T) {
	t.Parallel()
	conn := &connection.Connection{
		Provider:   connection.ProviderGoogle,
		Services:   []connection.Service{{Name: "gmail", Access: connection.AccessRead}},
		BoundHosts: []string{"api.example.com:8443", "10.0.0.1", "[2001:db8::1]"},
	}
	tests := []struct {
		url  string
		want bool
	}{
		{url: "https://gmailmcp.googleapis.com/mcp/v1", want: true},
		{url: "https://GMAILMCP.googleapis.com./mcp/v1", want: true},
		{url: "https://gmailmcp.googleapis.com:443/mcp/v1", want: true},
		{url: "https://gmail.googleapis.com/gmail/v1", want: true},
		{url: "https://api.example.com:8443/v1", want: true},
		{url: "https://10.0.0.1/v1", want: true},
		{url: "https://[2001:db8:0::1]/v1", want: true},
		{url: "http://gmailmcp.googleapis.com/mcp/v1"},
		{url: "https://gmailmcp.googleapis.com:8443/mcp/v1"},
		{url: "https://evil.gmailmcp.googleapis.com/mcp/v1"},
		{url: "https://gmailmcp.googleapis.com.evil.com/mcp/v1"},
		{url: "https://api.example.com/v1"},
		{url: "https://10.0.0.2/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tt.url)
			require.NoError(t, err)
			assert.Equal(t, tt.want, conn.BindsURL(u))
		})
	}
}
