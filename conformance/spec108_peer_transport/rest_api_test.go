// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// The server creates, lists, and deletes invites, reports join readiness,
// and removes workers through its coordinator, which uses the CA it
// generates. Only the create response contains the invite (Spec 108 §REST
// API, §Secrets in Output).
func TestInviteAPI(t *testing.T) {
	dagu := harness.NewRunner(t)
	services := startStartAll(t, dagu, "DAGU_AUTH_MODE=none")
	defer services.process.Stop()

	code, body := services.call(t, http.MethodPost, "/api/v1/workers/invites", `{"address": "192.168.1.20:50055"}`)
	require.Equal(t, http.StatusCreated, code, body)
	var created struct {
		ID        string `json:"id"`
		Invite    string `json:"invite"`
		ExpiresAt string `json:"expiresAt"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	assert.True(t, strings.HasPrefix(created.Invite, "dagu1."), created.Invite)
	assert.NotEmpty(t, created.ExpiresAt, "a single-use invite expires by default")

	code, body = services.call(t, http.MethodGet, "/api/v1/workers/invites", "")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, `"id":"`+created.ID+`"`)
	assert.Contains(t, body, `"kind":"single_use"`)
	assert.NotContains(t, body, created.Invite)

	code, body = services.call(t, http.MethodGet, "/api/v1/workers/join-readiness", "")
	require.Equal(t, http.StatusOK, code, body)
	var readiness struct {
		Coordinators []struct {
			Report *struct {
				LoopbackOnly bool `json:"loopbackOnly"`
				BuiltinTLS   bool `json:"builtinTLS"`
				Ready        bool `json:"ready"`
			} `json:"report"`
		} `json:"coordinators"`
		LastInviteAddress string `json:"lastInviteAddress"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &readiness))
	require.Len(t, readiness.Coordinators, 1)
	report := readiness.Coordinators[0].Report
	require.NotNil(t, report, body)
	assert.True(t, report.BuiltinTLS)
	assert.True(t, report.LoopbackOnly, "the coordinator listens on 127.0.0.1")
	assert.False(t, report.Ready)
	assert.Equal(t, "192.168.1.20:50055", readiness.LastInviteAddress)
	assert.NotContains(t, body, created.Invite)

	code, body = services.call(t, http.MethodPost, "/api/v1/workers/invites", `{"expiresIn": "soon"}`)
	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Contains(t, body, "expiresIn")

	code, body = services.call(t, http.MethodDelete, "/api/v1/workers/invites/"+created.ID, "")
	assert.Equal(t, http.StatusNoContent, code, body)
	code, body = services.call(t, http.MethodDelete, "/api/v1/workers/invites/"+created.ID, "")
	assert.Equal(t, http.StatusNotFound, code, body)
	assert.NotContains(t, services.process.Output(), created.Invite)

	code, body = services.call(t, http.MethodDelete, "/api/v1/workers/unknown/certificate?runs=stop", "")
	assert.Equal(t, http.StatusNotFound, code, body)
	code, body = services.call(t, http.MethodDelete, "/api/v1/workers/unknown/certificate?runs=later", "")
	assert.Equal(t, http.StatusBadRequest, code, body)
}

// call sends method to path on the server of s with the JSON body, and
// returns the status and the response body.
func (s startAll) call(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(callContext(t), method, "http://"+s.server+path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(data)
}
