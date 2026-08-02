// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for common LLM error conditions.
var (
	// ErrNoAPIKey indicates that no API key was provided.
	ErrNoAPIKey = errors.New("no API key provided")
	// ErrInvalidProvider indicates an unknown or invalid provider type.
	ErrInvalidProvider = errors.New("invalid provider")
	// ErrRateLimited indicates the API rate limit has been exceeded.
	ErrRateLimited = errors.New("rate limited")
	// ErrContextTooLong indicates the input exceeds the model's context limit.
	ErrContextTooLong = errors.New("context length exceeded")
	// ErrModelNotFound indicates the requested model does not exist.
	ErrModelNotFound = errors.New("model not found")
	// ErrInvalidRequest indicates a malformed request.
	ErrInvalidRequest = errors.New("invalid request")
	// ErrUnauthorized indicates invalid or missing authentication.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrServerError indicates a server-side error.
	ErrServerError = errors.New("server error")
	// ErrTimeout indicates the request timed out.
	ErrTimeout = errors.New("request timeout")
	// ErrStreamClosed indicates the stream was unexpectedly closed.
	ErrStreamClosed = errors.New("stream closed unexpectedly")
)

// APIError represents an error response from an LLM API.
type APIError struct {
	// Provider is the name of the provider that returned the error.
	Provider string
	// StatusCode is the HTTP status code returned.
	StatusCode int
	// Message is the error message from the API.
	Message string
	// Retryable indicates whether the request can be retried.
	Retryable bool
	// Err is the underlying error, if any.
	Err error
}

// Error returns the error message.
func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s API error (status %d): %s: %v", e.Provider, e.StatusCode, e.Message, e.Err)
	}
	return fmt.Sprintf("%s API error (status %d): %s", e.Provider, e.StatusCode, e.Message)
}

// Unwrap returns the underlying error.
func (e *APIError) Unwrap() error {
	return e.Err
}

// NewAPIError creates a new APIError with the given parameters.
func NewAPIError(provider string, statusCode int, message string) *APIError {
	return &APIError{
		Provider:   provider,
		StatusCode: statusCode,
		Message:    message,
		Retryable:  isRetryableStatusCode(statusCode),
		Err:        classifyAPIError(statusCode, message),
	}
}

func classifyAPIError(statusCode int, body string) error {
	if statusCode != 400 && statusCode != 413 && statusCode != 422 {
		return nil
	}

	codes, messages := apiErrorDetails(body)
	for _, code := range codes {
		switch normalizeAPIErrorText(code) {
		case "context_length_exceeded", "context_window_exceeded", "prompt_too_long", "input_too_long":
			return ErrContextTooLong
		}
	}
	for _, message := range messages {
		text := normalizeAPIErrorText(message)
		switch {
		case strings.Contains(text, "context length exceeded"),
			strings.Contains(text, "maximum context length"),
			strings.Contains(text, "context window") && strings.Contains(text, "exceed"),
			strings.Contains(text, "prompt is too long"),
			strings.Contains(text, "input is too long"),
			strings.Contains(text, "input token count") && strings.Contains(text, "exceed") && strings.Contains(text, "maximum"),
			strings.Contains(text, "request exceeds the available context"):
			return ErrContextTooLong
		}
	}
	return nil
}

func apiErrorDetails(body string) (codes []string, messages []string) {
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return nil, []string{body}
	}

	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, nested := range typed {
				switch text := nested.(type) {
				case string:
					switch strings.ToLower(key) {
					case "code", "type", "status":
						codes = append(codes, text)
					case "message", "error", "detail", "details", "raw":
						messages = append(messages, text)
					}
				default:
					visit(nested)
				}
			}
		case []any:
			for _, nested := range typed {
				visit(nested)
			}
		}
	}
	visit(value)
	if len(messages) == 0 {
		messages = append(messages, body)
	}
	return codes, messages
}

func normalizeAPIErrorText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// isRetryableStatusCode determines if an HTTP status code indicates a retryable error.
func isRetryableStatusCode(code int) bool {
	switch code {
	case 429: // Too Many Requests (rate limited)
		return true
	case 500, 502, 503, 504: // Server errors
		return true
	default:
		return false
	}
}

// IsRetryable returns true if the error is retryable.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check for APIError
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable
	}

	// Check for known retryable sentinel errors
	if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrServerError) || errors.Is(err, ErrTimeout) {
		return true
	}

	if isRetryableTransportError(err) {
		return true
	}

	return false
}

// IsAuthError returns true if the error is an authentication error.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 401 || apiErr.StatusCode == 403
	}

	return errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrNoAPIKey)
}

// IsRateLimitError returns true if the error is a rate limit error.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 429
	}

	return errors.Is(err, ErrRateLimited)
}

// WrapError wraps an error with provider context.
func WrapError(provider string, err error) error {
	if err == nil {
		return nil
	}

	// Don't double-wrap APIErrors
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return err
	}

	return &APIError{
		Provider:  provider,
		Message:   err.Error(),
		Retryable: IsRetryable(err) || errors.Is(err, context.Canceled),
		Err:       err,
	}
}
