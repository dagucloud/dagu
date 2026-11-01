// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudaccess

import (
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cloudrelay"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
)

const testBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

var testCreds = license.CloudCredentials{CloudURL: "https://console.example", LicenseID: "lic-1", ServerID: "srv-1", HeartbeatSecret: "secret"}

// cloudKey stands in for the key Dagu Cloud signs with.
type cloudKey struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func newCloudKey(t *testing.T) cloudKey {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return cloudKey{public: public, private: private}
}

// assertion signs what Dagu Cloud asserts for a call with body to /mcp, as
// change leaves it.
func (k cloudKey) assertion(t *testing.T, body string, change func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": "https://console.example", "aud": assertionAudience, "use": assertionUse,
		"tid": "tenant-1", "license_id": testCreds.LicenseID, "server_id": testCreds.ServerID,
		"sub": "user-1", "email": "ana@example.com", "cloud_workspace": "acme", "role": "developer",
		"grant": "grant-1", "client": "Claude", "jti": uuid.NewString(),
		"req_sha256": requestDigest(http.MethodPost, mcpPath, []byte(body)),
		"iat":        now.Unix(), "exp": now.Add(time.Minute).Unix(),
	}
	if change != nil {
		change(claims)
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(k.private)
	require.NoError(t, err)
	return token
}

// callFixture serves calls at a remote access level, recording the request
// the MCP handler gets.
type callFixture struct {
	key   cloudKey
	calls *calls
	// served is the request the MCP handler got last.
	served *http.Request
	body   string
}

func newCallFixture(t *testing.T, level config.RemoteAccessLevel) *callFixture {
	t.Helper()
	f := &callFixture{key: newCloudKey(t)}
	store := filemonitor.NewStateStore(filepath.Join(t.TempDir(), "access-settings.json"))
	settings := NewSettings(config.CloudConfig{RemoteAccess: level}, store)
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.served, f.body = r, string(body)
		w.WriteHeader(http.StatusOK)
	})
	creds := func() (license.CloudCredentials, bool) { return testCreds, true }
	f.calls = &calls{settings: settings, credentials: creds, verifier: newVerifier(f.key.public), mcp: mcp}
	return f
}

func (f *callFixture) call(path, body, assertion string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://dagu-cloud-relay"+path, strings.NewReader(body))
	r.Header.Set(cloudrelay.AssertionHeader, assertion)
	w := httptest.NewRecorder()
	f.served = nil
	f.calls.ServeHTTP(w, r)
	return w
}

// A call runs as the user Dagu Cloud vouches for, in the lower of the role
// Dagu Cloud grants and the one the server allows.
func TestACallRunsAsTheUserDaguCloudVouchesFor(t *testing.T) {
	f := newCallFixture(t, config.RemoteAccessOperator)
	w := f.call(mcpPath, testBody, f.key.assertion(t, testBody, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, f.served)
	assert.Equal(t, testBody, f.body)
	assert.Empty(t, f.served.Header.Get(cloudrelay.AssertionHeader), "the assertion goes no further")

	user, ok := auth.UserFromContext(f.served.Context())
	require.True(t, ok)
	assert.Equal(t, "cloud:user-1", user.ID)
	assert.Equal(t, "ana@example.com", user.Username)
	assert.Equal(t, auth.RoleOperator, user.Role)
	assert.Equal(t, auth.AuthProviderCloud, user.AuthProvider)
	source, ok := audit.SourceContextFromContext(f.served.Context())
	require.True(t, ok)
	assert.Equal(t, audit.SourceContext{
		Transport: "cloud_relay", CredentialType: "cloud", CredentialID: "grant-1", CredentialName: "Claude",
		SubjectType: "cloud_user", SubjectID: "cloud:user-1", SubjectName: "ana@example.com", CloudWorkspace: "acme",
	}, *source)

	f = newCallFixture(t, config.RemoteAccessDeveloper)
	f.call(mcpPath, testBody, f.key.assertion(t, testBody, func(c jwt.MapClaims) { c["role"] = "viewer" }))
	require.NotNil(t, f.served)
	user, _ = auth.UserFromContext(f.served.Context())
	assert.Equal(t, auth.RoleViewer, user.Role)
}

func TestCallsWithInvalidAssertionsAreRefused(t *testing.T) {
	f := newCallFixture(t, config.RemoteAccessDeveloper)
	other := newCloudKey(t)
	used := f.key.assertion(t, testBody, nil)
	require.Equal(t, http.StatusOK, f.call(mcpPath, testBody, used).Code)

	for name, token := range map[string]string{
		"used before":     used,
		"another key":     other.assertion(t, testBody, nil),
		"another server":  f.key.assertion(t, testBody, func(c jwt.MapClaims) { c["server_id"] = "srv-2" }),
		"another license": f.key.assertion(t, testBody, func(c jwt.MapClaims) { c["license_id"] = "lic-2" }),
		"a license":       f.key.assertion(t, testBody, func(c jwt.MapClaims) { delete(c, "aud") }),
		"a relay ticket":  f.key.assertion(t, testBody, func(c jwt.MapClaims) { c["aud"] = "dagu-relay" }),
		"a session":       f.key.assertion(t, testBody, func(c jwt.MapClaims) { c["use"] = "session" }),
		"another body":    f.key.assertion(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`, nil),
		"an admin":        f.key.assertion(t, testBody, func(c jwt.MapClaims) { c["role"] = "admin" }),
		"no ID":           f.key.assertion(t, testBody, func(c jwt.MapClaims) { delete(c, "jti") }),
		"no user":         f.key.assertion(t, testBody, func(c jwt.MapClaims) { delete(c, "sub") }),
		"expired": f.key.assertion(t, testBody, func(c jwt.MapClaims) {
			c["iat"], c["exp"] = time.Now().Add(-3*time.Minute).Unix(), time.Now().Add(-2*time.Minute).Unix()
		}),
		"long-lived": f.key.assertion(t, testBody, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(15 * time.Minute).Unix() }),
		"none":       "",
	} {
		w := f.call(mcpPath, testBody, token)
		assert.Equal(t, http.StatusUnauthorized, w.Code, name)
		assert.Nil(t, f.served, name)
	}
}

func TestCallsWhileRemoteAccessIsOff(t *testing.T) {
	f := newCallFixture(t, config.RemoteAccessOff)
	w := f.call(mcpPath, testBody, f.key.assertion(t, testBody, nil))
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Nil(t, f.served)
	var refusal map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
	assert.Equal(t, errorRemoteAccessOff, refusal["error"])
	assert.Contains(t, refusal["message"], "Plan & features")
}

func TestCallsReachOnlyMCP(t *testing.T) {
	f := newCallFixture(t, config.RemoteAccessDeveloper)
	w := f.call("/api/v1/dags", "", f.key.assertion(t, "", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Nil(t, f.served)
}
