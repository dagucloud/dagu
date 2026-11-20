// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	oapimiddleware "github.com/oapi-codegen/nethttp-middleware"

	api "github.com/dagucloud/dagu/v2/api/v1"
)

// The routes of the AI endpoints (Spec 093), the MCP server entries (Spec 095) and
// the skills (Spec 096) check each JSON body against the OpenAPI document, also
// when server.strict_validation is off: a body is bounded, and a field that the
// schema does not have is refused. A typo then gives a 400, not a request that
// silently does less, such as a set of credentials with no value.
const (
	maxAIRequestBodyBytes     = 1 << 20
	maxSkillsRequestBodyBytes = 8 << 20
)

// closedBodyMiddleware applies the rule above. The error has the shape of each
// other error of the API, and never quotes a value of the body: a value can be a
// key.
func closedBodyMiddleware(swagger *openapi3.T, mountedAPIPath string) func(http.Handler) http.Handler {
	filterOptions := openapi3filter.Options{
		AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil },
	}
	filterOptions.WithCustomSchemaErrorFunc(func(err *openapi3.SchemaError) string { return err.Reason })
	validate := oapimiddleware.OapiRequestValidatorWithOptions(swagger, &oapimiddleware.Options{
		SilenceServersWarning: true,
		Options:               filterOptions,
		ErrorHandler: func(w http.ResponseWriter, message string, _ int) {
			WriteErrorResponse(w, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: message})
		},
	})
	return func(next http.Handler) http.Handler {
		validated := validate(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, ok := closedBodyLimit(r, mountedAPIPath)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
			if err != nil {
				if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
					WriteErrorResponse(w, &Error{
						HTTPStatus: http.StatusRequestEntityTooLarge,
						Code:       api.ErrorCodePayloadTooLarge,
						Message:    fmt.Sprintf("the request body exceeds the %d-byte limit", limit),
					})
					return
				}
				WriteErrorResponse(w, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: "failed to read the request body"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			validated.ServeHTTP(w, r)
		})
	}
}

// closedBodyLimit returns the limit of the body of a request with a JSON body
// to the routes of the rule. It returns false for another request.
func closedBodyLimit(r *http.Request, mountedAPIPath string) (int64, bool) {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return 0, false
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		return 0, false
	}
	relative, ok := strings.CutPrefix(r.URL.Path, strings.TrimSuffix(mountedAPIPath, "/"))
	if !ok {
		return 0, false
	}
	relative = strings.TrimPrefix(relative, "/")
	switch {
	case strings.HasPrefix(relative, "ai/"):
		return maxAIRequestBodyBytes, true
	case relative == "skills" || strings.HasPrefix(relative, "skills/"):
		return maxSkillsRequestBodyBytes, true
	}
	return 0, false
}
