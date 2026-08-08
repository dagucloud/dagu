// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue

import "strings"

const staleQueueDispatchErrorPrefix = "stale queue dispatch"

// StaleQueueDispatchError reports that a scheduler-owned queued dispatch is no
// longer valid for the latest visible attempt.
type StaleQueueDispatchError struct {
	Reason string
}

func (e *StaleQueueDispatchError) Error() string {
	if e == nil || e.Reason == "" {
		return staleQueueDispatchErrorPrefix
	}
	return staleQueueDispatchErrorPrefix + ": " + e.Reason
}

// ParseStaleQueueDispatchError reconstructs a stale queue-dispatch error from a
// transport-safe string representation.
func ParseStaleQueueDispatchError(msg string) (*StaleQueueDispatchError, bool) {
	if msg == staleQueueDispatchErrorPrefix {
		return &StaleQueueDispatchError{}, true
	}

	if idx := strings.Index(msg, staleQueueDispatchErrorPrefix); idx >= 0 {
		msg = msg[idx:]
	} else {
		return nil, false
	}

	prefix := staleQueueDispatchErrorPrefix + ": "
	if msg == staleQueueDispatchErrorPrefix {
		return &StaleQueueDispatchError{}, true
	}
	if !strings.HasPrefix(msg, prefix) {
		return nil, false
	}

	return &StaleQueueDispatchError{Reason: strings.TrimPrefix(msg, prefix)}, true
}
