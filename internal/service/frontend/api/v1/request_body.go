// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	oapimiddleware "github.com/oapi-codegen/nethttp-middleware"

	api "github.com/dagucloud/dagu/v2/api/v1"
)

// The routes of the skills (Spec 096) check each body against the OpenAPI
// document, also when server.strict_validation is off: a body is bounded, and a
// field that the schema does not have is refused. A typo then gives a 400, not a
// request that silently does less. The check does not depend on the
// Content-Type: the generated handlers decode JSON whatever the header says, so
// a body with another type, or with none, is refused instead.
const maxSkillsRequestBodyBytes = 8 << 20

// closedBodyMiddleware applies the rule above. The error has the shape of each
// other error of the API, and never quotes a value of the body: a value can be a
// key. A path or a method that the document does not have passes on, so the
// router answers it as it answers any other unknown route.
func closedBodyMiddleware(swagger *openapi3.T, mountedAPIPath string) func(http.Handler) http.Handler {
	filterOptions := openapi3filter.Options{
		AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil },
	}
	filterOptions.WithCustomSchemaErrorFunc(func(err *openapi3.SchemaError) string { return err.Reason })
	limited := closedBodyLimitMiddleware(swagger, mountedAPIPath)
	return func(next http.Handler) http.Handler {
		validated := limited(oapimiddleware.OapiRequestValidatorWithOptions(swagger, &oapimiddleware.Options{
			SilenceServersWarning: true,
			Options:               filterOptions,
			ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts oapimiddleware.ErrorHandlerOpts) {
				if opts.MatchedRoute == nil {
					next.ServeHTTP(w, r)
					return
				}
				// The first line of a validation error names the field and the rule.
				message, _, _ := strings.Cut(err.Error(), "\n")
				WriteErrorResponse(w, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: message})
			},
		})(next))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := closedBodyLimit(r, mountedAPIPath); !ok {
				next.ServeHTTP(w, r)
				return
			}
			validated.ServeHTTP(w, r)
		})
	}
}

// closedBodyLimitMiddleware refuses with 413 a body over the limit of the rule
// above, and with 400 a body of a JSON operation that holds more than one JSON
// value, since each decoder of the body stops after the first. It gives the
// next handler the body read into memory. It runs before any other reader of
// the body, so no reader holds more than the limit. A request that no
// operation of the document matches passes unchanged.
func closedBodyLimitMiddleware(swagger *openapi3.T, mountedAPIPath string) func(http.Handler) http.Handler {
	router, err := gorillamux.NewRouter(swagger)
	if err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, ok := closedBodyLimit(r, mountedAPIPath)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			route, _, err := router.FindRoute(r)
			if err != nil {
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
			if takesJSON(route.Operation) && hasTrailingJSON(raw) {
				WriteErrorResponse(w, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: "the request body holds more than one JSON value"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			next.ServeHTTP(w, r)
		})
	}
}

// takesJSON reports whether the operation declares a JSON request body.
func takesJSON(op *openapi3.Operation) bool {
	return op.RequestBody != nil && op.RequestBody.Value != nil && op.RequestBody.Value.Content.Get("application/json") != nil
}

// hasTrailingJSON reports whether raw holds anything but space after its first
// JSON value. A body that does not start with a JSON value is left to the
// schema check.
func hasTrailingJSON(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		return false
	}
	_, err := dec.Token()
	return !errors.Is(err, io.EOF)
}

// closedBodyLimit returns the limit of the body of a request that can carry one
// to the routes of the rule. It returns false for another request.
func closedBodyLimit(r *http.Request, mountedAPIPath string) (int64, bool) {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return 0, false
	}
	relative, ok := strings.CutPrefix(r.URL.Path, strings.TrimSuffix(mountedAPIPath, "/"))
	if !ok {
		return 0, false
	}
	relative = strings.TrimPrefix(relative, "/")
	if relative == "skills" || relative == "skills-download" || strings.HasPrefix(relative, "skills/") {
		return maxSkillsRequestBodyBytes, true
	}
	return 0, false
}
