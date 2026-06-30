// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Sentinel errors for SSE operations.
var (
	ErrStreamingNotSupported = errors.New("streaming not supported")
	ErrQueryTooLong          = errors.New("query string exceeds maximum length")
)

// Event type constants for SSE messages.
const (
	EventTypeData      = "data"
	EventTypeHeartbeat = "heartbeat"
	EventTypeConnected = "connected"
	EventTypeError     = "error"
)

// Default configuration constants for the multiplexer.
const (
	defaultMaxClients   = 1000
	heartbeatInterval   = 10 * time.Second
	defaultBaseInterval = time.Second      // Minimum polling interval
	defaultMaxInterval  = 10 * time.Second // Maximum polling interval cap
	intervalMultiplier  = 3                // interval = multiplier * fetchDuration
	smoothingFactor     = 0.3              // EMA alpha: weight for new value (0.3 = 30% new, 70% old)
)

// TopicRefreshMode controls how a topic stays fresh after its initial snapshot.
type TopicRefreshMode string

const (
	// TopicRefreshModePolling continuously refetches while subscribers are present.
	TopicRefreshModePolling TopicRefreshMode = "polling"
	// TopicRefreshModeOnDemand refetches only on explicit wakeups, with retry-backoff on errors.
	TopicRefreshModeOnDemand TopicRefreshMode = "on_demand"
)

// TopicType identifies the type of data being watched.
type TopicType string

// Topic type constants. Each has a registered fetcher function.
// Identifier formats:
//   - TopicTypeDAGRun: "dagName/dagRunId"
//   - TopicTypeSubDAGRun: "dagName/dagRunId/subDAGRunId"
//   - TopicTypeDAG: "fileName"
//   - TopicTypeDAGHistory: "fileName"
//   - TopicTypeDAGRunLogs: "dagName/dagRunId"
//   - TopicTypeStepLog: "dagName/dagRunId/stepName"
//   - TopicTypeDAGRuns: URL query string (e.g., "limit=50&offset=0")
//   - TopicTypeQueueItems: "queueName"
//   - TopicTypeQueues: URL query string
//   - TopicTypeDAGsList: URL query string (e.g., "page=1&perPage=100&name=mydag")
const (
	TopicTypeDAGRun     TopicType = "dagrun"
	TopicTypeSubDAGRun  TopicType = "subdagrun"
	TopicTypeDAG        TopicType = "dag"
	TopicTypeDAGHistory TopicType = "daghistory"
	TopicTypeDAGRunLogs TopicType = "dagrunlogs"
	TopicTypeStepLog    TopicType = "steplog"
	TopicTypeDAGRuns    TopicType = "dagruns"
	TopicTypeQueueItems TopicType = "queueitems"
	TopicTypeQueues     TopicType = "queues"
	TopicTypeDAGsList   TopicType = "dagslist"
)

// FetchFunc fetches data for a given identifier. The returned data is JSON
// marshaled and sent to clients with the same structure as the REST API.
type FetchFunc func(ctx context.Context, identifier string) (any, error)

// computeHash returns a short hex digest of the given data for change detection.
func computeHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

// maxQueryLength is the maximum length of a sanitized query string used as a topic identifier.
const maxQueryLength = 4096

// SetSSEHeaders sets the standard headers required for SSE responses.
func SetSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

// streamResponse copies data from the response body to the client.
func streamResponse(w http.ResponseWriter, flusher http.Flusher, body io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

// parseAndSanitizeQuery parses and sanitizes a URL query string,
// removing sensitive parameters and enforcing a maximum length.
func parseAndSanitizeQuery(rawQuery string) (string, error) {
	if rawQuery == "" {
		return "", nil
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", err
	}

	// Remove sensitive parameters that should not be part of topic identity
	values.Del("token")
	values.Del("remoteNode")

	result := values.Encode()
	if len(result) > maxQueryLength {
		return "", ErrQueryTooLong
	}
	return result, nil
}
