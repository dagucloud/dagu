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

// A skills route refuses a JSON body with a field that the schema does not
// have, a value of the wrong type and a body over the limit, whatever its
// Content-Type: the generated handlers decode JSON whatever the header says.
// The error never quotes a value of the body. Other routes, and a path or a
// method that the document does not have, pass unchanged.
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
	send := func(method, path, contentType, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	const value = "secret-looking-value-0123456789"
	tooLarge := `{"content":"` + strings.Repeat("a", maxSkillsRequestBodyBytes) + `"}`

	for _, contentType := range []string{"application/json", "", "text/plain"} {
		for body, status := range map[string]int{
			`{"content":"x","contents":"` + value + `"}`: http.StatusBadRequest,
			`{"content":123,"other":"` + value + `"}`:    http.StatusBadRequest,
			`{}`:     http.StatusBadRequest,
			tooLarge: http.StatusRequestEntityTooLarge,
		} {
			w := send(http.MethodPut, "/api/v1/skills/demo", contentType, body)
			assert.Equal(t, status, w.Code, "Content-Type %q, body %.40s", contentType, body)
			assert.NotContains(t, w.Body.String(), value)
		}
	}
	require.Empty(t, reached, "a refused body never reaches the handler")

	good := `{"content":"` + value + `"}`
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/skills/demo", good},
		{http.MethodPost, "/api/v1/other", `{"anything":1}`},
		{http.MethodPost, "/api/v1/auth/login", `{"username":"","password":""}`},
		{http.MethodPost, "/api/v1/skills/demo", `{"a":1}`},
		{http.MethodPut, "/api/v1/skills/demo/unknown", `{"b":2}`},
	} {
		w := send(req.method, req.path, "application/json", req.body)
		assert.Equal(t, http.StatusNoContent, w.Code, "%s %s", req.method, req.path)
	}
	assert.Equal(t, []string{good, `{"anything":1}`, `{"username":"","password":""}`, `{"a":1}`, `{"b":2}`}, reached, "the handler reads the whole body")

	w := send(http.MethodPut, "/api/v1/skills/demo/unknown", "application/json", tooLarge)
	assert.Equal(t, http.StatusNoContent, w.Code, "an unknown route gets no 413, whatever its body")
}

const closedBodyTestKey = "sk-ant-api03-SECRET-VALUE-0123456789"

// closedBodyRecorder serves the closed-body middleware in front of a handler
// that records each body it reads.
type closedBodyRecorder struct {
	handler http.Handler
	reached []string
}

func newClosedBodyRecorder(t *testing.T) *closedBodyRecorder {
	t.Helper()
	base, err := loadBaseOpenAPISpec()
	require.NoError(t, err)
	swagger := *base
	swagger.Servers = openapi3.Servers{&openapi3.Server{URL: "/api/v1"}}
	rec := &closedBodyRecorder{}
	rec.handler = closedBodyMiddleware(&swagger, "/api/v1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.reached = append(rec.reached, string(body))
		w.WriteHeader(http.StatusNoContent)
	}))
	return rec
}

// send serves one request. An empty contentType sends no Content-Type header.
func (rec *closedBodyRecorder) send(method, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	rec.handler.ServeHTTP(w, req)
	return w
}

// The AI and skills routes refuse a JSON body with a field that the schema does
// not have, and a body over the limit, also with strict validation off. The
// error never quotes a value of the body: a value can be a key.
func TestClosedBodyMiddlewareAIRoutes(t *testing.T) {
	t.Parallel()
	rec := newClosedBodyRecorder(t)

	good := `{"name":"cle1","preset":"anthropic","protocol":"anthropic","scope":"global","allow":{"workspaces":["*"],"dags":["*"]},"key":"` + closedBodyTestKey + `"}`
	w := rec.send(http.MethodPost, "/api/v1/ai/endpoints", "application/json", good)
	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.Equal(t, []string{good}, rec.reached, "the handler reads the whole body")

	for name, body := range map[string]string{
		"unknown field":        `{"name":"cle1","preset":"anthropic","protocol":"anthropic","scope":"global","allow":{"dags":["*"]},"key":"` + closedBodyTestKey + `","baseURL":"x"}`,
		"unknown nested field": `{"name":"cle1","preset":"anthropic","protocol":"anthropic","scope":"global","allow":{"dags":["*"],"dag":["x"]},"key":"` + closedBodyTestKey + `"}`,
		"wrong type":           `{"name":"cle1","preset":"anthropic","protocol":"anthropic","scope":"global","allow":{"dags":["*"]},"key":123,"description":"` + closedBodyTestKey + `"}`,
	} {
		w := rec.send(http.MethodPost, "/api/v1/ai/endpoints", "application/json", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, name)
		assert.Contains(t, w.Body.String(), `"code":"bad_request"`, name)
		assert.NotContains(t, w.Body.String(), closedBodyTestKey, name)
	}
	w = rec.send(http.MethodPut, "/api/v1/ai/mcp-servers/docs/credentials", "application/json", `{"tokens":"x"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "a misspelled field does not clear the credentials")

	w = rec.send(http.MethodPost, "/api/v1/ai/endpoints", "application/json", `{"description":"`+strings.Repeat("a", maxAIRequestBodyBytes)+`"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	w = rec.send(http.MethodPut, "/api/v1/skills/demo", "application/json", `{"content":"`+strings.Repeat("a", maxSkillsRequestBodyBytes)+`"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	// Another route passes as it is.
	rec.reached = nil
	w = rec.send(http.MethodPost, "/api/v1/other", "application/json", `{"anything":1}`)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, []string{`{"anything":1}`}, rec.reached)
}

// The generated handlers decode a JSON body whatever its Content-Type says, so a
// body with no type or another type must not skip the check.
func TestClosedBodyMiddlewareChecksEveryContentType(t *testing.T) {
	t.Parallel()
	rec := newClosedBodyRecorder(t)

	for _, contentType := range []string{"", "text/plain"} {
		w := rec.send(http.MethodPut, "/api/v1/ai/mcp-servers/docs/credentials", contentType, `{"tokens":"x"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "Content-Type %q", contentType)
		w = rec.send(http.MethodPut, "/api/v1/ai/mcp-servers/docs/credentials", contentType, `{"description":"`+strings.Repeat("a", maxAIRequestBodyBytes)+`"}`)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "Content-Type %q", contentType)
	}
	assert.Empty(t, rec.reached)

	w := rec.send(http.MethodPost, "/api/v1/ai/endpoints/cle1/models/reload", "", "")
	assert.Equal(t, http.StatusNoContent, w.Code, "a route with no body needs no Content-Type")
}

// A path or a method under /ai/ that the document does not have reaches the
// router, which answers 404 or 405, not the 400 of a body check.
func TestClosedBodyMiddlewarePassesUnknownRoutes(t *testing.T) {
	t.Parallel()
	rec := newClosedBodyRecorder(t)

	w := rec.send(http.MethodPost, "/api/v1/ai/unknown", "application/json", `{"a":1}`)
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = rec.send(http.MethodPut, "/api/v1/ai/endpoints", "application/json", `{"b":2}`)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, []string{`{"a":1}`, `{"b":2}`}, rec.reached)
}
