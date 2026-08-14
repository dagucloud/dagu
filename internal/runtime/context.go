// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
)

// Type aliases for execution package types.
// These allow runtime package users to access execution types without importing execution directly.
type (
	// Context is an alias for execution.Context
	Context = runctx.Context
	// Database is an alias for execution.Database
	Database = runctx.Database
	// Dispatcher is an alias for execution.Dispatcher
	Dispatcher = dispatch.Dispatcher
	// RunStatus is an alias for execution.RunStatus
	RunStatus = ir.RunStatus
	// ContextOption is an alias for execution.ContextOption
	ContextOption = runctx.ContextOption
)

// Re-export execution package functions for convenience.
var (
	// NewContext creates a new context with DAG execution metadata.
	NewContext = runctx.NewContext
	// LookupDAGContext returns the DAG execution metadata when it is present.
	LookupDAGContext = runctx.LookupContext
	// WithDatabase sets the database interface.
	WithDatabase = runctx.WithDatabase
	// WithRootDAGRun sets the root DAG run reference for sub-DAG execution.
	WithRootDAGRun = runctx.WithRootDAGRun
	// WithRetryPath sets a targeted child DAG retry path.
	WithRetryPath = runctx.WithRetryPath
	// WithAttemptID sets the DAG-run attempt identifier.
	WithAttemptID = runctx.WithAttemptID
	// WithTriggerType sets the DAG-run trigger type.
	WithTriggerType = runctx.WithTriggerType
	// WithTriggerActor sets the attributable trigger actor.
	WithTriggerActor = runctx.WithTriggerActor
	// WithRunStartedAt sets the recorded DAG-run start timestamp.
	WithRunStartedAt = runctx.WithRunStartedAt
	// WithScheduleTime sets the logical schedule time.
	WithScheduleTime = runctx.WithScheduleTime
	// WithParams sets runtime parameters.
	WithParams = runctx.WithParams
	// WithDefaultEnvVars sets low-precedence inherited environment variables.
	WithDefaultEnvVars = runctx.WithDefaultEnvVars
	// WithEnvVars sets additional execution-scoped environment variables.
	WithEnvVars = runctx.WithEnvVars
	// WithCoordinator sets the coordinator dispatcher for distributed execution.
	WithCoordinator = runctx.WithCoordinator
	// WithDefaultSecrets sets low-precedence inherited secret environment variables.
	WithDefaultSecrets = runctx.WithDefaultSecrets
	// WithSecrets sets secret environment variables.
	WithSecrets = runctx.WithSecrets
	// WithLogEncoding sets the log file character encoding.
	WithLogEncoding = runctx.WithLogEncoding
	// WithLogWriterFactory sets the log writer factory for remote log streaming.
	WithLogWriterFactory = runctx.WithLogWriterFactory
	// WithDefaultExecMode sets the server-level default execution mode.
	WithDefaultExecMode = runctx.WithDefaultExecMode
	// WithDAGRunRepository sets the DAG-run repository.
	WithDAGRunRepository = runctx.WithDAGRunRepository
	// WithQueueStore sets the queue store.
	WithQueueStore = runctx.WithQueueStore
	// WithStateStore sets the persistent DAG state store.
	WithStateStore = runctx.WithStateStore
	// WithMaterializationStore sets the build materialization store.
	WithMaterializationStore = runctx.WithMaterializationStore
	// WithNoReuse records that manifest hits are disabled for the run.
	WithNoReuse = runctx.WithNoReuse
	// WithDAGRunLogDir sets the base log directory for newly persisted DAG runs.
	WithDAGRunLogDir = runctx.WithDAGRunLogDir
	// WithDAGRunArtifactDir sets the base artifact directory for newly persisted DAG runs.
	WithDAGRunArtifactDir = runctx.WithDAGRunArtifactDir
	// WithWorkDir sets the per-DAG-run working directory path.
	WithWorkDir = runctx.WithWorkDir
	// WithArtifactDir sets the per-DAG-run artifact directory path.
	WithArtifactDir = runctx.WithArtifactDir
	// WithRuntimeProfile sets selected runtime profile metadata.
	WithRuntimeProfile = runctx.WithRuntimeProfile
)

// LogWriterFactory is re-exported from execution package
type LogWriterFactory = runctx.LogWriterFactory

// GetDAGContext retrieves the DAGContext from the context.
// This is a convenience wrapper for execution.GetContext.
func GetDAGContext(ctx context.Context) Context {
	return runctx.GetContext(ctx)
}

// MustDAGContext retrieves the DAGContext from the context and panics when one
// is not present.
// This is a convenience wrapper for execution.MustContext.
func MustDAGContext(ctx context.Context) Context {
	return runctx.MustContext(ctx)
}

// WithDAGContext returns a new context with the given DAGContext.
// This is a convenience wrapper for execution.WithContext.
func WithDAGContext(ctx context.Context, rCtx Context) context.Context {
	return runctx.WithContext(ctx, rCtx)
}

// NewDAGRunRef is a convenience wrapper for execution.NewDAGRunRef.
func NewDAGRunRef(name, runID string) ir.DAGRunRef {
	return ir.NewDAGRunRef(name, runID)
}

// NewContextForTest creates a minimal context for testing purposes.
// This is useful when you need a context with just basic DAG metadata.
func NewContextForTest(ctx context.Context, dag *ir.DAG, dagRunID, logFile string) context.Context {
	return runctx.NewContext(ctx, dag, dagRunID, logFile)
}
