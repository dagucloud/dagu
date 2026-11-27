// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/connection"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

// testConnections serves the connections it holds by name, and answers
// every token request with token, recording the minTTL asked for. The
// token answer for withdrawn disallows its export, as a binding a worker
// receives with a token does after an admin turned export off.
type testConnections struct {
	conns     map[string]*connection.Connection
	token     string
	withdrawn string
	minTTL    time.Duration
}

func (c *testConnections) Connection(_ context.Context, name string) (*connection.Connection, error) {
	conn, ok := c.conns[name]
	if !ok {
		return nil, connection.ErrNotFound
	}
	return conn, nil
}

func (c *testConnections) Token(_ context.Context, conn *connection.Connection, _ string, minTTL time.Duration) (string, time.Duration, error) {
	c.minTTL = minTTL
	if conn.Name == c.withdrawn {
		c.conns[conn.Name] = &connection.Connection{Name: conn.Name, Services: conn.Services}
	}
	return c.token, time.Hour, nil
}

func (c *testConnections) Rejected(ctx context.Context, conn *connection.Connection, resource, _ string, minTTL time.Duration) (string, time.Duration, error) {
	return c.Token(ctx, conn, resource, minTTL)
}

func TestConnectionResolver(t *testing.T) {
	t.Parallel()

	gmail := []connection.Service{{Name: "gmail", Access: connection.AccessRead}}
	tests := []struct {
		name    string
		key     string
		options map[string]string
		want    string
		wantErr string
	}{
		{name: "Export", key: "work-google", want: "access-token"},
		{name: "Service", key: "work-google", options: map[string]string{"service": "gmail"}, want: "access-token"},
		{name: "NotAService", key: "work-google", options: map[string]string{"service": "drive"}, wantErr: `connection "work-google": service "drive" is not one of its services`},
		{name: "NotAllowed", key: "ops-mail", wantErr: `connection "ops-mail" does not allow token export`},
		{name: "Missing", key: "missing", wantErr: "connection not found"},
		{name: "InvalidKey", key: "Work", wantErr: `invalid secret reference for "TOKEN": key:`},
		{name: "UnknownOption", key: "work-google", options: map[string]string{"scope": "x"}, wantErr: `unknown option "scope"`},
		{name: "Microsoft", key: "work-microsoft", wantErr: `connection "work-microsoft": a Microsoft connection cannot export a token yet`},
		{name: "Withdrawn", key: "withdrawn", wantErr: `connection "withdrawn" does not allow token export`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conns := &testConnections{token: "access-token", withdrawn: "withdrawn", conns: map[string]*connection.Connection{
				"work-google":    {Name: "work-google", Services: gmail, AllowTokenExport: true},
				"ops-mail":       {Name: "ops-mail", Services: gmail},
				"work-microsoft": {Name: "work-microsoft", Provider: connection.ProviderMicrosoft, AllowTokenExport: true},
				"withdrawn":      {Name: "withdrawn", Services: gmail, AllowTokenExport: true},
			}}
			ctx := WithConnections(context.Background(), conns)
			got, err := NewRegistry().Resolve(ctx, secretref.Ref{
				Name: "TOKEN", Provider: secretref.ProviderConnection, Key: tt.key, Options: tt.options,
			})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			// Spec 107 §Contract: token export asks for 50 minutes.
			assert.Equal(t, 50*time.Minute, conns.minTTL)
		})
	}
}

// A run that hands out no connections fails the secret, naming the
// connection.
func TestConnectionResolverWithoutConnections(t *testing.T) {
	t.Parallel()
	_, err := NewRegistry().Resolve(context.Background(), secretref.Ref{
		Name: "TOKEN", Provider: secretref.ProviderConnection, Key: "work-google",
	})
	require.ErrorContains(t, err, `connection "work-google": connection tokens are not available in this run`)
}
