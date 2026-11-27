// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"encoding/json"
	"net/http"
)

// StreamError converts the "error" field of a stream chunk to an error. It
// returns nil when the chunk carries no error. The field is an object with a
// message and, for some servers, a numeric HTTP status code, or a bare string.
// An error without a numeric status code is retryable.
func StreamError(provider string, raw json.RawMessage) error {
	switch string(raw) {
	case "", "null", `""`:
		return nil
	}

	var message string
	var status int
	if json.Unmarshal(raw, &message) != nil {
		var detail struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &detail)
		message = detail.Message
		if code, ok := detail.Code.(float64); ok && code >= http.StatusBadRequest && code < 600 {
			status = int(code)
		}
	}
	if message == "" {
		message = string(raw)
	}
	apiErr := NewAPIError(provider, status, message)
	if status == 0 {
		// The provider failed after accepting the request, so a failure
		// it does not classify is treated as transient.
		apiErr.Retryable = true
	}
	return apiErr
}
