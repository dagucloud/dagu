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

const closedBodyTestValue = "secret-looking-value-0123456789"

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

// A skills route refuses a JSON body with a field that the schema does not
// have, a value of the wrong type and a body over the limit. The error never
// quotes a value of the body.
func TestClosedBodyMiddleware(t *testing.T) {
	t.Parallel()
	rec := newClosedBodyRecorder(t)

	good := `{"content":"` + closedBodyTestValue + `"}`
	w := rec.send(http.MethodPut, "/api/v1/skills/demo", "application/json", good)
	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.Equal(t, []string{good}, rec.reached, "the handler reads the whole body")

	for name, body := range map[string]string{
		"unknown field": `{"content":"x","contents":"` + closedBodyTestValue + `"}`,
		"wrong type":    `{"content":123,"other":"` + closedBodyTestValue + `"}`,
		"no content":    `{}`,
	} {
		w := rec.send(http.MethodPut, "/api/v1/skills/demo", "application/json", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, name)
		assert.Contains(t, w.Body.String(), `"code":"bad_request"`, name)
		assert.NotContains(t, w.Body.String(), closedBodyTestValue, name)
	}

	w = rec.send(http.MethodPut, "/api/v1/skills/demo", "application/json", `{"content":"`+strings.Repeat("a", maxSkillsRequestBodyBytes)+`"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Len(t, rec.reached, 1, "a refused body never reaches the handler")
}

// The generated handlers decode a JSON body whatever its Content-Type says, so a
// body with no type or another type must not skip the check.
func TestClosedBodyMiddlewareChecksEveryContentType(t *testing.T) {
	t.Parallel()
	rec := newClosedBodyRecorder(t)

	for _, contentType := range []string{"", "text/plain"} {
		w := rec.send(http.MethodPut, "/api/v1/skills/demo", contentType, `{"content":"x","contents":"y"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "Content-Type %q", contentType)
		w = rec.send(http.MethodPut, "/api/v1/skills/demo", contentType, `{"content":"`+strings.Repeat("a", maxSkillsRequestBodyBytes)+`"}`)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "Content-Type %q", contentType)
	}
	assert.Empty(t, rec.reached)
}

// A route outside the skills, and a path or a method under /skills that the
// document does not have, reach the router unchanged, which answers them as any
// other route.
func TestClosedBodyMiddlewarePassesOtherRoutes(t *testing.T) {
	t.Parallel()
	rec := newClosedBodyRecorder(t)

	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/other", `{"anything":1}`},
		{http.MethodPost, "/api/v1/skills/demo", `{"a":1}`},
		{http.MethodPut, "/api/v1/skills/demo/unknown", `{"b":2}`},
		{http.MethodDelete, "/api/v1/skills/demo", ""},
	} {
		w := rec.send(req.method, req.path, "application/json", req.body)
		assert.Equal(t, http.StatusNoContent, w.Code, "%s %s", req.method, req.path)
	}
	assert.Equal(t, []string{`{"anything":1}`, `{"a":1}`, `{"b":2}`, ""}, rec.reached)
}
