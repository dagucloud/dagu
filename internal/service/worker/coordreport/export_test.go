// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport

import (
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

// LogBufferSize exposes the step log buffer threshold to black-box tests.
const LogBufferSize = logBufferSize

// MaxChunkSize exposes the stream chunk limit to black-box tests.
const MaxChunkSize = maxChunkSize

// StepLogWriter exposes the concrete step writer type to black-box tests.
type StepLogWriter = stepLogWriter

// StatusPusherSnapshot captures status pusher construction state for tests.
type StatusPusherSnapshot struct {
	WorkerID string
	Client   coordinator.Client
}

// SnapshotStatusPusher captures status pusher construction state.
func SnapshotStatusPusher(p *StatusPusher) StatusPusherSnapshot {
	return StatusPusherSnapshot{
		WorkerID: p.workerID,
		Client:   p.client,
	}
}

// LogStreamerSnapshot captures log streamer construction state for tests.
type LogStreamerSnapshot struct {
	WorkerID  string
	DAGRunID  string
	DAGName   string
	AttemptID string
	RootRef   exec.DAGRunRef
}

// SnapshotLogStreamer captures log streamer construction state.
func SnapshotLogStreamer(s *LogStreamer) LogStreamerSnapshot {
	return LogStreamerSnapshot{
		WorkerID:  s.workerID,
		DAGRunID:  s.dagRunID,
		DAGName:   s.dagName,
		AttemptID: s.getAttemptID(),
		RootRef:   s.rootRef,
	}
}

// LogStreamerAttemptID returns the current attempt ID.
func LogStreamerAttemptID(s *LogStreamer) string {
	return s.getAttemptID()
}

// StepLogWriterSnapshot captures mutable step writer state for tests.
type StepLogWriterSnapshot struct {
	StepName         string
	StreamType       int
	Streamer         *LogStreamer
	Closed           bool
	StreamInitFailed bool
	BufferLen        int
	Sequence         uint64
	HasStream        bool
	LineBuffered     bool
	Unbuffered       bool
}

// SnapshotStepLogWriter captures step writer state under its lock.
func SnapshotStepLogWriter(w *StepLogWriter) StepLogWriterSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	return snapshotStepLogWriterLocked(w)
}

func snapshotStepLogWriterLocked(w *StepLogWriter) StepLogWriterSnapshot {
	return StepLogWriterSnapshot{
		StepName:         w.stepName,
		StreamType:       w.streamType,
		Streamer:         w.streamer,
		Closed:           w.closed,
		StreamInitFailed: w.streamInitFailed,
		BufferLen:        len(w.buffer),
		Sequence:         w.sequence,
		HasStream:        w.stream != nil,
		LineBuffered:     w.lineBuffered,
		Unbuffered:       w.unbuffered,
	}
}

// StepLogWriterFlushResult captures a flush and the resulting writer state.
type StepLogWriterFlushResult struct {
	Err             error
	InitialSequence uint64
	FinalSequence   uint64
	BufferLen       int
	HasStream       bool
	StreamFailed    bool
}

// FlushStepLogWriterWithBuffer sets the writer buffer and flushes it under the writer lock.
func FlushStepLogWriterWithBuffer(w *StepLogWriter, data []byte) StepLogWriterFlushResult {
	w.mu.Lock()
	defer w.mu.Unlock()

	initialSequence := w.sequence
	w.buffer = data
	err := w.flush()

	return StepLogWriterFlushResult{
		Err:             err,
		InitialSequence: initialSequence,
		FinalSequence:   w.sequence,
		BufferLen:       len(w.buffer),
		HasStream:       w.stream != nil,
		StreamFailed:    w.streamInitFailed,
	}
}

// ToProtoStreamType exposes stream type conversion to black-box tests.
func ToProtoStreamType(streamType int) coordinatorv1.LogStreamType {
	return toProtoStreamType(streamType)
}
