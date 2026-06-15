// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/api/v1"
)

type errorResponse struct {
	Code    api.ErrorCode `json:"code"`
	Message string        `json:"message,omitempty"`
}

func WriteErrorResponse(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")

	status := http.StatusInternalServerError
	resp := errorResponse{Code: api.ErrorCodeInternalError}
	if apiErr, ok := err.(*Error); ok {
		status = apiErr.HTTPStatus
		resp.Code = apiErr.Code
		resp.Message = apiErr.Message
	}

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// Error is an error that has an associated HTTP status code.
type Error struct {
	// Code is the error code to return.
	Code api.ErrorCode
	// HTTPStatus is the HTTP status code to return.
	HTTPStatus int
	// Message is the error message to return.
	Message string
}

// Error returns the error message.
func (e Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewAPIError(httpCode int, code api.ErrorCode, err error) *Error {
	apiErr := &Error{
		Code:       code,
		HTTPStatus: httpCode,
	}
	if err != nil {
		apiErr.Message = err.Error()
	}
	return apiErr
}
