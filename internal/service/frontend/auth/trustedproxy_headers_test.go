// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTrustedProxyIdentity(t *testing.T) {
	t.Run("parses opaque user and CSV groups", func(t *testing.T) {
		headers := http.Header{}
		headers.Set("X-Proxy-User", "  Alice,Example\t")
		headers.Add("X-Proxy-Groups", `team-a,"team,with,comma"`)
		headers.Add("X-Proxy-Groups", "team-b,team-a")

		identity, err := parseTrustedProxyIdentity(headers, "x-proxy-user", "x-proxy-groups", true)
		require.NoError(t, err)
		assert.Equal(t, "Alice,Example", identity.user)
		assert.Equal(t, []string{"team-a", "team,with,comma", "team-b"}, identity.groups)
	})

	t.Run("allows absent optional groups", func(t *testing.T) {
		headers := http.Header{"X-Proxy-User": {"alice"}}
		identity, err := parseTrustedProxyIdentity(headers, "X-Proxy-User", "", false)
		require.NoError(t, err)
		assert.Empty(t, identity.groups)
	})

	t.Run("distinguishes missing and empty groups", func(t *testing.T) {
		headers := http.Header{"X-Proxy-User": {"alice"}}
		_, err := parseTrustedProxyIdentity(headers, "X-Proxy-User", "X-Proxy-Groups", true)
		assert.ErrorIs(t, err, errTrustedProxyIdentityUnavailable)

		headers["X-Proxy-Groups"] = []string{""}
		identity, err := parseTrustedProxyIdentity(headers, "X-Proxy-User", "X-Proxy-Groups", true)
		require.NoError(t, err)
		assert.Empty(t, identity.groups)
	})
}

func TestParseTrustedProxyUser(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		want    string
		wantErr error
	}{
		{name: "missing", wantErr: errTrustedProxyIdentityUnavailable},
		{name: "empty", values: []string{" \t"}, wantErr: errTrustedProxyIdentityUnavailable},
		{name: "multiple", values: []string{"alice", "bob"}, wantErr: errTrustedProxyIdentityInvalid},
		{name: "opaque comma", values: []string{"alice,bob"}, want: "alice,bob"},
		{name: "optional whitespace", values: []string{"\t alice \t"}, want: "alice"},
		{name: "internal tab", values: []string{"alice\tbob"}, wantErr: errTrustedProxyIdentityInvalid},
		{name: "control", values: []string{"alice\x00"}, wantErr: errTrustedProxyIdentityInvalid},
		{name: "invalid utf8", values: []string{string([]byte{0xff})}, wantErr: errTrustedProxyIdentityInvalid},
		{name: "too large", values: []string{strings.Repeat("a", trustedProxyUserMaxBytes+1)}, wantErr: errTrustedProxyIdentityInvalid},
		{name: "at limit", values: []string{strings.Repeat("a", trustedProxyUserMaxBytes)}, want: strings.Repeat("a", trustedProxyUserMaxBytes)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTrustedProxyUser(tt.values)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseTrustedProxyGroupsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{name: "invalid CSV", values: []string{`"unterminated`}},
		{name: "control", values: []string{"group\x7f"}},
		{name: "invalid utf8", values: []string{string([]byte{0xff})}},
		{name: "group too large", values: []string{strings.Repeat("g", trustedProxyGroupMaxBytes+1)}},
		{name: "raw input too large", values: []string{strings.Repeat("g", trustedProxyGroupsMaxRawBytes+1)}},
		{name: "too many entries", values: []string{strings.Repeat("same,", trustedProxyGroupsMaxCount) + "same"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTrustedProxyGroups(tt.values)
			assert.ErrorIs(t, err, errTrustedProxyIdentityInvalid)
		})
	}
}

func FuzzParseTrustedProxyUser(f *testing.F) {
	f.Add("alice")
	f.Add(" alice,example ")
	f.Add("alice\x00")
	f.Fuzz(func(t *testing.T, value string) {
		got, err := parseTrustedProxyUser([]string{value})
		if err != nil {
			assert.True(t, errors.Is(err, errTrustedProxyIdentityUnavailable) || errors.Is(err, errTrustedProxyIdentityInvalid))
			return
		}
		assert.NotEmpty(t, got)
		assert.LessOrEqual(t, len(got), trustedProxyUserMaxBytes)
		assert.True(t, utf8.ValidString(got))
	})
}

func FuzzParseTrustedProxyGroups(f *testing.F) {
	f.Add("team-a,team-b")
	f.Add(`team-a,"team,with,comma"`)
	f.Add(`"unterminated`)
	f.Fuzz(func(t *testing.T, value string) {
		groups, err := parseTrustedProxyGroups([]string{value})
		if err != nil {
			assert.ErrorIs(t, err, errTrustedProxyIdentityInvalid)
			return
		}
		assert.LessOrEqual(t, len(groups), trustedProxyGroupsMaxCount)
		for _, group := range groups {
			assert.NotEmpty(t, group)
			assert.LessOrEqual(t, len(group), trustedProxyGroupMaxBytes)
			assert.True(t, utf8.ValidString(group))
		}
	})
}
