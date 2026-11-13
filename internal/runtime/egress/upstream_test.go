// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress_test

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The URL of an upstream has the scheme http or https, a host, no user
// information, no query and no fragment. The port is the explicit port, or the
// default port of the scheme.
func TestNewUpstreamURL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		url  string
		host string
		port int
	}{
		{"https default port", "https://api.example.com", "api.example.com", 443},
		{"http default port", "http://api.example.com/base", "api.example.com", 80},
		{"explicit port", "https://API.Example.com:8443/base/", "api.example.com", 8443},
		{"address", "http://127.0.0.1:9000", "127.0.0.1", 9000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			upstream, err := egress.NewUpstream(tc.url, egress.AuthXAPIKey, "k")
			require.NoError(t, err)
			assert.Equal(t, tc.host, upstream.Host())
			assert.Equal(t, tc.port, upstream.Port())
		})
	}

	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{"empty", "", "expected the scheme http or https"},
		{"no scheme", "api.example.com", "expected the scheme http or https"},
		{"other scheme", "ftp://api.example.com", "expected the scheme http or https"},
		{"no host", "https:///v1", "expected a URL with a host"},
		{"opaque", "https:api.example.com", "expected a URL with a host"},
		{"query", "https://api.example.com/v1?a=b", "no query and no fragment"},
		{"empty query", "https://api.example.com/v1?", "no query and no fragment"},
		{"fragment", "https://api.example.com/v1#x", "no query and no fragment"},
		{"empty fragment", "https://api.example.com/v1#", "no query and no fragment"},
		{"port zero", "https://api.example.com:0", "expected a port from 1 to 65535"},
		{"port too large", "https://api.example.com:70000", "expected a port from 1 to 65535"},
		{"not a URL", "https://api.example.com:port", "expected an http or https URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := egress.NewUpstream(tc.url, egress.AuthXAPIKey, "k")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// A URL with user information is refused, and the error does not repeat the
// credential.
func TestNewUpstreamRefusesUserInformation(t *testing.T) {
	t.Parallel()

	_, err := egress.NewUpstream("https://user:hunter2@api.example.com", egress.AuthXAPIKey, "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no user information")
	assert.NotContains(t, err.Error(), "hunter2")
}

// The forms are x-api-key and bearer. Another name is an error that names both.
func TestUpstreamAuthForms(t *testing.T) {
	t.Parallel()

	for _, form := range []string{"x-api-key", "bearer"} {
		auth, err := egress.ParseUpstreamAuth(form)
		require.NoError(t, err)
		assert.Equal(t, form, string(auth))
	}
	for _, form := range []string{"", "Bearer", "basic"} {
		_, err := egress.ParseUpstreamAuth(form)
		require.Error(t, err, form)
		assert.Contains(t, err.Error(), `"x-api-key"`)
		assert.Contains(t, err.Error(), `"bearer"`)
		assert.Contains(t, err.Error(), fmt.Sprintf("%q", form))
	}
	_, err := egress.NewUpstream("https://api.example.com", egress.UpstreamAuth("basic"), "k")
	require.Error(t, err)
}

// A key is a non-empty value with no control character, so that it is a valid
// header value. The error gives the position and never the key.
func TestValidateUpstreamKey(t *testing.T) {
	t.Parallel()

	require.NoError(t, egress.ValidateUpstreamKey("sk-ant-api03-abc DEF/+="))
	require.Error(t, egress.ValidateUpstreamKey(""))
	for _, key := range []string{"sk-secret\nsecond", "sk-secret\r", "sk-\x00secret", "sk-secret\x7f", "\tsk-secret"} {
		err := egress.ValidateUpstreamKey(key)
		require.Error(t, err, "%q", key)
		assert.Contains(t, err.Error(), "control character at byte")
		assert.NotContains(t, err.Error(), "secret")
		_, err = egress.NewUpstream("https://api.example.com", egress.AuthBearer, key)
		require.Error(t, err, "%q", key)
		assert.NotContains(t, err.Error(), "secret")
	}
}

// Each text form of an upstream hides the key.
func TestUpstreamHidesTheKey(t *testing.T) {
	t.Parallel()

	upstream, err := egress.NewUpstream("https://api.example.com/v1", egress.AuthBearer, "sk-secret-value")
	require.NoError(t, err)
	for _, text := range []string{
		fmt.Sprintf("%v", upstream), fmt.Sprintf("%+v", upstream), fmt.Sprintf("%#v", upstream),
		fmt.Sprintf("<%s>", upstream), fmt.Sprintf("%v", *upstream), fmt.Sprintf("%+v", *upstream),
		fmt.Sprintf("%#v", *upstream), fmt.Sprint(upstream), fmt.Sprint(*upstream),
	} {
		assert.NotContains(t, text, "secret")
		assert.Contains(t, text, "https://api.example.com/v1")
	}
}
