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
		{http.MethodPost, "/api/v1/skills/demo", `{"a":1}`},
		{http.MethodPut, "/api/v1/skills/demo/unknown", `{"b":2}`},
	} {
		w := send(req.method, req.path, "application/json", req.body)
		assert.Equal(t, http.StatusNoContent, w.Code, "%s %s", req.method, req.path)
	}
	assert.Equal(t, []string{good, `{"anything":1}`, `{"a":1}`, `{"b":2}`}, reached, "the handler reads the whole body")

	w := send(http.MethodPut, "/api/v1/skills/demo/unknown", "application/json", tooLarge)
	assert.Equal(t, http.StatusNoContent, w.Code, "an unknown route gets no 413, whatever its body")
}
