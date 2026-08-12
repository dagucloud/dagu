// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/humantask"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorizeHumanTaskMutationClassifiesLookupErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   apiv1.ErrorCode
		wantStatus int
	}{
		{name: "missing run", err: dagrun.ErrDAGRunIDNotFound, wantCode: apiv1.ErrorCodeNotFound, wantStatus: http.StatusNotFound},
		{name: "storage failure", err: errors.New("storage unavailable"), wantCode: apiv1.ErrorCodeInternalError, wantStatus: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := lookupErrorDAGRunBackend{err: tc.err}
			apiServer := &API{dagRunRepository: dagrun.NewRepository(store, dagrun.RepositoryOptions{})}

			_, err := apiServer.authorizeHumanTaskMutation(t.Context(), "deploy", "run-1")

			require.Error(t, err)
			code, _, status := apiServer.resolveError(err)
			assert.Equal(t, tc.wantCode, code)
			assert.Equal(t, tc.wantStatus, status)
		})
	}
}

func TestCompleteHumanTaskResumeFailureResponseIsStable(t *testing.T) {
	response, err := completeHumanTaskErrorResponse(t.Context(), &humantask.ResumeError{
		Result: humantask.Result{DAGName: "deploy", DAGRunID: "run-1", StepID: "review"},
		Err:    errors.New("storage endpoint included sensitive diagnostics"),
	})
	require.NoError(t, err)

	typed, ok := response.(*apiv1.CompleteHumanTask503JSONResponse)
	require.True(t, ok)
	assert.Equal(t, apiv1.ErrorCodeHumanTaskResumeFailed, typed.Code)
	assert.Equal(
		t,
		"human-task completion was saved, but the DAG-run could not be queued for resume; retry the same completion request",
		typed.Message,
	)
	require.NotNil(t, typed.Details)
	assert.Equal(t, true, (*typed.Details)["completionStored"])
	assert.Equal(t, true, (*typed.Details)["resumePending"])
}

func TestResumeHumanTaskFailureResponseIsStable(t *testing.T) {
	response, err := resumeHumanTaskErrorResponse(t.Context(), &humantask.ResumeError{
		Result: humantask.Result{DAGName: "deploy", DAGRunID: "run-1"},
		Err:    errors.New("storage endpoint included sensitive diagnostics"),
	})
	require.NoError(t, err)

	typed, ok := response.(*apiv1.ResumeHumanTaskDAGRun503JSONResponse)
	require.True(t, ok)
	assert.Equal(t, apiv1.ErrorCodeHumanTaskResumeFailed, typed.Code)
	assert.Equal(t, "the DAG-run could not be queued for resume; retry the resume request", typed.Message)
	require.NotNil(t, typed.Details)
	assert.Equal(t, true, (*typed.Details)["completionStored"])
	assert.Equal(t, true, (*typed.Details)["resumePending"])
}

func TestHumanTaskInputMiddlewarePreservesValidatedBody(t *testing.T) {
	const raw = `{"count":9007199254740993}`
	originalBody := &trackingReadCloser{Reader: strings.NewReader(raw)}
	called := false
	handler := humanTaskInputMiddleware("/base/api/v1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		input, ok := r.Context().Value(humanTaskInputContextKey{}).(humantask.Input)
		require.True(t, ok)
		assert.Equal(t, json.Number("9007199254740993"), input.Values["count"])
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, raw, string(body))
		assert.Equal(t, int64(len(raw)), r.ContentLength)
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(
		http.MethodPost,
		"/base/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete",
		originalBody,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.True(t, called)
	assert.True(t, originalBody.closed)
	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestHumanTaskInputMiddlewareRejectsAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{
		`{} {}`,
		`{"nested":{"confirmed":true,"confirmed":false}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			called := false
			handler := humanTaskInputMiddleware("/api/v1")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete",
				strings.NewReader(raw),
			)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.False(t, called)
			assert.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
}

func TestHumanTaskInputMiddlewareRejectsOversizedBody(t *testing.T) {
	called := false
	handler := humanTaskInputMiddlewareWithLimit("/api/v1", 4)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete",
		strings.NewReader(`{"confirmed":true}`),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.False(t, called)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	var apiError struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &apiError))
	assert.Equal(t, "payload_too_large", apiError.Code)
}

func TestHumanTaskInputMiddlewareValidatesBeforeRemoteProxy(t *testing.T) {
	handler := humanTaskInputMiddleware("/api/v1")(
		WithRemoteNode(nil, "/api/v1")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete?remoteNode=edge",
		strings.NewReader(`{"confirmed":true,"confirmed":false}`),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestHumanTaskInputMiddlewareIgnoresOtherRoutes(t *testing.T) {
	called := false
	handler := humanTaskInputMiddleware("/api/v1")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/dag-runs/deploy/run-1/retry", strings.NewReader(`not json`))

	handler.ServeHTTP(httptest.NewRecorder(), request)

	assert.True(t, called)
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

type lookupErrorDAGRunBackend struct {
	testutil.DAGRunBackendStub
	err error
}

func (s lookupErrorDAGRunBackend) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	return nil, s.err
}

func (b *trackingReadCloser) Close() error {
	b.closed = true
	return nil
}
