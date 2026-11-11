// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The AI and skills routes refuse a JSON body with a field that the schema does
// not have, and a body over the limit, also with strict validation off. The
// error never quotes a value of the body: a value can be a key.
func TestClosedBodyMiddleware(t *testing.T) {
	t.Parallel()
	base, err := loadBaseOpenAPISpec()
	require.NoError(t, err)
	swagger := *base
	swagger.Servers = openapi3.Servers{&openapi3.Server{URL: "/api/v1"}}
	var reached []string
	handler := closedBodyMiddleware(&swagger, "/api/v1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reached = append(reached, string(body))
		w.WriteHeader(http.StatusNoContent)
	}))
	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	const key = "sk-ant-api03-SECRET-VALUE-0123456789"
	good := `{"name":"cle1","preset":"anthropic","protocol":"anthropic","scope":"global","allow":{"workspaces":["*"],"dags":["*"]},"key":"` + key + `"}`
	rec := send(http.MethodPost, "/api/v1/ai/endpoints", good)
	assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Equal(t, []string{good}, reached, "the handler reads the whole body")

	for name, body := range map[string]string{
		"unknown field":        `{"name":"cle1","preset":"anthropic","protocol":"anthropic","scope":"global","allow":{"dags":["*"]},"key":"` + key + `","baseURL":"x"}`,
		"unknown nested field": `{"name":"cle1","preset":"anthropic","protocol":"anthropic","scope":"global","allow":{"dags":["*"],"dag":["x"]},"key":"` + key + `"}`,
		"wrong type":           `{"name":"cle1","preset":"anthropic","protocol":"anthropic","scope":"global","allow":{"dags":["*"]},"key":123,"description":"` + key + `"}`,
	} {
		rec := send(http.MethodPost, "/api/v1/ai/endpoints", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, name)
		assert.Contains(t, rec.Body.String(), `"code":"bad_request"`, name)
		assert.NotContains(t, rec.Body.String(), key, name)
	}
	rec = send(http.MethodPut, "/api/v1/ai/mcp-servers/docs/credentials", `{"tokens":"x"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a misspelled field does not clear the credentials")

	rec = send(http.MethodPut, "/api/v1/skills/demo", `{"content":"`+strings.Repeat("a", maxSkillsRequestBodyBytes)+`"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	// Another route passes as it is.
	reached = nil
	rec = send(http.MethodPost, "/api/v1/other", `{"anything":1}`)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, []string{`{"anything":1}`}, reached)
}
