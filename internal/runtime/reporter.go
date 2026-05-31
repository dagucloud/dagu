// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"io"
	"os"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// StatusPusher reports DAG run status outside the current execution process.
type StatusPusher interface {
	Push(ctx context.Context, status exec.DAGRunStatus) error
}

// AttemptRejected marks a status push failure caused by a non-authoritative attempt.
type AttemptRejected interface {
	error
	AttemptRejectedReason() string
}

// SchedulerLogStreamer streams a completed scheduler log.
type SchedulerLogStreamer interface {
	exec.LogWriterFactory
	NewSchedulerLogWriter(ctx context.Context, localFile *os.File) io.WriteCloser
	StreamSchedulerLog(ctx context.Context, logFilePath string) error
}

// ArtifactFinalizer persists artifacts before terminal status is reported.
type ArtifactFinalizer interface {
	Finalize(ctx context.Context, attemptID, dir string) error
}
