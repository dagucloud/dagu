// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

func TestValidateName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"gmail", "work-mail", "0", strings.Repeat("a", 63)} {
		assert.NoError(t, connection.ValidateName(name), name)
	}
	for _, name := range []string{"", "Gmail", "-mail", "work_mail", "a/b", "a.b", strings.Repeat("a", 64)} {
		assert.ErrorIs(t, connection.ValidateName(name), connection.ErrInvalid, name)
	}
}

func TestKeyValidate(t *testing.T) {
	t.Parallel()
	for _, ws := range []string{"", secret.GlobalWorkspace, "payments"} {
		assert.NoError(t, connection.Key{Workspace: ws, Name: "mail"}.Validate(), ws)
	}
	for _, ws := range []string{"all", "../x", ".other"} {
		assert.ErrorIs(t, connection.Key{Workspace: ws, Name: "mail"}.Validate(), connection.ErrInvalid, ws)
	}
}

func TestConnectionValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		modify func(*connection.Connection)
		want   error
	}{
		{name: "Valid", modify: func(*connection.Connection) {}},
		{name: "CloudHolder", modify: func(c *connection.Connection) { c.Holder = connection.HolderCloud }, want: connection.ErrReserved},
		{name: "NoApp", modify: func(c *connection.Connection) { c.App = "" }, want: connection.ErrInvalid},
		{name: "UnknownGrant", modify: func(c *connection.Connection) { c.Grant = "implicit" }, want: connection.ErrInvalid},
		{name: "UnknownAccess", modify: func(c *connection.Connection) { c.Services[0].Access = "admin" }, want: connection.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conn := &connection.Connection{
				Workspace: "payments",
				Name:      "mail",
				Provider:  connection.ProviderGoogle,
				App:       "app-1",
				Grant:     connection.GrantAuthorizationCode,
				Services:  []connection.Service{{Name: "gmail", Access: connection.AccessRead}},
				Holder:    connection.HolderLocal,
			}
			tt.modify(conn)
			err := conn.Validate()
			if tt.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.want)
		})
	}
}

func TestAppValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		modify func(*connection.App)
		want   error
	}{
		{name: "Valid", modify: func(*connection.App) {}},
		{name: "HostedKind", modify: func(a *connection.App) { a.Kind = connection.AppKindHosted }, want: connection.ErrReserved},
		{name: "PublicWithSecret", modify: func(a *connection.App) { a.ClientType = connection.ClientTypePublic }, want: connection.ErrInvalid},
		{
			name: "ServiceAccountWithoutKey",
			modify: func(a *connection.App) {
				a.ClientType, a.ClientSecret = connection.ClientTypeServiceAccount, ""
			},
			want: connection.ErrInvalid,
		},
		{name: "TenantOutsideMicrosoft", modify: func(a *connection.App) { a.Tenant = "common" }, want: connection.ErrInvalid},
		{name: "UnknownStatus", modify: func(a *connection.App) { a.Status = "" }, want: connection.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			app := &connection.App{
				Name:         "Google",
				Provider:     connection.ProviderGoogle,
				Kind:         connection.AppKindBYO,
				ClientID:     "client-id",
				ClientSecret: "client-secret",
				ClientType:   connection.ClientTypeWeb,
				Status:       connection.AppStatusOK,
			}
			tt.modify(app)
			err := app.Validate()
			if tt.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.want)
		})
	}
}
