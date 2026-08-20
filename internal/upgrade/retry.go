// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/go-resty/resty/v2"
)

const (
	retryInitialInterval = 1 * time.Second
	retryMaxInterval     = 5 * time.Second
	retryMaxRetries      = 3
	retryBackoffFactor   = 2.0
)

// newUpgradeRetryPolicy creates the standard retry policy for the upgrade package.
// Matches codebase convention: exponential backoff + FullJitter.
func newUpgradeRetryPolicy() backoff.RetryPolicy {
	base := backoff.NewExponentialBackoffPolicy(retryInitialInterval)
	base.BackoffFactor = retryBackoffFactor
	base.MaxInterval = retryMaxInterval
	base.MaxRetries = retryMaxRetries
	return backoff.WithJitter(base, backoff.FullJitter)
}

// httpError carries an HTTP status code for retry classification.
type httpError struct {
	statusCode int
	message    string
}

func (e *httpError) Error() string { return e.message }

// nonRetriableError marks an error that should never be retried.
type nonRetriableError struct {
	err error
}

func (e *nonRetriableError) Error() string { return e.err.Error() }
func (e *nonRetriableError) Unwrap() error { return e.err }

// isRetriableError classifies errors for retry decisions:
//   - nonRetriableError → never retry
//   - httpError 429, 500-504 → retry
//   - httpError other (4xx) → never retry
//   - everything else (network, io) → retry
func isRetriableError(err error) bool {
	if _, ok := errors.AsType[*nonRetriableError](err); ok {
		return false
	}
	if he, ok := errors.AsType[*httpError](err); ok {
		return he.statusCode == 429 || (he.statusCode >= 500 && he.statusCode <= 504)
	}
	return true
}

// classifyResponse checks an HTTP response and returns an appropriate error:
//   - 2xx → nil
//   - 429, 500-504 → retriable httpError
//   - other → non-retriable httpError
func classifyResponse(resp *resty.Response) error {
	code := resp.StatusCode()
	if code >= 200 && code < 300 {
		return nil
	}
	return &httpError{
		statusCode: code,
		message:    fmt.Sprintf("HTTP %d: %s", code, resp.String()),
	}
}
