// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected int
	}{
		{"StatusNone", NotStarted, 0},
		{"StatusRunning", Running, 1},
		{"StatusError", Failed, 2},
		{"StatusAborted", Aborted, 3},
		{"StatusSuccess", Succeeded, 4},
		{"StatusQueued", Queued, 5},
		{"StatusPartialSuccess", PartiallySucceeded, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, int(tt.status))
		})
	}
}

func TestNodeStatusConstants(t *testing.T) {
	tests := []struct {
		name       string
		nodeStatus NodeStatus
		expected   int
	}{
		{"NodeStatusNone", NodeNotStarted, 0},
		{"NodeStatusRunning", NodeRunning, 1},
		{"NodeStatusError", NodeFailed, 2},
		{"NodeStatusAborted", NodeAborted, 3},
		{"NodeStatusSuccess", NodeSucceeded, 4},
		{"NodeStatusSkipped", NodeSkipped, 5},
		{"NodeStatusPartialSuccess", NodePartiallySucceeded, 6},
		{"NodeStatusWaiting", NodeWaiting, 7},
		{"NodeStatusRejected", NodeRejected, 8},
		{"NodeStatusRetrying", NodeRetrying, 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, int(tt.nodeStatus))
		})
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status   Status
		expected string
	}{
		{NotStarted, "not_started"},
		{Running, "running"},
		{Failed, "failed"},
		{Aborted, "aborted"},
		{Succeeded, "succeeded"},
		{Queued, "queued"},
		{PartiallySucceeded, "partially_succeeded"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

func TestNodeStatusString(t *testing.T) {
	tests := []struct {
		nodeStatus NodeStatus
		expected   string
	}{
		{NodeNotStarted, "not_started"},
		{NodeRunning, "running"},
		{NodeRetrying, "retrying"},
		{NodeFailed, "failed"},
		{NodeAborted, "aborted"},
		{NodeSucceeded, "succeeded"},
		{NodeSkipped, "skipped"},
		{NodePartiallySucceeded, "partially_succeeded"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.nodeStatus.String())
		})
	}
}

func TestNodeStatus_IsDone(t *testing.T) {
	tests := []struct {
		status   NodeStatus
		expected bool
	}{
		{NodeNotStarted, false},
		{NodeRunning, false},
		{NodeRetrying, false},
		{NodeWaiting, false},
		{NodeSucceeded, true},
		{NodeFailed, true},
		{NodeSkipped, true},
		{NodeAborted, true},
		{NodePartiallySucceeded, true},
		{NodeRejected, true},
	}

	for _, tt := range tests {
		t.Run(tt.status.String(), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.IsDone())
		})
	}
}
