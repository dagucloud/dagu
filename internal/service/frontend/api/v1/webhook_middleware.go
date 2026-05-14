// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
)

// contextKey is a private type for context keys in this package.
type contextKey string

const (
	// rawBodyContextKey stores the raw request body bytes in context.
	rawBodyContextKey contextKey = "webhook_raw_body"
	// requestHeadersContextKey stores a clone of the webhook request headers.
	requestHeadersContextKey contextKey = "webhook_request_headers"
)

// WebhookRequestContextMiddleware is a chi middleware that captures the raw
// request body and normalized request headers into the request context before
// the generated strict handler consumes them. This allows the webhook handler
// to fall back to the full raw body when the structured "payload" field is not
// present in the request and to forward selected request headers into runtime.
//
// The middleware only activates for POST requests to webhook trigger paths
// (paths containing "/webhooks/" with a fileName segment after it).
func WebhookRequestContextMiddleware(maxPayloadSize int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || !isWebhookTriggerPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			ctx := withRequestHeaders(r.Context(), r.Header)

			if r.Body == nil || r.Body == http.NoBody {
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Read the body with the same size limit used for payload validation.
			rawBody, err := io.ReadAll(io.LimitReader(r.Body, int64(maxPayloadSize)+1))
			if err != nil {
				http.Error(w, "failed to read request body", http.StatusBadRequest)
				return
			}

			// Keep oversized payloads on the normal handler path so the API layer
			// can return a structured 413 before auth/HMAC verification instead of
			// handing truncated JSON to the generated strict decoder.
			if len(rawBody) > maxPayloadSize {
				r.Body = io.NopCloser(bytes.NewReader([]byte("{}")))
				next.ServeHTTP(w, r.WithContext(withRawBody(ctx, rawBody)))
				return
			}

			// Replace r.Body so the generated code can still read it.
			r.Body = io.NopCloser(bytes.NewReader(rawBody))

			next.ServeHTTP(w, r.WithContext(withRawBody(ctx, rawBody)))
		})
	}
}

// rawBodyFromContext retrieves the raw request body bytes stored by
// WebhookRequestContextMiddleware. Returns nil if not present.
func rawBodyFromContext(ctx context.Context) []byte {
	raw, _ := ctx.Value(rawBodyContextKey).([]byte)
	return raw
}

// withRawBody returns a context with the raw body attached (for testing).
func withRawBody(ctx context.Context, body []byte) context.Context {
	return context.WithValue(ctx, rawBodyContextKey, body)
}

// requestHeadersFromContext retrieves the request headers stored by
// WebhookRequestContextMiddleware. Returns nil if not present.
func requestHeadersFromContext(ctx context.Context) http.Header {
	headers, _ := ctx.Value(requestHeadersContextKey).(http.Header)
	return headers
}

// withRequestHeaders returns a context with a clone of the request headers attached.
func withRequestHeaders(ctx context.Context, headers http.Header) context.Context {
	if headers == nil {
		return context.WithValue(ctx, requestHeadersContextKey, http.Header{})
	}

	normalized := make(http.Header, len(headers))
	for key, values := range headers {
		normalized[core.NormalizeWebhookForwardHeader(key)] = append([]string(nil), values...)
	}
	return context.WithValue(ctx, requestHeadersContextKey, normalized)
}

// isWebhookTriggerPath checks if the URL path matches the webhook trigger
// endpoint pattern (POST /api/v1/webhooks/{fileName}). It looks for "/webhooks/"
// in the path with a non-empty segment after it.
func isWebhookTriggerPath(urlPath string) bool {
	path := strings.TrimRight(urlPath, "/")
	_, after, ok := strings.Cut(path, "/webhooks/")
	if !ok {
		return false
	}
	return after != ""
}
